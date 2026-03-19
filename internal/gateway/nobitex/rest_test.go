package nobitex

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

func newTestRESTClient(handler http.Handler) (*restClient, *httptest.Server) {
	server := httptest.NewServer(handler)
	rl := gateway.NewRateLimiter()
	rl.AddBucket(domain.EndpointPublicData, 100, 100)
	rl.AddBucket(domain.EndpointPrivateData, 100, 100)
	rl.AddBucket(domain.EndpointOrderPlace, 100, 100)
	rl.AddBucket(domain.EndpointOrderCancel, 100, 100)
	rl.AddBucket(domain.EndpointAccount, 100, 100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	client := newRESTClient(server.URL, "test-token-abc123", rl, logger)
	return client, server
}

func TestRestClient_PlaceOrder_CorrectEndpointAndAuth(t *testing.T) {
	var capturedReq *http.Request
	var capturedBody map[string]interface{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		json.NewDecoder(r.Body).Decode(&capturedBody)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"order": map[string]interface{}{
				"id":            42,
				"type":          "buy",
				"srcCurrency":   "btc",
				"dstCurrency":   "usdt",
				"price":         "50000",
				"amount":        "0.1",
				"matchedAmount": "0",
				"status":        "Active",
			},
		})
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
		IdempotencyKey: "test-key-1",
	}

	ack, err := client.placeOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedReq.URL.Path != "/market/orders/add" {
		t.Errorf("expected path /market/orders/add, got %s", capturedReq.URL.Path)
	}
	if capturedReq.Method != "POST" {
		t.Errorf("expected POST, got %s", capturedReq.Method)
	}
	if capturedReq.Header.Get("Authorization") != "Token test-token-abc123" {
		t.Errorf("expected Token auth header, got %q", capturedReq.Header.Get("Authorization"))
	}
	if capturedBody["srcCurrency"] != "btc" {
		t.Errorf("expected srcCurrency=btc, got %v", capturedBody["srcCurrency"])
	}
	if capturedBody["dstCurrency"] != "usdt" {
		t.Errorf("expected dstCurrency=usdt, got %v", capturedBody["dstCurrency"])
	}
	if capturedBody["type"] != "buy" {
		t.Errorf("expected type=buy, got %v", capturedBody["type"])
	}
	if ack.VenueID != "42" {
		t.Errorf("expected venue ID 42, got %s", ack.VenueID)
	}
	if ack.Status != domain.OrderStatusAcknowledged {
		t.Errorf("expected ACKNOWLEDGED, got %s", ack.Status)
	}
}

func TestRestClient_PlaceOrder_MarketOrder(t *testing.T) {
	var capturedBody map[string]interface{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"order":  map[string]interface{}{"id": 99, "status": "Active"},
		})
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	req := domain.OrderRequest{
		InternalID: uuid.Must(uuid.NewV7()),
		Symbol:     "ETH/USDT",
		Side:       domain.SideSell,
		OrderType:  domain.OrderTypeMarket,
		Size:       decimal.NewFromFloat(1.5),
	}

	_, err := client.placeOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedBody["execution"] != "market" {
		t.Errorf("expected execution=market for market orders, got %v", capturedBody["execution"])
	}
	if _, hasPrice := capturedBody["price"]; hasPrice {
		t.Error("market orders should not include price")
	}
	if capturedBody["type"] != "sell" {
		t.Errorf("expected type=sell, got %v", capturedBody["type"])
	}
	if capturedBody["srcCurrency"] != "eth" {
		t.Errorf("expected srcCurrency=eth, got %v", capturedBody["srcCurrency"])
	}
}

func TestRestClient_CancelOrder_CorrectEndpoint(t *testing.T) {
	var capturedPath string
	var capturedBody map[string]interface{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&capturedBody)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":        "ok",
			"updatedStatus": "Canceled",
		})
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	ack, err := client.cancelOrder(context.Background(), "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedPath != "/market/orders/update-status" {
		t.Errorf("expected path /market/orders/update-status, got %s", capturedPath)
	}
	orderID, ok := capturedBody["order"].(float64)
	if !ok || int(orderID) != 42 {
		t.Errorf("expected order=42 in body, got %v", capturedBody["order"])
	}
	if capturedBody["status"] != "cancel" {
		t.Errorf("expected status=cancel, got %v", capturedBody["status"])
	}
	if ack.Status != domain.OrderStatusCancelled {
		t.Errorf("expected CANCELLED, got %s", ack.Status)
	}
}

func TestRestClient_GetBalances_ParsesWallets(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/wallets/list" {
			t.Errorf("expected path /users/wallets/list, got %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"wallets": []map[string]interface{}{
				{
					"id":             1234,
					"activeBalance":  "5000.50",
					"balance":        "5100.00",
					"blockedBalance": "99.50",
					"currency":       "usdt",
				},
				{
					"id":             1235,
					"activeBalance":  "0.5",
					"balance":        "0.6",
					"blockedBalance": "0.1",
					"currency":       "btc",
				},
			},
		})
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	balances, err := client.getBalances(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(balances) != 2 {
		t.Fatalf("expected 2 balances, got %d", len(balances))
	}

	usdt, ok := balances["USDT"]
	if !ok {
		t.Fatal("expected USDT balance")
	}
	if !usdt.Free.Equal(decimal.NewFromFloat(5000.50)) {
		t.Errorf("expected USDT free 5000.50, got %s", usdt.Free)
	}
	if !usdt.Locked.Equal(decimal.NewFromFloat(99.50)) {
		t.Errorf("expected USDT locked 99.50, got %s", usdt.Locked)
	}
	if usdt.Venue != "nobitex" {
		t.Errorf("expected venue nobitex, got %s", usdt.Venue)
	}

	btc, ok := balances["BTC"]
	if !ok {
		t.Fatal("expected BTC balance")
	}
	if !btc.Free.Equal(decimal.NewFromFloat(0.5)) {
		t.Errorf("expected BTC free 0.5, got %s", btc.Free)
	}
}

func TestRestClient_GetPositions_ReturnsEmpty(t *testing.T) {
	positions, err := (&restClient{}).getPositions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(positions) != 0 {
		t.Errorf("nobitex is spot-only, expected empty positions, got %d", len(positions))
	}
}

func TestRestClient_GetOpenOrders(t *testing.T) {
	var capturedBody map[string]interface{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"orders": []map[string]interface{}{
				{
					"id":              100,
					"type":            "buy",
					"srcCurrency":     "btc",
					"dstCurrency":     "usdt",
					"price":           "49000",
					"amount":          "0.5",
					"matchedAmount":   "0.1",
					"unmatchedAmount": "0.4",
					"status":          "Active",
				},
			},
		})
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	orders, err := client.getOpenOrders(context.Background(), "BTC/USDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedBody["status"] != "open" {
		t.Errorf("expected status=open filter, got %v", capturedBody["status"])
	}
	if capturedBody["srcCurrency"] != "btc" {
		t.Errorf("expected srcCurrency=btc, got %v", capturedBody["srcCurrency"])
	}

	if len(orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(orders))
	}
	if orders[0].VenueID != "100" {
		t.Errorf("expected venueID=100, got %s", orders[0].VenueID)
	}
	if orders[0].Side != domain.SideBuy {
		t.Errorf("expected BUY, got %s", orders[0].Side)
	}
	if !orders[0].FilledSize.Equal(decimal.NewFromFloat(0.1)) {
		t.Errorf("expected filled 0.1, got %s", orders[0].FilledSize)
	}
}

func TestRestClient_GetOrderBook(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/orderbook/BTCUSDT" {
			t.Errorf("expected path /v3/orderbook/BTCUSDT, got %s", r.URL.Path)
		}
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("orderbook is a public endpoint, should not have auth header")
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "ok",
			"bids":           [][]string{{"49900", "0.5"}, {"49800", "1.2"}},
			"asks":           [][]string{{"50000", "0.3"}, {"50100", "0.8"}},
			"lastTradePrice": "49950",
		})
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	book, err := client.getOrderBook(context.Background(), "BTC/USDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if book.Venue != "nobitex" {
		t.Errorf("expected venue nobitex, got %s", book.Venue)
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
	if !book.Asks[0].Price.Equal(decimal.NewFromInt(50000)) {
		t.Errorf("expected best ask 50000, got %s", book.Asks[0].Price)
	}
}

func TestRestClient_GetRecentTrades(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/trades/ETHUSDT" {
			t.Errorf("expected path /v3/trades/ETHUSDT, got %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"trades": []map[string]interface{}{
				{"time": 1700000000000, "price": "3500", "volume": "2.5", "type": "buy"},
				{"time": 1700000001000, "price": "3501", "volume": "1.0", "type": "sell"},
			},
		})
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	trades, err := client.getRecentTrades(context.Background(), "ETH/USDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(trades) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(trades))
	}
	if !trades[0].Price.Equal(decimal.NewFromInt(3500)) {
		t.Errorf("expected price 3500, got %s", trades[0].Price)
	}
	if trades[0].Side != domain.SideBuy {
		t.Errorf("expected BUY, got %s", trades[0].Side)
	}
	if trades[1].Side != domain.SideSell {
		t.Errorf("expected SELL, got %s", trades[1].Side)
	}
}

func TestRestClient_APIError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "failed",
			"code":    "InvalidMarketPair",
			"message": "Market pair is not valid",
		})
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	_, err := client.placeOrder(context.Background(), domain.OrderRequest{
		InternalID: uuid.Must(uuid.NewV7()),
		Symbol:     "INVALID/PAIR",
		Side:       domain.SideBuy,
		OrderType:  domain.OrderTypeLimit,
		Price:      decimal.NewFromInt(100),
		Size:       decimal.NewFromFloat(1),
	})

	if err == nil {
		t.Fatal("expected error for invalid market pair")
	}
}

func TestRestClient_GetFeeTier(t *testing.T) {
	client := &restClient{}
	tier, err := client.getFeeTier(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tier.Venue != "nobitex" {
		t.Errorf("expected venue nobitex, got %s", tier.Venue)
	}
	if !tier.MakerFeeBps.Equal(decimal.NewFromInt(10)) {
		t.Errorf("expected maker fee 10 bps, got %s", tier.MakerFeeBps)
	}
	if !tier.TakerFeeBps.Equal(decimal.NewFromInt(15)) {
		t.Errorf("expected taker fee 15 bps, got %s", tier.TakerFeeBps)
	}
}
