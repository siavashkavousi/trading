package simulated

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/marketdata"
)

type Gateway struct {
	mu sync.RWMutex

	venueName    string
	fillSim      FillSimulator
	mdService    *marketdata.Service
	logger       *slog.Logger

	balances     map[string]domain.Balance
	positions    []domain.Position
	openOrders   map[string]*domain.Order
	feeTier      *domain.FeeTier

	latencyMs    int
}

func New(venueName string, fillSim FillSimulator, mdService *marketdata.Service,
	initialCapital decimal.Decimal, latencyMs int, logger *slog.Logger) *Gateway {
	balances := map[string]domain.Balance{
		"USDT": {
			Venue: venueName,
			Asset: "USDT",
			Free:  initialCapital,
			Total: initialCapital,
		},
	}

	return &Gateway{
		venueName:  venueName,
		fillSim:    fillSim,
		mdService:  mdService,
		logger:     logger,
		balances:   balances,
		positions:  make([]domain.Position, 0),
		openOrders: make(map[string]*domain.Order),
		feeTier: &domain.FeeTier{
			Venue:       venueName,
			MakerFeeBps: decimal.NewFromFloat(2),
			TakerFeeBps: decimal.NewFromFloat(5),
			UpdatedAt:   time.Now(),
		},
		latencyMs: latencyMs,
	}
}

func (g *Gateway) Name() string { return g.venueName + "_simulated" }

func (g *Gateway) Connect(_ context.Context) error {
	g.logger.Info("simulated gateway connected", "venue", g.venueName)
	return nil
}

func (g *Gateway) Close() error {
	g.logger.Info("simulated gateway closed", "venue", g.venueName)
	return nil
}

func (g *Gateway) SubscribeOrderBook(_ context.Context, symbol string) (<-chan domain.OrderBookDelta, error) {
	ch := make(chan domain.OrderBookDelta, 256)
	return ch, nil
}

func (g *Gateway) SubscribeTrades(_ context.Context, symbol string) (<-chan domain.Trade, error) {
	ch := make(chan domain.Trade, 256)
	return ch, nil
}

func (g *Gateway) SubscribeFunding(_ context.Context, symbol string) (<-chan domain.FundingRate, error) {
	ch := make(chan domain.FundingRate, 256)
	return ch, nil
}

func (g *Gateway) PlaceOrder(ctx context.Context, req domain.OrderRequest) (*domain.OrderAck, error) {
	if g.latencyMs > 0 {
		select {
		case <-time.After(time.Duration(g.latencyMs) * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	book, ok := g.mdService.GetOrderBook(g.venueName, req.Symbol)
	if !ok {
		return &domain.OrderAck{
			InternalID: req.InternalID,
			VenueID:    "",
			Status:     domain.OrderStatusRejected,
			Timestamp:  time.Now(),
		}, fmt.Errorf("no order book available for %s:%s", g.venueName, req.Symbol)
	}

	fill, err := g.fillSim.SimulateFill(req, book)
	if err != nil {
		return nil, err
	}

	venueID := uuid.New().String()

	g.mu.Lock()
	order := &domain.Order{
		InternalID:   req.InternalID,
		VenueID:      venueID,
		SignalID:      req.SignalID,
		Venue:        g.venueName,
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
	g.openOrders[venueID] = order
	g.mu.Unlock()

	g.logger.Info("simulated order placed",
		"venue", g.venueName,
		"symbol", req.Symbol,
		"side", req.Side,
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

func (g *Gateway) CancelOrder(_ context.Context, orderID string) (*domain.CancelAck, error) {
	g.mu.Lock()
	order, ok := g.openOrders[orderID]
	if ok {
		order.Status = domain.OrderStatusCancelled
		delete(g.openOrders, orderID)
	}
	g.mu.Unlock()

	return &domain.CancelAck{
		Status:    domain.OrderStatusCancelled,
		Timestamp: time.Now(),
	}, nil
}

func (g *Gateway) GetOpenOrders(_ context.Context, symbol string) ([]domain.Order, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	orders := make([]domain.Order, 0)
	for _, o := range g.openOrders {
		if symbol == "" || o.Symbol == symbol {
			orders = append(orders, *o)
		}
	}
	return orders, nil
}

func (g *Gateway) GetBalances(_ context.Context) (map[string]domain.Balance, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := make(map[string]domain.Balance, len(g.balances))
	for k, v := range g.balances {
		result[k] = v
	}
	return result, nil
}

func (g *Gateway) GetPositions(_ context.Context) ([]domain.Position, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := make([]domain.Position, len(g.positions))
	copy(result, g.positions)
	return result, nil
}

func (g *Gateway) GetFeeTier(_ context.Context) (*domain.FeeTier, error) {
	return g.feeTier, nil
}
