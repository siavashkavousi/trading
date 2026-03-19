package kcex

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/gateway"
)

func kcexOK(data interface{}) map[string]interface{} {
	return map[string]interface{}{
		"code": "200000",
		"data": data,
	}
}

func newTestRESTClient(handler http.Handler) (*restClient, *httptest.Server) {
	server := httptest.NewServer(handler)
	rl := gateway.NewRateLimiter()
	rl.AddBucket(domain.EndpointPublicData, 100, 100)
	rl.AddBucket(domain.EndpointPrivateData, 100, 100)
	rl.AddBucket(domain.EndpointOrderPlace, 100, 100)
	rl.AddBucket(domain.EndpointOrderCancel, 100, 100)
	rl.AddBucket(domain.EndpointAccount, 100, 100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	client := newRESTClient(server.URL, "test-api-key", "test-api-secret", "test-passphrase", rl, logger)
	return client, server
}

func TestKCEXRestClient_PlaceOrder_SpotLimitOrder(t *testing.T) {
	var capturedReq *http.Request
	var capturedBody map[string]interface{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		json.NewDecoder(r.Body).Decode(&capturedBody)
		json.NewEncoder(w).Encode(kcexOK(map[string]interface{}{
			"orderId": "order-123-abc",
		}))
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	req := domain.OrderRequest{
		InternalID:     uuid.Must(uuid.NewV7()),
		Symbol:         "BTC/USDT",
		Side:           domain.SideBuy,
		OrderType:      domain.OrderTypeLimit,
		Price:          decimal.NewFromInt(50000),
		Size:           decimal.NewFromFloat(0.1),
		IdempotencyKey: "idem-123",
	}

	ack, err := client.placeOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedReq.URL.Path != "/api/v1/orders" {
		t.Errorf("expected path /api/v1/orders, got %s", capturedReq.URL.Path)
	}
	if capturedReq.Method != "POST" {
		t.Errorf("expected POST, got %s", capturedReq.Method)
	}

	// Verify KCEX auth headers
	if capturedReq.Header.Get("KC-API-KEY") != "test-api-key" {
		t.Errorf("expected KC-API-KEY header, got %q", capturedReq.Header.Get("KC-API-KEY"))
	}
	if capturedReq.Header.Get("KC-API-SIGN") == "" {
		t.Error("expected KC-API-SIGN header to be set")
	}
	if capturedReq.Header.Get("KC-API-TIMESTAMP") == "" {
		t.Error("expected KC-API-TIMESTAMP header to be set")
	}
	if capturedReq.Header.Get("KC-API-PASSPHRASE") == "" {
		t.Error("expected KC-API-PASSPHRASE header to be set")
	}
	if capturedReq.Header.Get("KC-API-KEY-VERSION") != "2" {
		t.Errorf("expected KC-API-KEY-VERSION=2, got %q", capturedReq.Header.Get("KC-API-KEY-VERSION"))
	}

	if capturedBody["symbol"] != "BTC-USDT" {
		t.Errorf("expected spot symbol BTC-USDT, got %v", capturedBody["symbol"])
	}
	if capturedBody["side"] != "buy" {
		t.Errorf("expected side=buy, got %v", capturedBody["side"])
	}
	if capturedBody["type"] != "limit" {
		t.Errorf("expected type=limit, got %v", capturedBody["type"])
	}
	if capturedBody["clientOid"] != "idem-123" {
		t.Errorf("expected clientOid=idem-123, got %v", capturedBody["clientOid"])
	}

	if ack.VenueID != "order-123-abc" {
		t.Errorf("expected orderId=order-123-abc, got %s", ack.VenueID)
	}
	if ack.Status != domain.OrderStatusAcknowledged {
		t.Errorf("expected ACKNOWLEDGED, got %s", ack.Status)
	}
}

func TestKCEXRestClient_PlaceOrder_FuturesMarketOrder(t *testing.T) {
	var capturedBody map[string]interface{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		json.NewEncoder(w).Encode(kcexOK(map[string]interface{}{
			"orderId": "fut-order-456",
		}))
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	req := domain.OrderRequest{
		InternalID: uuid.Must(uuid.NewV7()),
		Symbol:     "BTCUSDT",
		Side:       domain.SideSell,
		OrderType:  domain.OrderTypeMarket,
		Size:       decimal.NewFromFloat(0.5),
	}

	_, err := client.placeOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedBody["symbol"] != "BTCUSDTM" {
		t.Errorf("expected futures symbol BTCUSDTM, got %v", capturedBody["symbol"])
	}
	if capturedBody["type"] != "market" {
		t.Errorf("expected type=market, got %v", capturedBody["type"])
	}
	if capturedBody["side"] != "sell" {
		t.Errorf("expected side=sell, got %v", capturedBody["side"])
	}
	if capturedBody["leverage"] != "1" {
		t.Errorf("expected leverage=1 for futures, got %v", capturedBody["leverage"])
	}
}

func TestKCEXRestClient_CancelOrder(t *testing.T) {
	var capturedPath string
	var capturedMethod string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedMethod = r.Method
		json.NewEncoder(w).Encode(kcexOK(map[string]interface{}{
			"cancelledOrderIds": []string{"order-789"},
		}))
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	ack, err := client.cancelOrder(context.Background(), "order-789")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedPath != "/api/v1/orders/order-789" {
		t.Errorf("expected path /api/v1/orders/order-789, got %s", capturedPath)
	}
	if capturedMethod != "DELETE" {
		t.Errorf("expected DELETE, got %s", capturedMethod)
	}
	if ack.Status != domain.OrderStatusCancelled {
		t.Errorf("expected CANCELLED, got %s", ack.Status)
	}
}

func TestKCEXRestClient_GetBalances(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/accounts" {
			t.Errorf("expected path /api/v1/accounts, got %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(kcexOK([]map[string]interface{}{
			{"id": "acc1", "currency": "BTC", "type": "trade", "balance": "1.5", "available": "1.2", "holds": "0.3"},
			{"id": "acc2", "currency": "USDT", "type": "trade", "balance": "50000", "available": "45000", "holds": "5000"},
			{"id": "acc3", "currency": "BTC", "type": "main", "balance": "0.5", "available": "0.5", "holds": "0"},
		}))
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	balances, err := client.getBalances(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should only include "trade" type accounts
	if len(balances) != 2 {
		t.Fatalf("expected 2 trade balances, got %d", len(balances))
	}

	btc, ok := balances["BTC"]
	if !ok {
		t.Fatal("expected BTC balance")
	}
	if !btc.Free.Equal(decimal.NewFromFloat(1.2)) {
		t.Errorf("expected BTC available 1.2, got %s", btc.Free)
	}
	if !btc.Locked.Equal(decimal.NewFromFloat(0.3)) {
		t.Errorf("expected BTC holds 0.3, got %s", btc.Locked)
	}
	if btc.Venue != "kcex" {
		t.Errorf("expected venue kcex, got %s", btc.Venue)
	}
}

func TestKCEXRestClient_GetPositions(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/positions" {
			t.Errorf("expected path /api/v1/positions, got %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(kcexOK([]map[string]interface{}{
			{
				"symbol":        "BTCUSDTM",
				"currentQty":    100,
				"avgEntryPrice": "50000",
				"unrealisedPnl": "250.5",
				"maintMargin":   "1000",
				"isOpen":        true,
			},
			{
				"symbol":        "ETHUSDTM",
				"currentQty":    0,
				"avgEntryPrice": "3000",
				"unrealisedPnl": "0",
				"maintMargin":   "0",
				"isOpen":        false,
			},
		}))
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	positions, err := client.getPositions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should only include open positions
	if len(positions) != 1 {
		t.Fatalf("expected 1 open position, got %d", len(positions))
	}

	if positions[0].Asset != "BTCUSDTM" {
		t.Errorf("expected BTCUSDTM, got %s", positions[0].Asset)
	}
	if !positions[0].UnrealizedPnL.Equal(decimal.NewFromFloat(250.5)) {
		t.Errorf("expected unrealised pnl 250.5, got %s", positions[0].UnrealizedPnL)
	}
}

func TestKCEXRestClient_GetOpenOrders(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("status") != "active" {
			t.Errorf("expected status=active, got %s", r.URL.Query().Get("status"))
		}
		if r.URL.Query().Get("symbol") != "BTC-USDT" {
			t.Errorf("expected symbol=BTC-USDT, got %s", r.URL.Query().Get("symbol"))
		}
		json.NewEncoder(w).Encode(kcexOK(map[string]interface{}{
			"currentPage": 1,
			"pageSize":    50,
			"totalNum":    1,
			"totalPage":   1,
			"items": []map[string]interface{}{
				{
					"id":       "order-001",
					"symbol":   "BTC-USDT",
					"side":     "buy",
					"price":    "49000",
					"size":     "0.5",
					"dealSize": "0.1",
					"type":     "limit",
				},
			},
		}))
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	orders, err := client.getOpenOrders(context.Background(), "BTC/USDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(orders))
	}
	if orders[0].VenueID != "order-001" {
		t.Errorf("expected order-001, got %s", orders[0].VenueID)
	}
	if orders[0].Side != domain.SideBuy {
		t.Errorf("expected BUY, got %s", orders[0].Side)
	}
	if !orders[0].FilledSize.Equal(decimal.NewFromFloat(0.1)) {
		t.Errorf("expected filled 0.1, got %s", orders[0].FilledSize)
	}
}

func TestKCEXRestClient_GetFeeTier(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(kcexOK([]map[string]interface{}{
			{
				"symbol":       "BTC-USDT",
				"takerFeeRate": "0.001",
				"makerFeeRate": "0.0008",
			},
		}))
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	tier, err := client.getFeeTier(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tier.Venue != "kcex" {
		t.Errorf("expected venue kcex, got %s", tier.Venue)
	}
	if !tier.MakerFeeBps.Equal(decimal.NewFromFloat(0.0008)) {
		t.Errorf("expected maker fee 0.0008, got %s", tier.MakerFeeBps)
	}
	if !tier.TakerFeeBps.Equal(decimal.NewFromFloat(0.001)) {
		t.Errorf("expected taker fee 0.001, got %s", tier.TakerFeeBps)
	}
}

func TestKCEXRestClient_APIError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"code": "400100",
			"msg":  "Order creation for this pair suspended",
		})
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	_, err := client.placeOrder(context.Background(), domain.OrderRequest{
		InternalID: uuid.Must(uuid.NewV7()),
		Symbol:     "BTC/USDT",
		Side:       domain.SideBuy,
		OrderType:  domain.OrderTypeLimit,
		Price:      decimal.NewFromInt(50000),
		Size:       decimal.NewFromFloat(0.1),
	})

	if err == nil {
		t.Fatal("expected error for API error response")
	}
}

func TestKCEXRestClient_SignatureFormat(t *testing.T) {
	rl := gateway.NewRateLimiter()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	client := newRESTClient("https://api.kcex.com", "my-key", "my-secret", "my-pass", rl, logger)

	sig := client.sign("1234567890POST/api/v1/orders{}")
	if sig == "" {
		t.Error("expected non-empty signature")
	}

	passSig := client.signPassphrase()
	if passSig == "" {
		t.Error("expected non-empty passphrase signature")
	}
}

func TestKCEXRestClient_GetOrderBook(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/market/orderbook/level2_20" {
			t.Errorf("expected orderbook path, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("symbol") != "BTC-USDT" {
			t.Errorf("expected symbol=BTC-USDT, got %s", r.URL.Query().Get("symbol"))
		}
		json.NewEncoder(w).Encode(kcexOK(map[string]interface{}{
			"sequence": "102931",
			"time":     1700000000000,
			"bids":     [][]string{{"49900", "0.5"}, {"49800", "1.2"}},
			"asks":     [][]string{{"50000", "0.3"}, {"50100", "0.8"}},
		}))
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	book, err := client.getOrderBook(context.Background(), "BTC/USDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if book.Venue != "kcex" {
		t.Errorf("expected venue kcex, got %s", book.Venue)
	}
	if len(book.Bids) != 2 {
		t.Fatalf("expected 2 bids, got %d", len(book.Bids))
	}
	if len(book.Asks) != 2 {
		t.Fatalf("expected 2 asks, got %d", len(book.Asks))
	}
	if !book.Bids[0].Price.Equal(decimal.NewFromInt(49900)) {
		t.Errorf("expected best bid 49900, got %s", book.Bids[0].Price)
	}
}
