package kcex

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/gateway"
)

type restClient struct {
	baseURL       string
	apiKey        string
	apiSecret     string
	apiPassphrase string
	httpClient    *http.Client
	rateLimiter   *gateway.RateLimiter
	logger        *slog.Logger
}

func newRESTClient(baseURL, apiKey, apiSecret, passphrase string, rl *gateway.RateLimiter, logger *slog.Logger) *restClient {
	return &restClient{
		baseURL:       baseURL,
		apiKey:        apiKey,
		apiSecret:     apiSecret,
		apiPassphrase: passphrase,
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

// sign creates a Base64-encoded HMAC-SHA256 signature for KCEX (KuCoin-style auth).
// The signature string is: timestamp + method + endpoint + body
func (c *restClient) sign(payload string) string {
	mac := hmac.New(sha256.New, []byte(c.apiSecret))
	mac.Write([]byte(payload))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// signPassphrase creates a Base64-encoded HMAC-SHA256 of the passphrase using the API secret.
func (c *restClient) signPassphrase() string {
	mac := hmac.New(sha256.New, []byte(c.apiSecret))
	mac.Write([]byte(c.apiPassphrase))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func (c *restClient) doRequest(ctx context.Context, method, path string, body interface{}, category domain.EndpointCategory) ([]byte, error) {
	if err := c.rateLimiter.Acquire(ctx, category, 1); err != nil {
		return nil, fmt.Errorf("rate limit: %w", err)
	}

	url := c.baseURL + path

	var reqBody io.Reader
	var payload string
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		payload = string(data)
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	if c.apiKey != "" {
		timestamp := fmt.Sprintf("%d", time.Now().UnixMilli())
		signData := timestamp + method + path + payload
		signature := c.sign(signData)

		req.Header.Set("KC-API-KEY", c.apiKey)
		req.Header.Set("KC-API-SIGN", signature)
		req.Header.Set("KC-API-TIMESTAMP", timestamp)
		req.Header.Set("KC-API-PASSPHRASE", c.signPassphrase())
		req.Header.Set("KC-API-KEY-VERSION", "2")
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

	// KCEX wraps all responses in {"code": "200000", "data": ...}
	var baseResp struct {
		Code string          `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(respBody, &baseResp); err != nil {
		return nil, fmt.Errorf("parse response wrapper: %w", err)
	}

	if baseResp.Code != "200000" {
		return nil, fmt.Errorf("KCEX API error: code=%s msg=%s", baseResp.Code, baseResp.Msg)
	}

	return baseResp.Data, nil
}

// doPublicRequest performs a request without authentication for public endpoints.
func (c *restClient) doPublicRequest(ctx context.Context, method, path string, category domain.EndpointCategory) ([]byte, error) {
	if err := c.rateLimiter.Acquire(ctx, category, 1); err != nil {
		return nil, fmt.Errorf("rate limit: %w", err)
	}

	url := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
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

	var baseResp struct {
		Code string          `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(respBody, &baseResp); err != nil {
		return nil, fmt.Errorf("parse response wrapper: %w", err)
	}

	if baseResp.Code != "200000" {
		return nil, fmt.Errorf("KCEX API error: code=%s msg=%s", baseResp.Code, baseResp.Msg)
	}

	return baseResp.Data, nil
}

func (c *restClient) placeOrder(ctx context.Context, req domain.OrderRequest) (*domain.OrderAck, error) {
	venueSymbol := domain.MapKCEXSymbol(req.Symbol)
	isFutures := domain.IsKCEXFutures(req.Symbol)

	side := "buy"
	if req.Side == domain.SideSell {
		side = "sell"
	}

	body := map[string]interface{}{
		"clientOid": req.IdempotencyKey,
		"side":      side,
		"symbol":    venueSymbol,
		"size":      req.Size.String(),
	}

	if req.OrderType == domain.OrderTypeLimit {
		body["type"] = "limit"
		body["price"] = req.Price.String()
	} else {
		body["type"] = "market"
	}

	if isFutures {
		body["leverage"] = "1"
	}

	var path string
	if isFutures {
		path = "/api/v1/futures/orders"
	} else {
		path = "/api/v1/orders"
	}

	data, err := c.doRequest(ctx, "POST", path, body, domain.EndpointOrderPlace)
	if err != nil {
		return nil, err
	}

	var result struct {
		OrderID string `json:"orderId"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse order response: %w", err)
	}

	return &domain.OrderAck{
		InternalID: req.InternalID,
		VenueID:    result.OrderID,
		Status:     domain.OrderStatusAcknowledged,
		Timestamp:  time.Now(),
	}, nil
}

func (c *restClient) cancelOrder(ctx context.Context, orderID string) (*domain.CancelAck, error) {
	path := fmt.Sprintf("/api/v1/orders/%s", orderID)
	_, err := c.doRequest(ctx, "DELETE", path, nil, domain.EndpointOrderCancel)
	if err != nil {
		return nil, err
	}

	return &domain.CancelAck{
		Status:    domain.OrderStatusCancelled,
		Timestamp: time.Now(),
	}, nil
}

func (c *restClient) getBalances(ctx context.Context) (map[string]domain.Balance, error) {
	data, err := c.doRequest(ctx, "GET", "/api/v1/accounts", nil, domain.EndpointAccount)
	if err != nil {
		return nil, err
	}

	var accounts []struct {
		ID        string `json:"id"`
		Currency  string `json:"currency"`
		Type      string `json:"type"`
		Balance   string `json:"balance"`
		Available string `json:"available"`
		Holds     string `json:"holds"`
	}
	if err := json.Unmarshal(data, &accounts); err != nil {
		return nil, fmt.Errorf("parse accounts: %w", err)
	}

	balances := make(map[string]domain.Balance, len(accounts))
	for _, a := range accounts {
		if a.Type != "trade" {
			continue
		}
		bal := domain.Balance{
			Venue: "kcex",
			Asset: a.Currency,
		}
		bal.Free, _ = domain.ParseDecimal(a.Available)
		bal.Locked, _ = domain.ParseDecimal(a.Holds)
		bal.Total, _ = domain.ParseDecimal(a.Balance)
		balances[a.Currency] = bal
	}

	return balances, nil
}

func (c *restClient) getPositions(ctx context.Context) ([]domain.Position, error) {
	data, err := c.doRequest(ctx, "GET", "/api/v1/positions", nil, domain.EndpointAccount)
	if err != nil {
		return nil, err
	}

	var positions []struct {
		Symbol        string `json:"symbol"`
		CurrentQty    int64  `json:"currentQty"`
		AvgEntryPrice string `json:"avgEntryPrice"`
		UnrealisedPnl string `json:"unrealisedPnl"`
		MaintMargin   string `json:"maintMargin"`
		RealLeverage  string `json:"realLeverage"`
		IsOpen        bool   `json:"isOpen"`
	}
	if err := json.Unmarshal(data, &positions); err != nil {
		return nil, fmt.Errorf("parse positions: %w", err)
	}

	result := make([]domain.Position, 0, len(positions))
	for _, p := range positions {
		if !p.IsOpen {
			continue
		}
		pos := domain.Position{
			Venue:          "kcex",
			Asset:          p.Symbol,
			InstrumentType: domain.InstrumentPerp,
			UpdatedAt:      time.Now(),
		}
		pos.Size, _ = domain.ParseDecimal(fmt.Sprintf("%d", p.CurrentQty))
		pos.EntryPrice, _ = domain.ParseDecimal(p.AvgEntryPrice)
		pos.UnrealizedPnL, _ = domain.ParseDecimal(p.UnrealisedPnl)
		pos.MarginUsed, _ = domain.ParseDecimal(p.MaintMargin)
		result = append(result, pos)
	}

	return result, nil
}

func (c *restClient) getFeeTier(ctx context.Context) (*domain.FeeTier, error) {
	data, err := c.doRequest(ctx, "GET", "/api/v1/trade-fees?symbols=BTC-USDT", nil, domain.EndpointAccount)
	if err != nil {
		return nil, err
	}

	var fees []struct {
		Symbol       string `json:"symbol"`
		TakerFeeRate string `json:"takerFeeRate"`
		MakerFeeRate string `json:"makerFeeRate"`
	}
	if err := json.Unmarshal(data, &fees); err != nil {
		return nil, fmt.Errorf("parse fee tier: %w", err)
	}

	tier := &domain.FeeTier{
		Venue:     "kcex",
		UpdatedAt: time.Now(),
	}

	if len(fees) > 0 {
		tier.MakerFeeBps, _ = domain.ParseDecimal(fees[0].MakerFeeRate)
		tier.TakerFeeBps, _ = domain.ParseDecimal(fees[0].TakerFeeRate)
	}

	return tier, nil
}

func (c *restClient) getOpenOrders(ctx context.Context, symbol string) ([]domain.Order, error) {
	venueSymbol := domain.MapKCEXSymbol(symbol)
	path := fmt.Sprintf("/api/v1/orders?status=active&symbol=%s", url.QueryEscape(venueSymbol))
	data, err := c.doRequest(ctx, "GET", path, nil, domain.EndpointPrivateData)
	if err != nil {
		return nil, err
	}

	var result struct {
		CurrentPage int `json:"currentPage"`
		PageSize    int `json:"pageSize"`
		TotalNum    int `json:"totalNum"`
		TotalPage   int `json:"totalPage"`
		Items       []struct {
			ID       string `json:"id"`
			Symbol   string `json:"symbol"`
			Side     string `json:"side"`
			Price    string `json:"price"`
			Size     string `json:"size"`
			DealSize string `json:"dealSize"`
			Type     string `json:"type"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse open orders: %w", err)
	}

	orders := make([]domain.Order, 0, len(result.Items))
	for _, o := range result.Items {
		side := domain.SideBuy
		if o.Side == "sell" {
			side = domain.SideSell
		}

		orderType := domain.OrderTypeLimit
		if o.Type == "market" {
			orderType = domain.OrderTypeMarket
		}

		order := domain.Order{
			VenueID:   o.ID,
			Venue:     "kcex",
			Symbol:    o.Symbol,
			Side:      side,
			OrderType: orderType,
			Status:    domain.OrderStatusAcknowledged,
		}
		order.Price, _ = domain.ParseDecimal(o.Price)
		order.Size, _ = domain.ParseDecimal(o.Size)
		order.FilledSize, _ = domain.ParseDecimal(o.DealSize)
		orders = append(orders, order)
	}

	return orders, nil
}

func (c *restClient) getOrderBook(ctx context.Context, symbol string) (*domain.OrderBookSnapshot, error) {
	venueSymbol := domain.MapKCEXSymbol(symbol)
	path := fmt.Sprintf("/api/v1/market/orderbook/level2_20?symbol=%s", venueSymbol)

	data, err := c.doPublicRequest(ctx, "GET", path, domain.EndpointPublicData)
	if err != nil {
		return nil, err
	}

	var result struct {
		Sequence string     `json:"sequence"`
		Time     int64      `json:"time"`
		Bids     [][]string `json:"bids"`
		Asks     [][]string `json:"asks"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse orderbook: %w", err)
	}

	book := &domain.OrderBookSnapshot{
		Venue:          "kcex",
		Symbol:         symbol,
		VenueTimestamp: time.UnixMilli(result.Time),
		LocalTimestamp:  time.Now(),
	}

	for _, bid := range result.Bids {
		if len(bid) >= 2 {
			price, _ := domain.ParseDecimal(bid[0])
			size, _ := domain.ParseDecimal(bid[1])
			book.Bids = append(book.Bids, domain.PriceLevel{Price: price, Size: size})
		}
	}
	for _, ask := range result.Asks {
		if len(ask) >= 2 {
			price, _ := domain.ParseDecimal(ask[0])
			size, _ := domain.ParseDecimal(ask[1])
			book.Asks = append(book.Asks, domain.PriceLevel{Price: price, Size: size})
		}
	}

	return book, nil
}

// getWSToken requests a WebSocket connection token from the KCEX API.
func (c *restClient) getWSToken(ctx context.Context, private bool) (*wsToken, error) {
	path := "/api/v1/bullet-public"
	if private {
		path = "/api/v1/bullet-private"
	}

	var data []byte
	var err error

	if private {
		data, err = c.doRequest(ctx, "POST", path, nil, domain.EndpointPrivateData)
	} else {
		data, err = c.doPublicRequest(ctx, "POST", path, domain.EndpointPublicData)
	}
	if err != nil {
		return nil, fmt.Errorf("get ws token: %w", err)
	}

	var result struct {
		Token           string `json:"token"`
		InstanceServers []struct {
			Endpoint     string `json:"endpoint"`
			Protocol     string `json:"protocol"`
			Encrypt      bool   `json:"encrypt"`
			PingInterval int    `json:"pingInterval"`
			PingTimeout  int    `json:"pingTimeout"`
		} `json:"instanceServers"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse ws token: %w", err)
	}

	if len(result.InstanceServers) == 0 {
		return nil, fmt.Errorf("no websocket instance servers returned")
	}

	return &wsToken{
		token:        result.Token,
		endpoint:     result.InstanceServers[0].Endpoint,
		pingInterval: time.Duration(result.InstanceServers[0].PingInterval) * time.Millisecond,
		pingTimeout:  time.Duration(result.InstanceServers[0].PingTimeout) * time.Millisecond,
	}, nil
}

func (c *restClient) getFundingRate(ctx context.Context, symbol string) (*domain.FundingRate, error) {
	venueSymbol := domain.MapKCEXSymbol(symbol)
	path := fmt.Sprintf("/api/v1/funding-rate/%s/current", venueSymbol)

	data, err := c.doRequest(ctx, "GET", path, nil, domain.EndpointPublicData)
	if err != nil {
		return nil, err
	}

	var result struct {
		Symbol      string  `json:"symbol"`
		Granularity int     `json:"granularity"`
		TimePoint   int64   `json:"timePoint"`
		Value       float64 `json:"value"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse funding rate: %w", err)
	}

	rate := &domain.FundingRate{
		Venue:     "kcex",
		Symbol:    symbol,
		Timestamp: time.UnixMilli(result.TimePoint),
	}
	rate.Rate, _ = domain.ParseDecimal(fmt.Sprintf("%f", result.Value))

	return rate, nil
}
