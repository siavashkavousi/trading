package nobitex

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/domain"
)

type wsClient struct {
	url    string
	conn   *websocket.Conn
	mu     sync.Mutex
	logger *slog.Logger

	reconnectMax  time.Duration
	reconnectBase time.Duration
	maxFailures   int
	failureCount  int

	subscriptions []wsSubscription

	orderBookChans map[string]chan domain.OrderBookDelta
	tradeChans     map[string]chan domain.Trade
	fundingChans   map[string]chan domain.FundingRate
	chanMu         sync.RWMutex
}

type wsSubscription struct {
	symbol  string
	channel string
}

func newWSClient(url string, logger *slog.Logger) *wsClient {
	return &wsClient{
		url:            url,
		logger:         logger,
		reconnectBase:  100 * time.Millisecond,
		reconnectMax:   30 * time.Second,
		maxFailures:    5,
		orderBookChans: make(map[string]chan domain.OrderBookDelta),
		tradeChans:     make(map[string]chan domain.Trade),
		fundingChans:   make(map[string]chan domain.FundingRate),
	}
}

func (ws *wsClient) connect(ctx context.Context) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, ws.url, nil)
	if err != nil {
		return fmt.Errorf("websocket connect to %s: %w", ws.url, err)
	}

	ws.conn = conn
	ws.failureCount = 0
	ws.logger.Info("nobitex websocket connected", "url", ws.url)
	return nil
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
			ws.logger.Warn("nobitex reconnect attempt failed",
				"attempt", i+1, "error", err)
			delay *= 2
			if delay > ws.reconnectMax {
				delay = ws.reconnectMax
			}
			continue
		}
		for _, sub := range ws.subscriptions {
			if err := ws.sendSubscribe(sub.symbol, sub.channel); err != nil {
				ws.logger.Warn("failed to resubscribe after reconnect",
					"symbol", sub.symbol, "channel", sub.channel, "error", err)
			}
		}
		return nil
	}
	ws.failureCount++
	return fmt.Errorf("failed to reconnect after %d attempts", ws.maxFailures)
}

func (ws *wsClient) subscribe(symbol, channel string) error {
	ws.subscriptions = append(ws.subscriptions, wsSubscription{symbol: symbol, channel: channel})
	return ws.sendSubscribe(symbol, channel)
}

func (ws *wsClient) sendSubscribe(symbol, channel string) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.conn == nil {
		return fmt.Errorf("websocket not connected")
	}

	// Nobitex WS subscribe format
	msg := map[string]interface{}{
		"method": "subscribe",
		"params": map[string]interface{}{
			"channel": channel + ":" + symbol,
		},
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
			ws.logger.Error("nobitex websocket read error", "error", err)
			if reconnErr := ws.reconnect(ctx); reconnErr != nil {
				ws.logger.Error("nobitex reconnection failed permanently", "error", reconnErr)
				return
			}
			continue
		}

		ws.handleMessage(message)
	}
}

func (ws *wsClient) handleMessage(msg []byte) {
	var raw struct {
		Channel string          `json:"channel"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(msg, &raw); err != nil {
		ws.logger.Debug("failed to parse nobitex websocket message", "error", err)
		return
	}

	if raw.Channel == "" {
		return
	}

	// Channel format: "orderbook:BTCUSDT", "trades:BTCUSDT"
	channelType, symbol := parseChannel(raw.Channel)

	switch channelType {
	case "orderbook":
		ws.handleOrderBookMessage(symbol, raw.Data)
	case "trades":
		ws.handleTradeMessage(symbol, raw.Data)
	}
}

func parseChannel(ch string) (string, string) {
	for i := 0; i < len(ch); i++ {
		if ch[i] == ':' {
			return ch[:i], ch[i+1:]
		}
	}
	return ch, ""
}

func (ws *wsClient) handleOrderBookMessage(symbol string, data json.RawMessage) {
	ws.chanMu.RLock()
	ch, ok := ws.orderBookChans[symbol]
	ws.chanMu.RUnlock()
	if !ok {
		return
	}

	var update struct {
		Bids [][]string `json:"bids"`
		Asks [][]string `json:"asks"`
	}
	if err := json.Unmarshal(data, &update); err != nil {
		ws.logger.Warn("failed to parse nobitex orderbook update", "error", err)
		return
	}

	delta := domain.OrderBookDelta{
		Venue:          "nobitex",
		Symbol:         symbol,
		LocalTimestamp:  time.Now(),
	}

	for _, bid := range update.Bids {
		if len(bid) >= 2 {
			price, _ := decimal.NewFromString(bid[0])
			size, _ := decimal.NewFromString(bid[1])
			delta.Bids = append(delta.Bids, domain.PriceLevel{Price: price, Size: size})
		}
	}
	for _, ask := range update.Asks {
		if len(ask) >= 2 {
			price, _ := decimal.NewFromString(ask[0])
			size, _ := decimal.NewFromString(ask[1])
			delta.Asks = append(delta.Asks, domain.PriceLevel{Price: price, Size: size})
		}
	}

	select {
	case ch <- delta:
	default:
		ws.logger.Debug("nobitex orderbook channel full, dropping update", "symbol", symbol)
	}
}

func (ws *wsClient) handleTradeMessage(symbol string, data json.RawMessage) {
	ws.chanMu.RLock()
	ch, ok := ws.tradeChans[symbol]
	ws.chanMu.RUnlock()
	if !ok {
		return
	}

	var update struct {
		Price  string `json:"price"`
		Volume string `json:"volume"`
		Type   string `json:"type"`
		Time   int64  `json:"time"`
	}
	if err := json.Unmarshal(data, &update); err != nil {
		ws.logger.Warn("failed to parse nobitex trade update", "error", err)
		return
	}

	side := domain.SideBuy
	if update.Type == "sell" {
		side = domain.SideSell
	}

	trade := domain.Trade{
		Venue:     "nobitex",
		Symbol:    symbol,
		Side:      side,
		Timestamp: time.UnixMilli(update.Time),
	}
	trade.Price, _ = decimal.NewFromString(update.Price)
	trade.Size, _ = decimal.NewFromString(update.Volume)

	if trade.Timestamp.IsZero() {
		trade.Timestamp = time.Now()
	}

	select {
	case ch <- trade:
	default:
		ws.logger.Debug("nobitex trade channel full, dropping update", "symbol", symbol)
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

	// Nobitex is spot-only; funding rate channel will never receive data
	ch := make(chan domain.FundingRate, 8)
	ws.fundingChans[symbol] = ch
	return ch
}

func (ws *wsClient) close() error {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.conn != nil {
		return ws.conn.Close()
	}
	return nil
}
