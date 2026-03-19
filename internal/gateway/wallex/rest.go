package wallex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/gateway"
)

type restClient struct {
	baseURL     string
	apiKey      string
	httpClient  *http.Client
	rateLimiter *gateway.RateLimiter
	logger      *slog.Logger
}

func newRESTClient(baseURL, apiKey string, rl *gateway.RateLimiter, logger *slog.Logger) *restClient {
	return &restClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
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

// wallexResponse is the common wrapper for all Wallex API responses.
// All Wallex API responses follow the format: {"result": ..., "message": "...", "success": true/false}
type wallexResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Result  json.RawMessage `json:"result"`
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

	if authenticated && c.apiKey != "" {
		req.Header.Set("x-api-key", c.apiKey)
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

	var baseResp wallexResponse
	if err := json.Unmarshal(respBody, &baseResp); err == nil {
		if !baseResp.Success {
			return nil, fmt.Errorf("wallex API error: %s", baseResp.Message)
		}
	}

	return respBody, nil
}

// placeOrder places a new order on Wallex.
// POST https://api.wallex.ir/v1/account/orders
// Body: {"symbol": "BTCUSDT", "side": "buy"|"sell", "type": "limit"|"market", "price": "...", "quantity": "...", "client_id": "..."}
func (c *restClient) placeOrder(ctx context.Context, req domain.OrderRequest) (*domain.OrderAck, error) {
	wallexSymbol := domain.MapSymbol(req.Symbol, domain.WallexSymbolMap)

	side := "buy"
	if req.Side == domain.SideSell {
		side = "sell"
	}

	orderType := "limit"
	if req.OrderType == domain.OrderTypeMarket {
		orderType = "market"
	}

	body := map[string]interface{}{
		"symbol":   wallexSymbol,
		"side":     side,
		"type":     orderType,
		"quantity": req.Size.String(),
	}

	if req.OrderType == domain.OrderTypeLimit {
		body["price"] = req.Price.String()
	}

	if req.IdempotencyKey != "" {
		body["client_id"] = req.IdempotencyKey
	}

	respData, err := c.doRequest(ctx, "POST", "/v1/account/orders", body, domain.EndpointOrderPlace, true)
	if err != nil {
		return nil, err
	}

	var result struct {
		Result struct {
			Symbol        string `json:"symbol"`
			Type          string `json:"type"`
			Side          string `json:"side"`
			ClientOrderID string `json:"clientOrderId"`
			TransactTime  int64  `json:"transactTime"`
			Price         string `json:"price"`
			OrigQty       string `json:"origQty"`
			ExecutedQty   string `json:"executedQty"`
			Status        string `json:"status"`
			Active        bool   `json:"active"`
		} `json:"result"`
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(respData, &result); err != nil {
		return nil, fmt.Errorf("parse order response: %w", err)
	}

	return &domain.OrderAck{
		InternalID: req.InternalID,
		VenueID:    result.Result.ClientOrderID,
		Status:     domain.OrderStatusAcknowledged,
		Timestamp:  time.Now(),
	}, nil
}

// cancelOrder cancels an order on Wallex.
// DELETE https://api.wallex.ir/v1/account/orders
// Body: {"clientOrderId": "..."}
func (c *restClient) cancelOrder(ctx context.Context, orderID string) (*domain.CancelAck, error) {
	body := map[string]interface{}{
		"clientOrderId": orderID,
	}

	_, err := c.doRequest(ctx, "DELETE", "/v1/account/orders", body, domain.EndpointOrderCancel, true)
	if err != nil {
		return nil, err
	}

	return &domain.CancelAck{
		Status:    domain.OrderStatusCancelled,
		Timestamp: time.Now(),
	}, nil
}

// getBalances fetches all account balances from Wallex.
// GET https://api.wallex.ir/v1/account/balances
// Returns: {"result": {"balances": {"BTC": {"asset": "BTC", "value": "...", "locked": "..."}, ...}}}
func (c *restClient) getBalances(ctx context.Context) (map[string]domain.Balance, error) {
	respData, err := c.doRequest(ctx, "GET", "/v1/account/balances", nil, domain.EndpointAccount, true)
	if err != nil {
		return nil, err
	}

	var result struct {
		Result struct {
			Balances map[string]struct {
				Asset  string `json:"asset"`
				FaName string `json:"faName"`
				Fiat   bool   `json:"fiat"`
				Value  string `json:"value"`
				Locked string `json:"locked"`
			} `json:"balances"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respData, &result); err != nil {
		return nil, fmt.Errorf("parse balances: %w", err)
	}

	balances := make(map[string]domain.Balance, len(result.Result.Balances))
	for _, b := range result.Result.Balances {
		asset := strings.ToUpper(b.Asset)
		total, _ := decimal.NewFromString(b.Value)
		locked, _ := decimal.NewFromString(b.Locked)
		free := total.Sub(locked)

		balances[asset] = domain.Balance{
			Venue:  "wallex",
			Asset:  asset,
			Free:   free,
			Locked: locked,
			Total:  total,
		}
	}

	return balances, nil
}

// getPositions always returns empty since Wallex is spot-only.
func (c *restClient) getPositions(_ context.Context) ([]domain.Position, error) {
	return []domain.Position{}, nil
}

// getFeeTier fetches maker/taker fee rates from Wallex.
// GET https://api.wallex.ir/v1/account/fee
// Returns per-market fee rates; we pick the first USDT market as representative.
func (c *restClient) getFeeTier(ctx context.Context) (*domain.FeeTier, error) {
	respData, err := c.doRequest(ctx, "GET", "/v1/account/fee", nil, domain.EndpointAccount, true)
	if err != nil {
		// Fall back to Wallex default fee rates: maker 0.2%, taker 0.2% for USDT markets
		tier := &domain.FeeTier{
			Venue:     "wallex",
			UpdatedAt: time.Now(),
		}
		tier.MakerFeeBps, _ = domain.ParseDecimal("20")
		tier.TakerFeeBps, _ = domain.ParseDecimal("20")
		return tier, nil
	}

	var result struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(respData, &result); err != nil {
		return nil, fmt.Errorf("parse fee response: %w", err)
	}

	tier := &domain.FeeTier{
		Venue:     "wallex",
		UpdatedAt: time.Now(),
	}

	// Try to find a USDT market fee rate as the representative tier
	for symbol, raw := range result.Result {
		if symbol == "default" || symbol == "metaData" {
			continue
		}
		if !strings.Contains(symbol, "USDT") {
			continue
		}
		var feeInfo struct {
			MakerFeeRate string `json:"makerFeeRate"`
			TakerFeeRate string `json:"takerFeeRate"`
		}
		if err := json.Unmarshal(raw, &feeInfo); err != nil {
			continue
		}
		makerRate, _ := decimal.NewFromString(feeInfo.MakerFeeRate)
		takerRate, _ := decimal.NewFromString(feeInfo.TakerFeeRate)
		bpsFactor := decimal.NewFromInt(10000)
		tier.MakerFeeBps = makerRate.Mul(bpsFactor)
		tier.TakerFeeBps = takerRate.Mul(bpsFactor)
		return tier, nil
	}

	// Default: Wallex USDT market rates (0.2% = 20 bps)
	tier.MakerFeeBps, _ = domain.ParseDecimal("20")
	tier.TakerFeeBps, _ = domain.ParseDecimal("20")
	return tier, nil
}

// getOpenOrders fetches active orders from Wallex.
// GET https://api.wallex.ir/v1/account/openOrders?symbol=...
func (c *restClient) getOpenOrders(ctx context.Context, symbol string) ([]domain.Order, error) {
	wallexSymbol := domain.MapSymbol(symbol, domain.WallexSymbolMap)
	path := "/v1/account/openOrders?symbol=" + wallexSymbol

	respData, err := c.doRequest(ctx, "GET", path, nil, domain.EndpointPrivateData, true)
	if err != nil {
		return nil, err
	}

	var result struct {
		Result struct {
			Orders []struct {
				Symbol        string `json:"symbol"`
				Type          string `json:"type"`
				Side          string `json:"side"`
				ClientOrderID string `json:"clientOrderId"`
				Price         string `json:"price"`
				OrigQty       string `json:"origQty"`
				ExecutedQty   string `json:"executedQty"`
				Status        string `json:"status"`
				Active        bool   `json:"active"`
			} `json:"orders"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respData, &result); err != nil {
		return nil, fmt.Errorf("parse open orders: %w", err)
	}

	orders := make([]domain.Order, 0, len(result.Result.Orders))
	for _, o := range result.Result.Orders {
		side := domain.SideBuy
		if strings.EqualFold(o.Side, "SELL") {
			side = domain.SideSell
		}

		status := domain.OrderStatusAcknowledged
		if strings.EqualFold(o.Status, "FILLED") {
			status = domain.OrderStatusFilled
		} else if strings.EqualFold(o.Status, "CANCELED") || strings.EqualFold(o.Status, "CANCELLED") {
			status = domain.OrderStatusCancelled
		}

		order := domain.Order{
			VenueID: o.ClientOrderID,
			Venue:   "wallex",
			Symbol:  domain.ReverseMapSymbol(o.Symbol, domain.WallexSymbolMap),
			Side:    side,
			Status:  status,
		}
		order.Price, _ = domain.ParseDecimal(o.Price)
		order.Size, _ = domain.ParseDecimal(o.OrigQty)
		order.FilledSize, _ = domain.ParseDecimal(o.ExecutedQty)
		orders = append(orders, order)
	}

	return orders, nil
}

// getOrderBook fetches order book from Wallex REST API.
// GET https://api.wallex.ir/v1/depth?symbol=BTCUSDT
func (c *restClient) getOrderBook(ctx context.Context, symbol string) (*domain.OrderBookSnapshot, error) {
	wallexSymbol := domain.MapSymbol(symbol, domain.WallexSymbolMap)
	path := "/v1/depth?symbol=" + wallexSymbol

	respData, err := c.doRequest(ctx, "GET", path, nil, domain.EndpointPublicData, false)
	if err != nil {
		return nil, err
	}

	var result struct {
		Result struct {
			Ask []struct {
				Price    string `json:"price"`
				Quantity string `json:"quantity"`
			} `json:"ask"`
			Bid []struct {
				Price    string `json:"price"`
				Quantity string `json:"quantity"`
			} `json:"bid"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respData, &result); err != nil {
		return nil, fmt.Errorf("parse orderbook: %w", err)
	}

	book := &domain.OrderBookSnapshot{
		Venue:          "wallex",
		Symbol:         symbol,
		LocalTimestamp:  time.Now(),
	}

	for _, ask := range result.Result.Ask {
		price, _ := domain.ParseDecimal(ask.Price)
		size, _ := domain.ParseDecimal(ask.Quantity)
		book.Asks = append(book.Asks, domain.PriceLevel{Price: price, Size: size})
	}
	for _, bid := range result.Result.Bid {
		price, _ := domain.ParseDecimal(bid.Price)
		size, _ := domain.ParseDecimal(bid.Quantity)
		book.Bids = append(book.Bids, domain.PriceLevel{Price: price, Size: size})
	}

	return book, nil
}

// getRecentTrades fetches recent trades from Wallex REST API.
// GET https://api.wallex.ir/v1/trades?symbol=BTCUSDT
func (c *restClient) getRecentTrades(ctx context.Context, symbol string) ([]domain.Trade, error) {
	wallexSymbol := domain.MapSymbol(symbol, domain.WallexSymbolMap)
	path := "/v1/trades?symbol=" + wallexSymbol

	respData, err := c.doRequest(ctx, "GET", path, nil, domain.EndpointPublicData, false)
	if err != nil {
		return nil, err
	}

	var result struct {
		Result struct {
			LatestTrades []struct {
				Symbol     string `json:"symbol"`
				Quantity   string `json:"quantity"`
				Price      string `json:"price"`
				Sum        string `json:"sum"`
				IsBuyOrder bool   `json:"isBuyOrder"`
				Timestamp  string `json:"timestamp"`
			} `json:"latestTrades"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respData, &result); err != nil {
		return nil, fmt.Errorf("parse trades: %w", err)
	}

	trades := make([]domain.Trade, 0, len(result.Result.LatestTrades))
	for _, t := range result.Result.LatestTrades {
		side := domain.SideSell
		if t.IsBuyOrder {
			side = domain.SideBuy
		}

		ts, _ := time.Parse(time.RFC3339, t.Timestamp)

		trade := domain.Trade{
			Venue:     "wallex",
			Symbol:    symbol,
			Side:      side,
			Timestamp: ts,
		}
		trade.Price, _ = domain.ParseDecimal(t.Price)
		trade.Size, _ = domain.ParseDecimal(t.Quantity)

		if trade.Timestamp.IsZero() {
			trade.Timestamp = time.Now()
		}

		trades = append(trades, trade)
	}

	return trades, nil
}
