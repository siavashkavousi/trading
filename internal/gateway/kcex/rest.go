package kcex

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/gateway"
)

type restClient struct {
	baseURL     string
	apiKey      string
	apiSecret   string
	httpClient  *http.Client
	rateLimiter *gateway.RateLimiter
	logger      *slog.Logger
}

func newRESTClient(baseURL, apiKey, apiSecret string, rl *gateway.RateLimiter, logger *slog.Logger) *restClient {
	return &restClient{
		baseURL:   baseURL,
		apiKey:    apiKey,
		apiSecret: apiSecret,
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

func (c *restClient) sign(payload string) string {
	mac := hmac.New(sha256.New, []byte(c.apiSecret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
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
		req.Header.Set("KC-API-TIMESTAMP", timestamp)
		req.Header.Set("KC-API-SIGN", signature)
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

	return respBody, nil
}

func (c *restClient) placeOrder(ctx context.Context, req domain.OrderRequest) (*domain.OrderAck, error) {
	body := map[string]interface{}{
		"symbol":    req.Symbol,
		"side":      string(req.Side),
		"type":      string(req.OrderType),
		"price":     req.Price.String(),
		"size":      req.Size.String(),
		"clientOid": req.IdempotencyKey,
	}

	respData, err := c.doRequest(ctx, "POST", "/api/v1/orders", body, domain.EndpointOrderPlace)
	if err != nil {
		return nil, err
	}

	var result struct {
		Data struct {
			OrderID string `json:"orderId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respData, &result); err != nil {
		return nil, fmt.Errorf("parse order response: %w", err)
	}

	return &domain.OrderAck{
		InternalID: req.InternalID,
		VenueID:    result.Data.OrderID,
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
	respData, err := c.doRequest(ctx, "GET", "/api/v1/accounts", nil, domain.EndpointAccount)
	if err != nil {
		return nil, err
	}

	var result struct {
		Data []struct {
			Currency  string `json:"currency"`
			Available string `json:"available"`
			Holds     string `json:"holds"`
			Balance   string `json:"balance"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respData, &result); err != nil {
		return nil, fmt.Errorf("parse balances: %w", err)
	}

	balances := make(map[string]domain.Balance, len(result.Data))
	for _, b := range result.Data {
		bal := domain.Balance{
			Venue: "kcex",
			Asset: b.Currency,
		}
		bal.Free, _ = domain.ParseDecimal(b.Available)
		bal.Locked, _ = domain.ParseDecimal(b.Holds)
		bal.Total, _ = domain.ParseDecimal(b.Balance)
		balances[b.Currency] = bal
	}

	return balances, nil
}

func (c *restClient) getPositions(ctx context.Context) ([]domain.Position, error) {
	respData, err := c.doRequest(ctx, "GET", "/api/v1/positions", nil, domain.EndpointAccount)
	if err != nil {
		return nil, err
	}

	var result struct {
		Data []struct {
			Symbol     string `json:"symbol"`
			Size       string `json:"currentQty"`
			EntryPrice string `json:"avgEntryPrice"`
			PnL        string `json:"unrealisedPnl"`
			Margin     string `json:"maintMargin"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respData, &result); err != nil {
		return nil, fmt.Errorf("parse positions: %w", err)
	}

	positions := make([]domain.Position, 0, len(result.Data))
	for _, p := range result.Data {
		pos := domain.Position{
			Venue:          "kcex",
			Asset:          p.Symbol,
			InstrumentType: domain.InstrumentPerp,
			UpdatedAt:      time.Now(),
		}
		pos.Size, _ = domain.ParseDecimal(p.Size)
		pos.EntryPrice, _ = domain.ParseDecimal(p.EntryPrice)
		pos.UnrealizedPnL, _ = domain.ParseDecimal(p.PnL)
		pos.MarginUsed, _ = domain.ParseDecimal(p.Margin)
		positions = append(positions, pos)
	}

	return positions, nil
}

func (c *restClient) getFeeTier(ctx context.Context) (*domain.FeeTier, error) {
	respData, err := c.doRequest(ctx, "GET", "/api/v1/trade-fees", nil, domain.EndpointAccount)
	if err != nil {
		return nil, err
	}

	var result struct {
		Data struct {
			MakerFeeRate string `json:"makerFeeRate"`
			TakerFeeRate string `json:"takerFeeRate"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respData, &result); err != nil {
		return nil, fmt.Errorf("parse fee tier: %w", err)
	}

	tier := &domain.FeeTier{
		Venue:     "kcex",
		UpdatedAt: time.Now(),
	}
	tier.MakerFeeBps, _ = domain.ParseDecimal(result.Data.MakerFeeRate)
	tier.TakerFeeBps, _ = domain.ParseDecimal(result.Data.TakerFeeRate)

	return tier, nil
}

func (c *restClient) getOpenOrders(ctx context.Context, symbol string) ([]domain.Order, error) {
	path := fmt.Sprintf("/api/v1/orders?status=active&symbol=%s", symbol)
	respData, err := c.doRequest(ctx, "GET", path, nil, domain.EndpointPrivateData)
	if err != nil {
		return nil, err
	}

	var result struct {
		Data struct {
			Items []struct {
				ID     string `json:"id"`
				Symbol string `json:"symbol"`
				Side   string `json:"side"`
				Price  string `json:"price"`
				Size   string `json:"size"`
				Filled string `json:"dealSize"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respData, &result); err != nil {
		return nil, fmt.Errorf("parse open orders: %w", err)
	}

	orders := make([]domain.Order, 0, len(result.Data.Items))
	for _, o := range result.Data.Items {
		order := domain.Order{
			VenueID: o.ID,
			Venue:   "kcex",
			Symbol:  o.Symbol,
			Side:    domain.Side(o.Side),
			Status:  domain.OrderStatusAcknowledged,
		}
		order.Price, _ = domain.ParseDecimal(o.Price)
		order.Size, _ = domain.ParseDecimal(o.Size)
		order.FilledSize, _ = domain.ParseDecimal(o.Filled)
		orders = append(orders, order)
	}

	return orders, nil
}
