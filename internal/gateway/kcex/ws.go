package kcex

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/domain"
)

// wsToken holds the connection details returned from the bullet endpoint.
type wsToken struct {
	token        string
	endpoint     string
	pingInterval time.Duration
	pingTimeout  time.Duration
}

type wsClient struct {
	fallbackURL string
	rest        *restClient
	conn        *websocket.Conn
	mu          sync.Mutex
	logger      *slog.Logger

	reconnectMax  time.Duration
	reconnectBase time.Duration
	maxFailures   int

	subscriptions []wsSubscription
	pingInterval  time.Duration
	stopPing      chan struct{}

	orderBookChans map[string]chan domain.OrderBookDelta
	tradeChans     map[string]chan domain.Trade
	fundingChans   map[string]chan domain.FundingRate
	chanMu         sync.RWMutex
}

type wsSubscription struct {
	topic          string
	privateChannel bool
}

func newWSClient(fallbackURL string, rest *restClient, logger *slog.Logger) *wsClient {
	return &wsClient{
		fallbackURL:    fallbackURL,
		rest:           rest,
		logger:         logger,
		reconnectBase:  100 * time.Millisecond,
		reconnectMax:   30 * time.Second,
		maxFailures:    5,
		pingInterval:   18 * time.Second,
		orderBookChans: make(map[string]chan domain.OrderBookDelta),
		tradeChans:     make(map[string]chan domain.Trade),
		fundingChans:   make(map[string]chan domain.FundingRate),
	}
}

func (ws *wsClient) connect(ctx context.Context) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Get WS connection token from the REST API
	token, err := ws.rest.getWSToken(ctx, false)
	if err != nil {
		ws.logger.Warn("failed to get KCEX WS token, using fallback URL", "error", err)
		return ws.connectDirect(ctx, ws.fallbackURL)
	}

	connectID := uuid.New().String()
	wsURL := fmt.Sprintf("%s?token=%s&connectId=%s", token.endpoint, token.token, connectID)

	if token.pingInterval > 0 {
		ws.pingInterval = token.pingInterval
	}

	return ws.connectDirect(ctx, wsURL)
}

func (ws *wsClient) connectDirect(ctx context.Context, url string) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("websocket connect to %s: %w", url, err)
	}

	ws.conn = conn

	// Wait for welcome message
	ws.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := ws.conn.ReadMessage()
	ws.conn.SetReadDeadline(time.Time{})
	if err != nil {
		ws.conn.Close()
		ws.conn = nil
		return fmt.Errorf("failed to read welcome message: %w", err)
	}

	var welcome struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(msg, &welcome); err == nil && welcome.Type == "welcome" {
		ws.logger.Info("kcex websocket connected", "id", welcome.ID)
	}

	ws.stopPing = make(chan struct{})
	go ws.pingLoop()

	return nil
}

func (ws *wsClient) pingLoop() {
	ticker := time.NewTicker(ws.pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ws.stopPing:
			return
		case <-ticker.C:
			ws.mu.Lock()
			if ws.conn != nil {
				msg := map[string]interface{}{
					"id":   uuid.New().String(),
					"type": "ping",
				}
				if err := ws.conn.WriteJSON(msg); err != nil {
					ws.logger.Warn("kcex websocket ping failed", "error", err)
				}
			}
			ws.mu.Unlock()
		}
	}
}

func (ws *wsClient) reconnect(ctx context.Context) error {
	delay := ws.reconnectBase
	for i := 0; i < ws.maxFailures; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}

		if err := ws.connect(ctx); err != nil {
			ws.logger.Warn("kcex reconnect attempt failed",
				"attempt", i+1, "error", err)
			delay *= 2
			if delay > ws.reconnectMax {
				delay = ws.reconnectMax
			}
			continue
		}
		for _, sub := range ws.subscriptions {
			if err := ws.sendSubscribe(sub.topic, sub.privateChannel); err != nil {
				ws.logger.Warn("failed to resubscribe after reconnect",
					"topic", sub.topic, "error", err)
			}
		}
		return nil
	}
	return fmt.Errorf("failed to reconnect after %d attempts", ws.maxFailures)
}

func (ws *wsClient) subscribe(topic string, private bool) error {
	ws.subscriptions = append(ws.subscriptions, wsSubscription{topic: topic, privateChannel: private})
	return ws.sendSubscribe(topic, private)
}

func (ws *wsClient) sendSubscribe(topic string, private bool) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.conn == nil {
		return fmt.Errorf("websocket not connected")
	}

	msg := map[string]interface{}{
		"id":             uuid.New().String(),
		"type":           "subscribe",
		"topic":          topic,
		"privateChannel": private,
		"response":       true,
	}
	return ws.conn.WriteJSON(msg)
}

func (ws *wsClient) readPump(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		ws.mu.Lock()
		conn := ws.conn
		ws.mu.Unlock()

		if conn == nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			ws.logger.Error("kcex websocket read error", "error", err)
			if reconnErr := ws.reconnect(ctx); reconnErr != nil {
				ws.logger.Error("kcex reconnection failed permanently", "error", reconnErr)
				return
			}
			continue
		}

		ws.handleMessage(message)
	}
}

// KCEX WebSocket message format:
// {"type":"message","topic":"/market/level2:BTC-USDT","subject":"trade.l2update","data":{...}}
// {"type":"message","topic":"/market/match:BTC-USDT","subject":"trade.l3match","data":{...}}
// {"type":"message","topic":"/contract/instrument:BTCUSDTM","subject":"funding.rate","data":{...}}
func (ws *wsClient) handleMessage(msg []byte) {
	var raw struct {
		Type    string          `json:"type"`
		Topic   string          `json:"topic"`
		Subject string          `json:"subject"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(msg, &raw); err != nil {
		ws.logger.Debug("failed to parse kcex websocket message", "error", err)
		return
	}

	switch raw.Type {
	case "pong", "welcome", "ack":
		return
	case "message":
		// Process market data messages
	default:
		return
	}

	topic := raw.Topic

	switch {
	case matchPrefix(topic, "/market/level2:"):
		symbol := topic[len("/market/level2:"):]
		ws.handleOrderBookMessage(symbol, raw.Data)
	case matchPrefix(topic, "/market/match:"):
		symbol := topic[len("/market/match:"):]
		ws.handleTradeMessage(symbol, raw.Data)
	case matchPrefix(topic, "/contract/instrument:"):
		symbol := topic[len("/contract/instrument:"):]
		ws.handleFundingMessage(symbol, raw.Subject, raw.Data)
	}
}

func matchPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func (ws *wsClient) handleOrderBookMessage(symbol string, data json.RawMessage) {
	ws.chanMu.RLock()
	ch, ok := ws.orderBookChans[symbol]
	ws.chanMu.RUnlock()
	if !ok {
		return
	}

	var update struct {
		SequenceStart int64      `json:"sequenceStart"`
		SequenceEnd   int64      `json:"sequenceEnd"`
		Changes       struct {
			Bids [][]string `json:"bids"`
			Asks [][]string `json:"asks"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(data, &update); err != nil {
		ws.logger.Warn("failed to parse kcex orderbook update", "error", err)
		return
	}

	delta := domain.OrderBookDelta{
		Venue:          "kcex",
		Symbol:         symbol,
		Sequence:       uint64(update.SequenceEnd),
		LocalTimestamp:  time.Now(),
	}

	for _, bid := range update.Changes.Bids {
		if len(bid) >= 2 {
			price, _ := decimal.NewFromString(bid[0])
			size, _ := decimal.NewFromString(bid[1])
			delta.Bids = append(delta.Bids, domain.PriceLevel{Price: price, Size: size})
		}
	}
	for _, ask := range update.Changes.Asks {
		if len(ask) >= 2 {
			price, _ := decimal.NewFromString(ask[0])
			size, _ := decimal.NewFromString(ask[1])
			delta.Asks = append(delta.Asks, domain.PriceLevel{Price: price, Size: size})
		}
	}

	select {
	case ch <- delta:
	default:
		ws.logger.Debug("kcex orderbook channel full, dropping update", "symbol", symbol)
	}
}

func (ws *wsClient) handleTradeMessage(symbol string, data json.RawMessage) {
	ws.chanMu.RLock()
	ch, ok := ws.tradeChans[symbol]
	ws.chanMu.RUnlock()
	if !ok {
		return
	}

	var match struct {
		Sequence    string `json:"sequence"`
		Symbol      string `json:"symbol"`
		Side        string `json:"side"`
		Size        string `json:"size"`
		Price       string `json:"price"`
		TradeID     string `json:"tradeId"`
		TakerOrdID  string `json:"takerOrderId"`
		MakerOrdID  string `json:"makerOrderId"`
		Time        string `json:"time"`
	}
	if err := json.Unmarshal(data, &match); err != nil {
		ws.logger.Warn("failed to parse kcex trade match", "error", err)
		return
	}

	side := domain.SideBuy
	if match.Side == "sell" {
		side = domain.SideSell
	}

	trade := domain.Trade{
		Venue:   "kcex",
		Symbol:  symbol,
		Side:    side,
		TradeID: match.TradeID,
	}
	trade.Price, _ = decimal.NewFromString(match.Price)
	trade.Size, _ = decimal.NewFromString(match.Size)

	ts, err := decimal.NewFromString(match.Time)
	if err == nil {
		trade.Timestamp = time.UnixMilli(ts.IntPart())
	} else {
		trade.Timestamp = time.Now()
	}

	select {
	case ch <- trade:
	default:
		ws.logger.Debug("kcex trade channel full, dropping update", "symbol", symbol)
	}
}

func (ws *wsClient) handleFundingMessage(symbol, subject string, data json.RawMessage) {
	if subject != "funding.rate" {
		return
	}

	ws.chanMu.RLock()
	ch, ok := ws.fundingChans[symbol]
	ws.chanMu.RUnlock()
	if !ok {
		return
	}

	var update struct {
		Granularity int     `json:"granularity"`
		FundingRate float64 `json:"fundingRate"`
		Timestamp   int64   `json:"timestamp"`
	}
	if err := json.Unmarshal(data, &update); err != nil {
		ws.logger.Warn("failed to parse kcex funding rate", "error", err)
		return
	}

	rate := domain.FundingRate{
		Venue:     "kcex",
		Symbol:    symbol,
		Timestamp: time.UnixMilli(update.Timestamp),
	}
	rate.Rate = decimal.NewFromFloat(update.FundingRate)

	select {
	case ch <- rate:
	default:
		ws.logger.Debug("kcex funding channel full, dropping update", "symbol", symbol)
	}
}

func (ws *wsClient) subscribeOrderBook(symbol string) <-chan domain.OrderBookDelta {
	ws.chanMu.Lock()
	defer ws.chanMu.Unlock()
	ch := make(chan domain.OrderBookDelta, 256)
	ws.orderBookChans[symbol] = ch
	return ch
}

func (ws *wsClient) subscribeTrades(symbol string) <-chan domain.Trade {
	ws.chanMu.Lock()
	defer ws.chanMu.Unlock()
	ch := make(chan domain.Trade, 256)
	ws.tradeChans[symbol] = ch
	return ch
}

func (ws *wsClient) subscribeFunding(symbol string) <-chan domain.FundingRate {
	ws.chanMu.Lock()
	defer ws.chanMu.Unlock()
	ch := make(chan domain.FundingRate, 256)
	ws.fundingChans[symbol] = ch
	return ch
}

func (ws *wsClient) close() error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.stopPing != nil {
		close(ws.stopPing)
	}

	if ws.conn != nil {
		return ws.conn.Close()
	}
	return nil
}
