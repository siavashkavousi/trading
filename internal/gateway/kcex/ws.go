package kcex

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/crypto-trading/trading/internal/domain"
)

type wsClient struct {
	url    string
	conn   *websocket.Conn
	mu     sync.Mutex
	logger *slog.Logger

	reconnectMax   time.Duration
	reconnectBase  time.Duration
	maxFailures    int

	orderBookChans map[string]chan domain.OrderBookDelta
	tradeChans     map[string]chan domain.Trade
	fundingChans   map[string]chan domain.FundingRate
	chanMu         sync.RWMutex
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
	ws.logger.Info("websocket connected", "url", ws.url)
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
			ws.logger.Warn("reconnect attempt failed",
				"attempt", i+1, "error", err)
			delay *= 2
			if delay > ws.reconnectMax {
				delay = ws.reconnectMax
			}
			continue
		}
		return nil
	}
	return fmt.Errorf("failed to reconnect after %d attempts", ws.maxFailures)
}

func (ws *wsClient) subscribe(symbol, channel string) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.conn == nil {
		return fmt.Errorf("websocket not connected")
	}

	msg := map[string]interface{}{
		"op":      "subscribe",
		"channel": channel,
		"args":    []string{symbol},
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
			ws.logger.Error("websocket read error", "error", err)
			if reconnErr := ws.reconnect(ctx); reconnErr != nil {
				ws.logger.Error("reconnection failed permanently", "error", reconnErr)
				return
			}
			continue
		}

		ws.handleMessage(message)
	}
}

func (ws *wsClient) handleMessage(msg []byte) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(msg, &raw); err != nil {
		ws.logger.Warn("failed to parse websocket message", "error", err)
		return
	}

	channelRaw, ok := raw["channel"]
	if !ok {
		return
	}

	var channel string
	if err := json.Unmarshal(channelRaw, &channel); err != nil {
		return
	}

	switch channel {
	case "orderbook":
		ws.handleOrderBookMessage(raw)
	case "trades":
		ws.handleTradeMessage(raw)
	case "funding":
		ws.handleFundingMessage(raw)
	}
}

func (ws *wsClient) handleOrderBookMessage(raw map[string]json.RawMessage) {
	ws.chanMu.RLock()
	defer ws.chanMu.RUnlock()

	var symbolStr string
	if s, ok := raw["symbol"]; ok {
		_ = json.Unmarshal(s, &symbolStr)
	}

	ch, ok := ws.orderBookChans[symbolStr]
	if !ok {
		return
	}

	delta := domain.OrderBookDelta{
		Venue:         "kcex",
		Symbol:        symbolStr,
		LocalTimestamp: time.Now(),
	}

	select {
	case ch <- delta:
	default:
	}
}

func (ws *wsClient) handleTradeMessage(raw map[string]json.RawMessage) {
	ws.chanMu.RLock()
	defer ws.chanMu.RUnlock()

	var symbolStr string
	if s, ok := raw["symbol"]; ok {
		_ = json.Unmarshal(s, &symbolStr)
	}

	ch, ok := ws.tradeChans[symbolStr]
	if !ok {
		return
	}

	trade := domain.Trade{
		Venue:     "kcex",
		Symbol:    symbolStr,
		Timestamp: time.Now(),
	}

	select {
	case ch <- trade:
	default:
	}
}

func (ws *wsClient) handleFundingMessage(raw map[string]json.RawMessage) {
	ws.chanMu.RLock()
	defer ws.chanMu.RUnlock()

	var symbolStr string
	if s, ok := raw["symbol"]; ok {
		_ = json.Unmarshal(s, &symbolStr)
	}

	ch, ok := ws.fundingChans[symbolStr]
	if !ok {
		return
	}

	rate := domain.FundingRate{
		Venue:     "kcex",
		Symbol:    symbolStr,
		Timestamp: time.Now(),
	}

	select {
	case ch <- rate:
	default:
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
	if ws.conn != nil {
		return ws.conn.Close()
	}
	return nil
}
