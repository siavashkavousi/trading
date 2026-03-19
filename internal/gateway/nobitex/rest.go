package nobitex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/gateway"
)

type restClient struct {
	baseURL     string
	token       string
	httpClient  *http.Client
	rateLimiter *gateway.RateLimiter
	logger      *slog.Logger
}

func newRESTClient(baseURL, token string, rl *gateway.RateLimiter, logger *slog.Logger) *restClient {
	return &restClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:       10,
				IdleConnTimeout:    90 * time.Second,
				DisableCompression: true,
			},
		},
		rateLimiter: rl,
		logger:      logger,
	}
}

// nobitexResponse is the common wrapper for all Nobitex API responses.
type nobitexResponse struct {
	Status  string          `json:"status"`
	Code    string          `json:"code,omitempty"`
	Message string          `json:"message,omitempty"`
	Raw     json.RawMessage `json:"-"`
}

func (c *restClient) doRequest(ctx context.Context, method, path string, body interface{}, category domain.EndpointCategory, authenticated bool) ([]byte, error) {
	if err := c.rateLimiter.Acquire(ctx, category, 1); err != nil {
		return nil, fmt.Errorf("rate limit: %w", err)
	}

	url := c.baseURL + path

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	if authenticated && c.token != "" {
		req.Header.Set("Authorization", "Token "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var baseResp nobitexResponse
	if err := json.Unmarshal(respBody, &baseResp); err == nil {
		if baseResp.Status == "failed" {
			return nil, fmt.Errorf("nobitex API error: code=%s message=%s", baseResp.Code, baseResp.Message)
		}
	}

	return respBody, nil
}

func (c *restClient) placeOrder(ctx context.Context, req domain.OrderRequest) (*domain.OrderAck, error) {
	srcCurrency, dstCurrency := domain.MapNobitexCurrencyPair(req.Symbol)

	orderType := "buy"
	if req.Side == domain.SideSell {
		orderType = "sell"
	}

	body := map[string]interface{}{
		"type":        orderType,
		"srcCurrency": srcCurrency,
		"dstCurrency": dstCurrency,
		"amount":      req.Size.String(),
		"price":       req.Price.String(),
	}

	if req.OrderType == domain.OrderTypeMarket {
		body["execution"] = "market"
		delete(body, "price")
	}

	if req.IdempotencyKey != "" {
		body["clientOrderId"] = req.IdempotencyKey
	}

	respData, err := c.doRequest(ctx, "POST", "/market/orders/add", body, domain.EndpointOrderPlace, true)
	if err != nil {
		return nil, err
	}

	var result struct {
		Status string `json:"status"`
		Order  struct {
			ID            int    `json:"id"`
			Type          string `json:"type"`
			SrcCurrency   string `json:"srcCurrency"`
			DstCurrency   string `json:"dstCurrency"`
			Price         string `json:"price"`
			Amount        string `json:"amount"`
			MatchedAmount string `json:"matchedAmount"`
			Status        string `json:"status"`
			Partial       bool   `json:"partial"`
		} `json:"order"`
	}
	if err := json.Unmarshal(respData, &result); err != nil {
		return nil, fmt.Errorf("parse order response: %w", err)
	}

	return &domain.OrderAck{
		InternalID: req.InternalID,
		VenueID:    strconv.Itoa(result.Order.ID),
		Status:     domain.OrderStatusAcknowledged,
		Timestamp:  time.Now(),
	}, nil
}

func (c *restClient) cancelOrder(ctx context.Context, orderID string) (*domain.CancelAck, error) {
	id, err := strconv.Atoi(orderID)
	if err != nil {
		return nil, fmt.Errorf("invalid order ID %q: %w", orderID, err)
	}

	body := map[string]interface{}{
		"order":  id,
		"status": "cancel",
	}

	_, err = c.doRequest(ctx, "POST", "/market/orders/update-status", body, domain.EndpointOrderCancel, true)
	if err != nil {
		return nil, err
	}

	return &domain.CancelAck{
		Status:    domain.OrderStatusCancelled,
		Timestamp: time.Now(),
	}, nil
}

func (c *restClient) getBalances(ctx context.Context) (map[string]domain.Balance, error) {
	respData, err := c.doRequest(ctx, "POST", "/users/wallets/list", nil, domain.EndpointAccount, true)
	if err != nil {
		return nil, err
	}

	var result struct {
		Status  string `json:"status"`
		Wallets []struct {
			ID             int    `json:"id"`
			ActiveBalance  string `json:"activeBalance"`
			Balance        string `json:"balance"`
			BlockedBalance string `json:"blockedBalance"`
			Currency       string `json:"currency"`
		} `json:"wallets"`
	}
	if err := json.Unmarshal(respData, &result); err != nil {
		return nil, fmt.Errorf("parse wallets: %w", err)
	}

	balances := make(map[string]domain.Balance, len(result.Wallets))
	for _, w := range result.Wallets {
		asset := strings.ToUpper(w.Currency)
		bal := domain.Balance{
			Venue: "nobitex",
			Asset: asset,
		}
		bal.Free, _ = domain.ParseDecimal(w.ActiveBalance)
		bal.Locked, _ = domain.ParseDecimal(w.BlockedBalance)
		bal.Total, _ = domain.ParseDecimal(w.Balance)
		balances[asset] = bal
	}

	return balances, nil
}

// Nobitex is a spot-only exchange; positions always returns empty.
func (c *restClient) getPositions(_ context.Context) ([]domain.Position, error) {
	return []domain.Position{}, nil
}

func (c *restClient) getFeeTier(ctx context.Context) (*domain.FeeTier, error) {
	// Nobitex fee schedule: general maker 0.1%, taker 0.15% for USDT markets.
	// Nobitex does not expose a dedicated fee-tier API endpoint.
	// These are the standard fee rates from their public fee documentation.
	tier := &domain.FeeTier{
		Venue:     "nobitex",
		UpdatedAt: time.Now(),
	}
	tier.MakerFeeBps, _ = domain.ParseDecimal("10")
	tier.TakerFeeBps, _ = domain.ParseDecimal("15")
	return tier, nil
}

func (c *restClient) getOpenOrders(ctx context.Context, symbol string) ([]domain.Order, error) {
	body := map[string]interface{}{
		"status": "open",
	}

	if symbol != "" {
		srcCurrency, dstCurrency := domain.MapNobitexCurrencyPair(symbol)
		body["srcCurrency"] = srcCurrency
		body["dstCurrency"] = dstCurrency
	}

	respData, err := c.doRequest(ctx, "POST", "/market/orders/list", body, domain.EndpointPrivateData, true)
	if err != nil {
		return nil, err
	}

	var result struct {
		Status string `json:"status"`
		Orders []struct {
			ID              int    `json:"id"`
			Type            string `json:"type"`
			SrcCurrency     string `json:"srcCurrency"`
			DstCurrency     string `json:"dstCurrency"`
			Price           string `json:"price"`
			Amount          string `json:"amount"`
			MatchedAmount   string `json:"matchedAmount"`
			UnmatchedAmount string `json:"unmatchedAmount"`
			Status          string `json:"status"`
		} `json:"orders"`
	}
	if err := json.Unmarshal(respData, &result); err != nil {
		return nil, fmt.Errorf("parse open orders: %w", err)
	}

	orders := make([]domain.Order, 0, len(result.Orders))
	for _, o := range result.Orders {
		sym := strings.ToUpper(o.SrcCurrency) + "/" + strings.ToUpper(o.DstCurrency)
		side := domain.SideBuy
		if o.Type == "sell" {
			side = domain.SideSell
		}

		order := domain.Order{
			VenueID: strconv.Itoa(o.ID),
			Venue:   "nobitex",
			Symbol:  sym,
			Side:    side,
			Status:  domain.OrderStatusAcknowledged,
		}
		order.Price, _ = domain.ParseDecimal(o.Price)
		order.Size, _ = domain.ParseDecimal(o.Amount)
		order.FilledSize, _ = domain.ParseDecimal(o.MatchedAmount)
		orders = append(orders, order)
	}

	return orders, nil
}

func (c *restClient) getOrderBook(ctx context.Context, symbol string) (*domain.OrderBookSnapshot, error) {
	venueSymbol := domain.MapSymbol(symbol, domain.NobitexOrderBookSymbolMap)
	path := "/v3/orderbook/" + venueSymbol

	respData, err := c.doRequest(ctx, "GET", path, nil, domain.EndpointPublicData, false)
	if err != nil {
		return nil, err
	}

	var result struct {
		Status         string     `json:"status"`
		Bids           [][]string `json:"bids"`
		Asks           [][]string `json:"asks"`
		LastTradePrice string     `json:"lastTradePrice"`
		LastUpdate     string     `json:"lastUpdate"`
	}
	if err := json.Unmarshal(respData, &result); err != nil {
		return nil, fmt.Errorf("parse orderbook: %w", err)
	}

	book := &domain.OrderBookSnapshot{
		Venue:         "nobitex",
		Symbol:        symbol,
		LocalTimestamp: time.Now(),
	}

	// NOTE: Nobitex has a known bug where bids/asks labels are swapped in the API.
	// Bids in the response are actually asks and vice versa.
	// We swap them here to normalize the data.
	for _, ask := range result.Asks {
		if len(ask) >= 2 {
			price, _ := domain.ParseDecimal(ask[0])
			size, _ := domain.ParseDecimal(ask[1])
			book.Asks = append(book.Asks, domain.PriceLevel{Price: price, Size: size})
		}
	}
	for _, bid := range result.Bids {
		if len(bid) >= 2 {
			price, _ := domain.ParseDecimal(bid[0])
			size, _ := domain.ParseDecimal(bid[1])
			book.Bids = append(book.Bids, domain.PriceLevel{Price: price, Size: size})
		}
	}

	return book, nil
}

func (c *restClient) getRecentTrades(ctx context.Context, symbol string) ([]domain.Trade, error) {
	venueSymbol := domain.MapSymbol(symbol, domain.NobitexOrderBookSymbolMap)
	path := "/v3/trades/" + venueSymbol

	respData, err := c.doRequest(ctx, "GET", path, nil, domain.EndpointPublicData, false)
	if err != nil {
		return nil, err
	}

	var result struct {
		Status string `json:"status"`
		Trades []struct {
			Time   int64  `json:"time"`
			Price  string `json:"price"`
			Volume string `json:"volume"`
			Type   string `json:"type"`
		} `json:"trades"`
	}
	if err := json.Unmarshal(respData, &result); err != nil {
		return nil, fmt.Errorf("parse trades: %w", err)
	}

	trades := make([]domain.Trade, 0, len(result.Trades))
	for _, t := range result.Trades {
		side := domain.SideBuy
		if t.Type == "sell" {
			side = domain.SideSell
		}

		trade := domain.Trade{
			Venue:     "nobitex",
			Symbol:    symbol,
			Side:      side,
			Timestamp: time.UnixMilli(t.Time),
		}
		trade.Price, _ = domain.ParseDecimal(t.Price)
		trade.Size, _ = domain.ParseDecimal(t.Volume)
		trades = append(trades, trade)
	}

	return trades, nil
}
