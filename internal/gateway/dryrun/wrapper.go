package dryrun

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/gateway"
	"github.com/crypto-trading/trading/internal/gateway/simulated"
	"github.com/crypto-trading/trading/internal/marketdata"
)

// Wrapper wraps a real VenueGateway so that all read operations (market data
// subscriptions, balances, positions, fees) hit the live exchange while order
// placement and cancellation are simulated locally.
type Wrapper struct {
	inner     gateway.VenueGateway
	fillSim   simulated.FillSimulator
	mdService *marketdata.Service
	logger    *slog.Logger

	mu         sync.RWMutex
	openOrders map[string]*domain.Order
}

func NewWrapper(
	inner gateway.VenueGateway,
	fillSim simulated.FillSimulator,
	mdService *marketdata.Service,
	logger *slog.Logger,
) *Wrapper {
	return &Wrapper{
		inner:      inner,
		fillSim:    fillSim,
		mdService:  mdService,
		logger:     logger,
		openOrders: make(map[string]*domain.Order),
	}
}

func (w *Wrapper) Name() string { return w.inner.Name() + "_dryrun" }

func (w *Wrapper) Connect(ctx context.Context) error {
	w.logger.Info("dry-run wrapper connecting to live venue", "venue", w.inner.Name())
	return w.inner.Connect(ctx)
}

func (w *Wrapper) Close() error {
	w.logger.Info("dry-run wrapper closing live venue", "venue", w.inner.Name())
	return w.inner.Close()
}

// --- Live read operations delegated to the real gateway ---

func (w *Wrapper) SubscribeOrderBook(ctx context.Context, symbol string) (<-chan domain.OrderBookDelta, error) {
	return w.inner.SubscribeOrderBook(ctx, symbol)
}

func (w *Wrapper) SubscribeTrades(ctx context.Context, symbol string) (<-chan domain.Trade, error) {
	return w.inner.SubscribeTrades(ctx, symbol)
}

func (w *Wrapper) SubscribeFunding(ctx context.Context, symbol string) (<-chan domain.FundingRate, error) {
	return w.inner.SubscribeFunding(ctx, symbol)
}

func (w *Wrapper) GetBalances(ctx context.Context) (map[string]domain.Balance, error) {
	return w.inner.GetBalances(ctx)
}

func (w *Wrapper) GetPositions(ctx context.Context) ([]domain.Position, error) {
	return w.inner.GetPositions(ctx)
}

func (w *Wrapper) GetFeeTier(ctx context.Context) (*domain.FeeTier, error) {
	return w.inner.GetFeeTier(ctx)
}

// --- Simulated write operations ---

// GetOpenOrders returns locally tracked dry-run orders instead of querying the
// exchange, since no real orders are ever placed.
func (w *Wrapper) GetOpenOrders(_ context.Context, symbol string) ([]domain.Order, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	orders := make([]domain.Order, 0)
	for _, o := range w.openOrders {
		if symbol == "" || o.Symbol == symbol {
			orders = append(orders, *o)
		}
	}
	return orders, nil
}

func (w *Wrapper) PlaceOrder(ctx context.Context, req domain.OrderRequest) (*domain.OrderAck, error) {
	venueName := w.inner.Name()

	book, ok := w.mdService.GetOrderBook(venueName, req.Symbol)
	if !ok {
		return &domain.OrderAck{
			InternalID: req.InternalID,
			VenueID:    "",
			Status:     domain.OrderStatusRejected,
			Timestamp:  time.Now(),
		}, fmt.Errorf("no order book available for %s:%s", venueName, req.Symbol)
	}

	fill, err := w.fillSim.SimulateFill(req, book)
	if err != nil {
		return nil, err
	}

	venueID := uuid.New().String()

	w.mu.Lock()
	order := &domain.Order{
		InternalID:   req.InternalID,
		VenueID:      venueID,
		SignalID:     req.SignalID,
		Venue:        venueName,
		Symbol:       req.Symbol,
		Side:         req.Side,
		OrderType:    req.OrderType,
		Price:        req.Price,
		Size:         req.Size,
		FilledSize:   fill.FillSize,
		AvgFillPrice: fill.FillPrice,
		Status:       fill.Status,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if !fill.Status.IsTerminal() {
		w.openOrders[venueID] = order
	}
	w.mu.Unlock()

	w.logger.Info("dry-run order simulated (no real order placed)",
		"venue", venueName,
		"symbol", req.Symbol,
		"side", req.Side,
		"type", req.OrderType,
		"price", fill.FillPrice.String(),
		"size", fill.FillSize.String(),
		"status", fill.Status,
		"fee", fill.Fee.String(),
		"mode", "dry_run",
	)

	return &domain.OrderAck{
		InternalID: req.InternalID,
		VenueID:    venueID,
		Status:     fill.Status,
		Timestamp:  time.Now(),
	}, nil
}

func (w *Wrapper) CancelOrder(_ context.Context, orderID string) (*domain.CancelAck, error) {
	w.mu.Lock()
	order, ok := w.openOrders[orderID]
	if ok {
		order.Status = domain.OrderStatusCancelled
		delete(w.openOrders, orderID)
	}
	w.mu.Unlock()

	w.logger.Info("dry-run order cancelled (no real cancel sent)",
		"venue", w.inner.Name(),
		"orderID", orderID,
		"found", ok,
		"mode", "dry_run",
	)

	return &domain.CancelAck{
		Status:    domain.OrderStatusCancelled,
		Timestamp: time.Now(),
	}, nil
}

// OpenOrderCount returns the number of locally tracked open orders (for metrics).
func (w *Wrapper) OpenOrderCount() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.openOrders)
}

// Inner returns the underlying real gateway for inspection or testing.
func (w *Wrapper) Inner() gateway.VenueGateway {
	return w.inner
}

var _ gateway.VenueGateway = (*Wrapper)(nil)
