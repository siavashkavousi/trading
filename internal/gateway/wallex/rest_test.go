package wallex

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
	client := newRESTClient(server.URL, "test-api-key-123", rl, logger)
	return client, server
}

func TestRestClient_PlaceOrder_CorrectEndpointAndAuth(t *testing.T) {
	var capturedReq *http.Request
	var capturedBody map[string]interface{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		json.NewDecoder(r.Body).Decode(&capturedBody)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": map[string]interface{}{
				"symbol":        "BTCUSDT",
				"type":          "LIMIT",
				"side":          "BUY",
				"clientOrderId": "LIMIT-abc-123",
				"transactTime":  1624846226,
				"price":         "50000.0000000000000000",
				"origQty":       "0.1000000000000000",
				"executedQty":   "0.0000000000000000",
				"status":        "NEW",
				"active":        true,
			},
			"message": "The operation was successful",
			"success": true,
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

	if capturedReq.URL.Path != "/v1/account/orders" {
		t.Errorf("expected path /v1/account/orders, got %s", capturedReq.URL.Path)
	}
	if capturedReq.Method != "POST" {
		t.Errorf("expected POST, got %s", capturedReq.Method)
	}
	if capturedReq.Header.Get("x-api-key") != "test-api-key-123" {
		t.Errorf("expected x-api-key header, got %q", capturedReq.Header.Get("x-api-key"))
	}
	if capturedBody["symbol"] != "BTCUSDT" {
		t.Errorf("expected symbol=BTCUSDT, got %v", capturedBody["symbol"])
	}
	if capturedBody["side"] != "buy" {
		t.Errorf("expected side=buy, got %v", capturedBody["side"])
	}
	if capturedBody["type"] != "limit" {
		t.Errorf("expected type=limit, got %v", capturedBody["type"])
	}
	if capturedBody["client_id"] != "test-key-1" {
		t.Errorf("expected client_id=test-key-1, got %v", capturedBody["client_id"])
	}
	if ack.VenueID != "LIMIT-abc-123" {
		t.Errorf("expected venue ID LIMIT-abc-123, got %s", ack.VenueID)
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
			"result": map[string]interface{}{
				"symbol":        "ETHUSDT",
				"type":          "MARKET",
				"side":          "SELL",
				"clientOrderId": "MARKET-xyz-456",
				"transactTime":  1624846226,
				"origQty":       "1.5000000000000000",
				"executedQty":   "0.0000000000000000",
				"status":        "NEW",
				"active":        true,
			},
			"message": "The operation was successful",
			"success": true,
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

	if capturedBody["type"] != "market" {
		t.Errorf("expected type=market, got %v", capturedBody["type"])
	}
	if capturedBody["side"] != "sell" {
		t.Errorf("expected side=sell, got %v", capturedBody["side"])
	}
	if _, hasPrice := capturedBody["price"]; hasPrice {
		t.Error("market orders should not include price")
	}
	if capturedBody["symbol"] != "ETHUSDT" {
		t.Errorf("expected symbol=ETHUSDT, got %v", capturedBody["symbol"])
	}
}

func TestRestClient_CancelOrder_CorrectEndpoint(t *testing.T) {
	var capturedReq *http.Request
	var capturedBody map[string]interface{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		json.NewDecoder(r.Body).Decode(&capturedBody)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": map[string]interface{}{
				"symbol":        "USDTTMN",
				"type":          "LIMIT",
				"side":          "SELL",
				"clientOrderId": "LIMIT-93c76637-9742-466d-b30a-89926d2cf11c",
				"status":        "FILLED",
				"active":        false,
			},
			"message": "The operation was successful",
			"success": true,
		})
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	ack, err := client.cancelOrder(context.Background(), "LIMIT-93c76637-9742-466d-b30a-89926d2cf11c")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedReq.URL.Path != "/v1/account/orders" {
		t.Errorf("expected path /v1/account/orders, got %s", capturedReq.URL.Path)
	}
	if capturedReq.Method != "DELETE" {
		t.Errorf("expected DELETE, got %s", capturedReq.Method)
	}
	if capturedBody["clientOrderId"] != "LIMIT-93c76637-9742-466d-b30a-89926d2cf11c" {
		t.Errorf("expected clientOrderId in body, got %v", capturedBody["clientOrderId"])
	}
	if ack.Status != domain.OrderStatusCancelled {
		t.Errorf("expected CANCELLED, got %s", ack.Status)
	}
}

func TestRestClient_GetBalances_ParsesWallets(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/account/balances" {
			t.Errorf("expected path /v1/account/balances, got %s", r.URL.Path)
		}
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.Header.Get("x-api-key") != "test-api-key-123" {
			t.Errorf("expected x-api-key header, got %q", r.Header.Get("x-api-key"))
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": map[string]interface{}{
				"balances": map[string]interface{}{
					"BTC": map[string]interface{}{
						"asset":  "BTC",
						"faName": "بیت کوین",
						"fiat":   false,
						"value":  "0.00000045",
						"locked": "0.00000000",
					},
					"USDT": map[string]interface{}{
						"asset":  "USDT",
						"faName": "تتر",
						"fiat":   false,
						"value":  "562.47946200",
						"locked": "100.00000000",
					},
				},
			},
			"message": "The operation was successful",
			"success": true,
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
	expectedFree, _ := decimal.NewFromString("462.47946200")
	if !usdt.Free.Equal(expectedFree) {
		t.Errorf("expected USDT free %s, got %s", expectedFree, usdt.Free)
	}
	expectedLocked, _ := decimal.NewFromString("100.00000000")
	if !usdt.Locked.Equal(expectedLocked) {
		t.Errorf("expected USDT locked %s, got %s", expectedLocked, usdt.Locked)
	}
	if usdt.Venue != "wallex" {
		t.Errorf("expected venue wallex, got %s", usdt.Venue)
	}

	btc, ok := balances["BTC"]
	if !ok {
		t.Fatal("expected BTC balance")
	}
	expectedBTC, _ := decimal.NewFromString("0.00000045")
	if !btc.Free.Equal(expectedBTC) {
		t.Errorf("expected BTC free %s, got %s", expectedBTC, btc.Free)
	}
}

func TestRestClient_GetPositions_ReturnsEmpty(t *testing.T) {
	positions, err := (&restClient{}).getPositions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(positions) != 0 {
		t.Errorf("wallex is spot-only, expected empty positions, got %d", len(positions))
	}
}

func TestRestClient_GetOpenOrders(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/account/openOrders" {
			t.Errorf("expected path /v1/account/openOrders, got %s", r.URL.Path)
		}
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Query().Get("symbol") != "BTCUSDT" {
			t.Errorf("expected symbol=BTCUSDT, got %s", r.URL.Query().Get("symbol"))
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": map[string]interface{}{
				"orders": []map[string]interface{}{
					{
						"symbol":        "BTCUSDT",
						"type":          "LIMIT",
						"side":          "BUY",
						"clientOrderId": "LIMIT-93b9b17b-c21b-4f34-a7b0-0b43cae08adc",
						"price":         "50000.0000000000000000",
						"origQty":       "0.0500000000000000",
						"executedQty":   "0.0100000000000000",
						"status":        "NEW",
						"active":        true,
					},
				},
			},
			"message": "The operation was successful",
			"success": true,
			"result_info": map[string]interface{}{
				"page":        1,
				"per_page":    1,
				"count":       1,
				"total_count": 1,
			},
		})
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
	if orders[0].VenueID != "LIMIT-93b9b17b-c21b-4f34-a7b0-0b43cae08adc" {
		t.Errorf("expected venueID, got %s", orders[0].VenueID)
	}
	if orders[0].Side != domain.SideBuy {
		t.Errorf("expected BUY, got %s", orders[0].Side)
	}
	if orders[0].Venue != "wallex" {
		t.Errorf("expected venue wallex, got %s", orders[0].Venue)
	}
	if !orders[0].FilledSize.Equal(decimal.NewFromFloat(0.01)) {
		t.Errorf("expected filled 0.01, got %s", orders[0].FilledSize)
	}
}

func TestRestClient_GetOrderBook(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/depth" {
			t.Errorf("expected path /v1/depth, got %s", r.URL.Path)
		}
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Query().Get("symbol") != "BTCUSDT" {
			t.Errorf("expected symbol=BTCUSDT, got %s", r.URL.Query().Get("symbol"))
		}
		if r.Header.Get("x-api-key") != "" {
			t.Error("orderbook is a public endpoint, should not have auth header")
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": map[string]interface{}{
				"ask": []map[string]interface{}{
					{"price": "50000.00000000", "quantity": "0.13471800"},
					{"price": "50100.00000000", "quantity": "0.69433100"},
				},
				"bid": []map[string]interface{}{
					{"price": "49900.00000000", "quantity": "0.00076300"},
					{"price": "49800.00000000", "quantity": "0.00076300"},
				},
			},
			"message": "The operation was successful",
			"success": true,
		})
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	book, err := client.getOrderBook(context.Background(), "BTC/USDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if book.Venue != "wallex" {
		t.Errorf("expected venue wallex, got %s", book.Venue)
	}
	if len(book.Asks) != 2 {
		t.Fatalf("expected 2 asks, got %d", len(book.Asks))
	}
	if len(book.Bids) != 2 {
		t.Fatalf("expected 2 bids, got %d", len(book.Bids))
	}
	if !book.Asks[0].Price.Equal(decimal.NewFromInt(50000)) {
		t.Errorf("expected best ask 50000, got %s", book.Asks[0].Price)
	}
	if !book.Bids[0].Price.Equal(decimal.NewFromInt(49900)) {
		t.Errorf("expected best bid 49900, got %s", book.Bids[0].Price)
	}
}

func TestRestClient_GetRecentTrades(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/trades" {
			t.Errorf("expected path /v1/trades, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("symbol") != "BTCUSDT" {
			t.Errorf("expected symbol=BTCUSDT, got %s", r.URL.Query().Get("symbol"))
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": map[string]interface{}{
				"latestTrades": []map[string]interface{}{
					{
						"symbol":     "BTCUSDT",
						"quantity":   "0.0017700000000000",
						"price":      "50000.0000000000000000",
						"sum":        "88.5000000000000000",
						"isBuyOrder": false,
						"timestamp":  "2021-06-28T00:02:15Z",
					},
					{
						"symbol":     "BTCUSDT",
						"quantity":   "0.0007040000000000",
						"price":      "49500.0000000000000000",
						"sum":        "34.8480000000000000",
						"isBuyOrder": true,
						"timestamp":  "2021-06-27T23:56:36Z",
					},
				},
			},
			"message": "The operation was successful",
			"success": true,
		})
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	trades, err := client.getRecentTrades(context.Background(), "BTC/USDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(trades) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(trades))
	}
	if !trades[0].Price.Equal(decimal.NewFromInt(50000)) {
		t.Errorf("expected price 50000, got %s", trades[0].Price)
	}
	if trades[0].Side != domain.SideSell {
		t.Errorf("expected SELL, got %s", trades[0].Side)
	}
	if trades[1].Side != domain.SideBuy {
		t.Errorf("expected BUY, got %s", trades[1].Side)
	}
	if trades[0].Venue != "wallex" {
		t.Errorf("expected venue wallex, got %s", trades[0].Venue)
	}
}

func TestRestClient_GetFeeTier_FromAPI(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/account/fee" {
			t.Errorf("expected path /v1/account/fee, got %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": map[string]interface{}{
				"BTCUSDT": map[string]interface{}{
					"makerFeeRate":   "0.00200000",
					"takerFeeRate":   "0.00200000",
					"recent_days_sum": 0,
				},
				"BTCTMN": map[string]interface{}{
					"makerFeeRate":   "0.00300000",
					"takerFeeRate":   "0.00400000",
					"recent_days_sum": 240448,
				},
				"default":  []interface{}{},
				"metaData": map[string]interface{}{"levels": []int{0}},
			},
			"message": "The operation was successful",
			"success": true,
		})
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	tier, err := client.getFeeTier(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tier.Venue != "wallex" {
		t.Errorf("expected venue wallex, got %s", tier.Venue)
	}
	// BTCUSDT: maker 0.002 * 10000 = 20 bps, taker 0.002 * 10000 = 20 bps
	if !tier.MakerFeeBps.Equal(decimal.NewFromInt(20)) {
		t.Errorf("expected maker fee 20 bps, got %s", tier.MakerFeeBps)
	}
	if !tier.TakerFeeBps.Equal(decimal.NewFromInt(20)) {
		t.Errorf("expected taker fee 20 bps, got %s", tier.TakerFeeBps)
	}
}

func TestRestClient_APIError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result":  nil,
			"message": "Insufficient balance",
			"success": false,
		})
	})

	client, server := newTestRESTClient(handler)
	defer server.Close()

	_, err := client.placeOrder(context.Background(), domain.OrderRequest{
		InternalID: uuid.Must(uuid.NewV7()),
		Symbol:     "BTC/USDT",
		Side:       domain.SideBuy,
		OrderType:  domain.OrderTypeLimit,
		Price:      decimal.NewFromInt(100),
		Size:       decimal.NewFromFloat(1),
	})

	if err == nil {
		t.Fatal("expected error for failed API response")
	}
}
