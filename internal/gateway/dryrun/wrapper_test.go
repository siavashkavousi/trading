package dryrun

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/eventbus"
	"github.com/crypto-trading/trading/internal/gateway/simulated"
	"github.com/crypto-trading/trading/internal/marketdata"
)

type mockGateway struct {
	name              string
	connectCalled     bool
	closeCalled       bool
	balances          map[string]domain.Balance
	positions         []domain.Position
	feeTier           *domain.FeeTier
	openOrders        []domain.Order
	placeOrderCalled  bool
	cancelOrderCalled bool
}

func newMockGateway(name string) *mockGateway {
	return &mockGateway{
		name: name,
		balances: map[string]domain.Balance{
			"USDT": {Venue: name, Asset: "USDT", Free: decimal.NewFromInt(5000), Total: decimal.NewFromInt(5000)},
			"BTC":  {Venue: name, Asset: "BTC", Free: decimal.NewFromFloat(0.1), Total: decimal.NewFromFloat(0.1)},
		},
		positions: []domain.Position{
			{Venue: name, Asset: "BTC", Size: decimal.NewFromFloat(0.05)},
		},
		feeTier: &domain.FeeTier{
			Venue:       name,
			MakerFeeBps: decimal.NewFromFloat(1.5),
			TakerFeeBps: decimal.NewFromFloat(4),
			UpdatedAt:   time.Now(),
		},
		openOrders: []domain.Order{},
	}
}

func (m *mockGateway) Name() string                             { return m.name }
func (m *mockGateway) Connect(_ context.Context) error          { m.connectCalled = true; return nil }
func (m *mockGateway) Close() error                             { m.closeCalled = true; return nil }

func (m *mockGateway) SubscribeOrderBook(_ context.Context, _ string) (<-chan domain.OrderBookDelta, error) {
	ch := make(chan domain.OrderBookDelta, 16)
	return ch, nil
}

func (m *mockGateway) SubscribeTrades(_ context.Context, _ string) (<-chan domain.Trade, error) {
	ch := make(chan domain.Trade, 16)
	return ch, nil
}

func (m *mockGateway) SubscribeFunding(_ context.Context, _ string) (<-chan domain.FundingRate, error) {
	ch := make(chan domain.FundingRate, 16)
	return ch, nil
}

func (m *mockGateway) PlaceOrder(_ context.Context, _ domain.OrderRequest) (*domain.OrderAck, error) {
	m.placeOrderCalled = true
	return &domain.OrderAck{Status: domain.OrderStatusFilled, Timestamp: time.Now()}, nil
}

func (m *mockGateway) CancelOrder(_ context.Context, _ string) (*domain.CancelAck, error) {
	m.cancelOrderCalled = true
	return &domain.CancelAck{Status: domain.OrderStatusCancelled, Timestamp: time.Now()}, nil
}

func (m *mockGateway) GetOpenOrders(_ context.Context, _ string) ([]domain.Order, error) {
	return m.openOrders, nil
}

func (m *mockGateway) GetBalances(_ context.Context) (map[string]domain.Balance, error) {
	return m.balances, nil
}

func (m *mockGateway) GetPositions(_ context.Context) ([]domain.Position, error) {
	return m.positions, nil
}

func (m *mockGateway) GetFeeTier(_ context.Context) (*domain.FeeTier, error) {
	return m.feeTier, nil
}

func newTestWrapper(mock *mockGateway) (*Wrapper, *marketdata.Service) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	bus := eventbus.New(64, logger)
	mdService := marketdata.NewService(bus, time.Second, 5*time.Second, logger)
	fillSim := simulated.NewFillSimulator(0, 0, decimal.NewFromFloat(2), decimal.NewFromFloat(5))
	w := NewWrapper(mock, fillSim, mdService, logger)
	return w, mdService
}

func TestWrapper_DelegatesConnect(t *testing.T) {
	mock := newMockGateway("test_venue")
	w, _ := newTestWrapper(mock)

	if err := w.Connect(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.connectCalled {
		t.Error("expected Connect to be delegated to inner gateway")
	}
}

func TestWrapper_DelegatesClose(t *testing.T) {
	mock := newMockGateway("test_venue")
	w, _ := newTestWrapper(mock)

	if err := w.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mock.closeCalled {
		t.Error("expected Close to be delegated to inner gateway")
	}
}

func TestWrapper_DelegatesGetBalances(t *testing.T) {
	mock := newMockGateway("test_venue")
	w, _ := newTestWrapper(mock)

	balances, err := w.GetBalances(context.Background())
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
	if !usdt.Free.Equal(decimal.NewFromInt(5000)) {
		t.Errorf("expected USDT free balance 5000, got %s", usdt.Free)
	}
}

func TestWrapper_DelegatesGetPositions(t *testing.T) {
	mock := newMockGateway("test_venue")
	w, _ := newTestWrapper(mock)

	positions, err := w.GetPositions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(positions))
	}
	if positions[0].Asset != "BTC" {
		t.Errorf("expected BTC position, got %s", positions[0].Asset)
	}
}

func TestWrapper_DelegatesGetFeeTier(t *testing.T) {
	mock := newMockGateway("test_venue")
	w, _ := newTestWrapper(mock)

	fee, err := w.GetFeeTier(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fee.MakerFeeBps.Equal(decimal.NewFromFloat(1.5)) {
		t.Errorf("expected maker fee 1.5 bps, got %s", fee.MakerFeeBps)
	}
}

func TestWrapper_PlaceOrderDoesNotDelegateToInner(t *testing.T) {
	mock := newMockGateway("test_venue")
	w, mdService := newTestWrapper(mock)

	mdService.UpdateOrderBook(domain.OrderBookSnapshot{
		Venue:  "test_venue",
		Symbol: "BTC/USDT",
		Asks: []domain.PriceLevel{
			{Price: decimal.NewFromInt(50000), Size: decimal.NewFromFloat(1.0)},
		},
		Bids: []domain.PriceLevel{
			{Price: decimal.NewFromInt(49900), Size: decimal.NewFromFloat(1.0)},
		},
	})

	req := domain.OrderRequest{
		InternalID: uuid.Must(uuid.NewV7()),
		Symbol:     "BTC/USDT",
		Side:       domain.SideBuy,
		OrderType:  domain.OrderTypeMarket,
		Price:      decimal.NewFromInt(50000),
		Size:       decimal.NewFromFloat(0.1),
	}

	ack, err := w.PlaceOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.placeOrderCalled {
		t.Error("PlaceOrder should NOT be delegated to the real gateway in dry-run mode")
	}
	if ack.Status != domain.OrderStatusFilled {
		t.Errorf("expected FILLED status, got %s", ack.Status)
	}
}

func TestWrapper_CancelOrderDoesNotDelegateToInner(t *testing.T) {
	mock := newMockGateway("test_venue")
	w, mdService := newTestWrapper(mock)

	mdService.UpdateOrderBook(domain.OrderBookSnapshot{
		Venue:  "test_venue",
		Symbol: "BTC/USDT",
		Asks: []domain.PriceLevel{
			{Price: decimal.NewFromInt(50000), Size: decimal.NewFromFloat(1.0)},
		},
		Bids: []domain.PriceLevel{
			{Price: decimal.NewFromInt(49900), Size: decimal.NewFromFloat(1.0)},
		},
	})

	req := domain.OrderRequest{
		InternalID: uuid.Must(uuid.NewV7()),
		Symbol:     "BTC/USDT",
		Side:       domain.SideBuy,
		OrderType:  domain.OrderTypeLimit,
		Price:      decimal.NewFromInt(49800),
		Size:       decimal.NewFromFloat(0.1),
	}
	ack, err := w.PlaceOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error placing order: %v", err)
	}

	cancelAck, err := w.CancelOrder(context.Background(), ack.VenueID)
	if err != nil {
		t.Fatalf("unexpected error cancelling: %v", err)
	}
	if mock.cancelOrderCalled {
		t.Error("CancelOrder should NOT be delegated to the real gateway in dry-run mode")
	}
	if cancelAck.Status != domain.OrderStatusCancelled {
		t.Errorf("expected CANCELLED status, got %s", cancelAck.Status)
	}
}

func TestWrapper_GetOpenOrdersReturnsLocalOrders(t *testing.T) {
	mock := newMockGateway("test_venue")
	w, mdService := newTestWrapper(mock)

	mdService.UpdateOrderBook(domain.OrderBookSnapshot{
		Venue:  "test_venue",
		Symbol: "BTC/USDT",
		Asks: []domain.PriceLevel{
			{Price: decimal.NewFromInt(50000), Size: decimal.NewFromFloat(1.0)},
		},
		Bids: []domain.PriceLevel{
			{Price: decimal.NewFromInt(49900), Size: decimal.NewFromFloat(1.0)},
		},
	})

	req := domain.OrderRequest{
		InternalID: uuid.Must(uuid.NewV7()),
		Symbol:     "BTC/USDT",
		Side:       domain.SideBuy,
		OrderType:  domain.OrderTypeLimit,
		Price:      decimal.NewFromInt(49800),
		Size:       decimal.NewFromFloat(0.1),
	}
	_, err := w.PlaceOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	orders, err := w.GetOpenOrders(context.Background(), "BTC/USDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(orders) != 1 {
		t.Fatalf("expected 1 open order, got %d", len(orders))
	}
	if orders[0].Symbol != "BTC/USDT" {
		t.Errorf("expected BTC/USDT order, got %s", orders[0].Symbol)
	}
}

func TestWrapper_PlaceOrderRejectsWithoutOrderBook(t *testing.T) {
	mock := newMockGateway("test_venue")
	w, _ := newTestWrapper(mock)

	req := domain.OrderRequest{
		InternalID: uuid.Must(uuid.NewV7()),
		Symbol:     "BTC/USDT",
		Side:       domain.SideBuy,
		OrderType:  domain.OrderTypeMarket,
		Size:       decimal.NewFromFloat(0.1),
	}

	ack, err := w.PlaceOrder(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when no order book available")
	}
	if ack.Status != domain.OrderStatusRejected {
		t.Errorf("expected REJECTED status, got %s", ack.Status)
	}
	if mock.placeOrderCalled {
		t.Error("real gateway PlaceOrder should not be called")
	}
}

func TestWrapper_NameIncludesDryrunSuffix(t *testing.T) {
	mock := newMockGateway("test_venue")
	w, _ := newTestWrapper(mock)

	if w.Name() != "test_venue_dryrun" {
		t.Errorf("expected name 'test_venue_dryrun', got '%s'", w.Name())
	}
}

func TestWrapper_FilledOrderNotTrackedAsOpen(t *testing.T) {
	mock := newMockGateway("test_venue")
	w, mdService := newTestWrapper(mock)

	mdService.UpdateOrderBook(domain.OrderBookSnapshot{
		Venue:  "test_venue",
		Symbol: "BTC/USDT",
		Asks: []domain.PriceLevel{
			{Price: decimal.NewFromInt(50000), Size: decimal.NewFromFloat(10.0)},
		},
		Bids: []domain.PriceLevel{
			{Price: decimal.NewFromInt(49900), Size: decimal.NewFromFloat(10.0)},
		},
	})

	req := domain.OrderRequest{
		InternalID: uuid.Must(uuid.NewV7()),
		Symbol:     "BTC/USDT",
		Side:       domain.SideBuy,
		OrderType:  domain.OrderTypeMarket,
		Price:      decimal.NewFromInt(50000),
		Size:       decimal.NewFromFloat(0.1),
	}
	ack, err := w.PlaceOrder(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ack.Status != domain.OrderStatusFilled {
		t.Fatalf("expected FILLED, got %s", ack.Status)
	}

	orders, err := w.GetOpenOrders(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(orders) != 0 {
		t.Errorf("filled orders should not appear in open orders, got %d", len(orders))
	}
}

func TestWrapper_Inner(t *testing.T) {
	mock := newMockGateway("test_venue")
	w, _ := newTestWrapper(mock)

	inner := w.Inner()
	if inner.Name() != "test_venue" {
		t.Errorf("Inner() should return the underlying gateway, got name '%s'", inner.Name())
	}
}
