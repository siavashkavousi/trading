package order

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
	"github.com/crypto-trading/trading/internal/gateway"
)

type mockGateway struct {
	placeErr  error
	cancelErr error
	lastReq   domain.OrderRequest
}

func (m *mockGateway) Connect(_ context.Context) error { return nil }
func (m *mockGateway) Close() error                    { return nil }
func (m *mockGateway) Name() string                    { return "test" }
func (m *mockGateway) SubscribeOrderBook(_ context.Context, _ string) (<-chan domain.OrderBookDelta, error) {
	return nil, nil
}
func (m *mockGateway) SubscribeTrades(_ context.Context, _ string) (<-chan domain.Trade, error) {
	return nil, nil
}
func (m *mockGateway) SubscribeFunding(_ context.Context, _ string) (<-chan domain.FundingRate, error) {
	return nil, nil
}
func (m *mockGateway) GetBalances(_ context.Context) (map[string]domain.Balance, error) {
	return nil, nil
}
func (m *mockGateway) GetPositions(_ context.Context) ([]domain.Position, error) {
	return nil, nil
}
func (m *mockGateway) GetFeeTier(_ context.Context) (*domain.FeeTier, error) { return nil, nil }
func (m *mockGateway) GetOpenOrders(_ context.Context, _ string) ([]domain.Order, error) {
	return nil, nil
}

func (m *mockGateway) PlaceOrder(_ context.Context, req domain.OrderRequest) (*domain.OrderAck, error) {
	m.lastReq = req
	if m.placeErr != nil {
		return nil, m.placeErr
	}
	return &domain.OrderAck{
		InternalID: req.InternalID,
		VenueID:    "venue-" + req.InternalID.String()[:8],
		Status:     domain.OrderStatusAcknowledged,
		Timestamp:  time.Now(),
	}, nil
}

func (m *mockGateway) CancelOrder(_ context.Context, orderID string) (*domain.CancelAck, error) {
	if m.cancelErr != nil {
		return nil, m.cancelErr
	}
	return &domain.CancelAck{
		Status:    domain.OrderStatusCancelled,
		Timestamp: time.Now(),
	}, nil
}

var _ gateway.VenueGateway = (*mockGateway)(nil)

func newTestManager() (*Manager, *mockGateway) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	bus := eventbus.New(64, logger)
	mock := &mockGateway{}
	gateways := map[string]gateway.VenueGateway{"test": mock}
	return NewManager(gateways, bus, logger), mock
}

func TestSubmitOrder(t *testing.T) {
	mgr, _ := newTestManager()
	ctx := context.Background()

	req := domain.OrderRequest{
		InternalID:     NewOrderID(),
		SignalID:       uuid.New(),
		Venue:          "test",
		Symbol:         "BTC/USDT",
		Side:           domain.SideBuy,
		OrderType:      domain.OrderTypeLimit,
		Price:          decimal.NewFromInt(50000),
		Size:           decimal.NewFromFloat(0.1),
		IdempotencyKey: "test-key-1",
	}

	order, err := mgr.SubmitOrder(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if order.Status != domain.OrderStatusAcknowledged {
		t.Errorf("expected status %s, got %s", domain.OrderStatusAcknowledged, order.Status)
	}
	if order.VenueID == "" {
		t.Error("expected non-empty venue ID")
	}
}

func TestSubmitOrderIdempotency(t *testing.T) {
	mgr, _ := newTestManager()
	ctx := context.Background()

	req := domain.OrderRequest{
		InternalID:     NewOrderID(),
		SignalID:       uuid.New(),
		Venue:          "test",
		Symbol:         "BTC/USDT",
		Side:           domain.SideBuy,
		OrderType:      domain.OrderTypeLimit,
		Price:          decimal.NewFromInt(50000),
		Size:           decimal.NewFromFloat(0.1),
		IdempotencyKey: "idempotent-key",
	}

	order1, err := mgr.SubmitOrder(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error on first submit: %v", err)
	}

	req.InternalID = NewOrderID()
	order2, err := mgr.SubmitOrder(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error on second submit: %v", err)
	}

	if order1.InternalID != order2.InternalID {
		t.Errorf("idempotent requests should return the same order, got %s and %s",
			order1.InternalID, order2.InternalID)
	}
}

func TestSubmitOrderUnknownVenue(t *testing.T) {
	mgr, _ := newTestManager()
	ctx := context.Background()

	req := domain.OrderRequest{
		InternalID: NewOrderID(),
		Venue:      "nonexistent",
		Symbol:     "BTC/USDT",
		Side:       domain.SideBuy,
	}

	_, err := mgr.SubmitOrder(ctx, req)
	if err == nil {
		t.Fatal("expected error for unknown venue")
	}
}

func TestGetOrder(t *testing.T) {
	mgr, _ := newTestManager()
	ctx := context.Background()

	id := NewOrderID()
	req := domain.OrderRequest{
		InternalID: id,
		SignalID:   uuid.New(),
		Venue:      "test",
		Symbol:     "BTC/USDT",
		Side:       domain.SideBuy,
		OrderType:  domain.OrderTypeLimit,
		Price:      decimal.NewFromInt(50000),
		Size:       decimal.NewFromFloat(0.1),
	}

	mgr.SubmitOrder(ctx, req)

	order, ok := mgr.GetOrder(id)
	if !ok {
		t.Fatal("expected to find order")
	}
	if order.InternalID != id {
		t.Errorf("expected ID %s, got %s", id, order.InternalID)
	}

	_, ok = mgr.GetOrder(uuid.New())
	if ok {
		t.Error("expected not to find non-existent order")
	}
}

func TestUpdateOrderFill(t *testing.T) {
	mgr, _ := newTestManager()
	ctx := context.Background()

	id := NewOrderID()
	req := domain.OrderRequest{
		InternalID: id,
		SignalID:   uuid.New(),
		Venue:      "test",
		Symbol:     "BTC/USDT",
		Side:       domain.SideBuy,
		OrderType:  domain.OrderTypeLimit,
		Price:      decimal.NewFromInt(50000),
		Size:       decimal.NewFromFloat(1),
	}

	mgr.SubmitOrder(ctx, req)

	mgr.UpdateOrderFill(id, decimal.NewFromFloat(0.5), decimal.NewFromInt(50100))
	order, _ := mgr.GetOrder(id)
	if order.Status != domain.OrderStatusPartialFill {
		t.Errorf("expected partial fill, got %s", order.Status)
	}

	mgr.UpdateOrderFill(id, decimal.NewFromFloat(1), decimal.NewFromInt(50050))
	order, _ = mgr.GetOrder(id)
	if order.Status != domain.OrderStatusFilled {
		t.Errorf("expected filled, got %s", order.Status)
	}
}

func TestGetActiveOrders(t *testing.T) {
	mgr, _ := newTestManager()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		req := domain.OrderRequest{
			InternalID: NewOrderID(),
			SignalID:   uuid.New(),
			Venue:      "test",
			Symbol:     "BTC/USDT",
			Side:       domain.SideBuy,
			OrderType:  domain.OrderTypeLimit,
			Price:      decimal.NewFromInt(50000),
			Size:       decimal.NewFromFloat(0.1),
		}
		mgr.SubmitOrder(ctx, req)
	}

	active := mgr.GetActiveOrders()
	if len(active) != 3 {
		t.Errorf("expected 3 active orders, got %d", len(active))
	}
}

func TestCleanupStaleOrders(t *testing.T) {
	mgr, _ := newTestManager()
	ctx := context.Background()

	id := NewOrderID()
	req := domain.OrderRequest{
		InternalID: id,
		SignalID:   uuid.New(),
		Venue:      "test",
		Symbol:     "BTC/USDT",
		Side:       domain.SideBuy,
		OrderType:  domain.OrderTypeLimit,
		Price:      decimal.NewFromInt(50000),
		Size:       decimal.NewFromFloat(1),
	}

	mgr.SubmitOrder(ctx, req)
	mgr.UpdateOrderFill(id, decimal.NewFromFloat(1), decimal.NewFromInt(50000))

	mgr.CleanupStaleOrders(0)

	_, ok := mgr.GetOrder(id)
	if ok {
		t.Error("expected stale order to be cleaned up")
	}
}
