package order

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/eventbus"
	"github.com/crypto-trading/trading/internal/gateway"
)

type Manager struct {
	mu sync.RWMutex

	orders         map[uuid.UUID]*domain.Order
	venueIDMap     map[string]uuid.UUID // venueOrderID → internalID
	idempotencyMap map[string]uuid.UUID // idempotencyKey → internalID

	gateways map[string]gateway.VenueGateway
	bus      *eventbus.EventBus
	logger   *slog.Logger
}

func NewManager(
	gateways map[string]gateway.VenueGateway,
	bus *eventbus.EventBus,
	logger *slog.Logger,
) *Manager {
	return &Manager{
		orders:         make(map[uuid.UUID]*domain.Order),
		venueIDMap:     make(map[string]uuid.UUID),
		idempotencyMap: make(map[string]uuid.UUID),
		gateways:       gateways,
		bus:            bus,
		logger:         logger,
	}
}

func (m *Manager) SubmitOrder(ctx context.Context, req domain.OrderRequest) (*domain.Order, error) {
	m.mu.Lock()
	if existing, ok := m.idempotencyMap[req.IdempotencyKey]; ok && req.IdempotencyKey != "" {
		order := m.orders[existing]
		m.mu.Unlock()
		return order, nil
	}

	order := &domain.Order{
		InternalID: req.InternalID,
		SignalID:   req.SignalID,
		Venue:      req.Venue,
		Symbol:     req.Symbol,
		Side:       req.Side,
		OrderType:  req.OrderType,
		Price:      req.Price,
		Size:       req.Size,
		Status:     domain.OrderStatusPendingNew,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	m.orders[order.InternalID] = order
	if req.IdempotencyKey != "" {
		m.idempotencyMap[req.IdempotencyKey] = order.InternalID
	}
	m.mu.Unlock()

	m.publishStateChange(order, "", domain.OrderStatusPendingNew)

	gw, ok := m.gateways[req.Venue]
	if !ok {
		m.updateStatus(order.InternalID, domain.OrderStatusSubmitFailed)
		return nil, fmt.Errorf("unknown venue: %s", req.Venue)
	}

	m.updateStatus(order.InternalID, domain.OrderStatusSubmitted)

	ack, err := gw.PlaceOrder(ctx, req)
	if err != nil {
		m.updateStatus(order.InternalID, domain.OrderStatusSubmitFailed)
		return nil, fmt.Errorf("place order: %w", err)
	}

	m.mu.Lock()
	order.VenueID = ack.VenueID
	order.Status = ack.Status
	order.UpdatedAt = time.Now()
	m.venueIDMap[ack.VenueID] = order.InternalID
	m.mu.Unlock()

	m.publishStateChange(order, domain.OrderStatusSubmitted, ack.Status)

	return order, nil
}

func (m *Manager) CancelOrder(ctx context.Context, internalID uuid.UUID) error {
	m.mu.RLock()
	order, ok := m.orders[internalID]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("order not found: %s", internalID)
	}
	venueID := order.VenueID
	venue := order.Venue
	m.mu.RUnlock()

	gw, ok := m.gateways[venue]
	if !ok {
		return fmt.Errorf("unknown venue: %s", venue)
	}

	_, err := gw.CancelOrder(ctx, venueID)
	if err != nil {
		return fmt.Errorf("cancel order: %w", err)
	}

	m.updateStatus(internalID, domain.OrderStatusCancelled)
	return nil
}

func (m *Manager) CancelAllOrders(ctx context.Context) {
	m.mu.RLock()
	var activeOrders []uuid.UUID
	for id, order := range m.orders {
		if !order.Status.IsTerminal() {
			activeOrders = append(activeOrders, id)
		}
	}
	m.mu.RUnlock()

	for _, id := range activeOrders {
		if err := m.CancelOrder(ctx, id); err != nil {
			m.logger.Error("failed to cancel order during kill switch",
				"order_id", id, "error", err)
		}
	}
}

func (m *Manager) UpdateOrderFill(internalID uuid.UUID, filledSize, avgPrice decimal.Decimal) {
	m.mu.Lock()
	defer m.mu.Unlock()

	order, ok := m.orders[internalID]
	if !ok {
		return
	}

	prevStatus := order.Status
	order.FilledSize = filledSize
	order.AvgFillPrice = avgPrice
	order.UpdatedAt = time.Now()

	if order.FilledSize.GreaterThanOrEqual(order.Size) {
		order.Status = domain.OrderStatusFilled
	} else if order.FilledSize.IsPositive() {
		order.Status = domain.OrderStatusPartialFill
	}

	if prevStatus != order.Status {
		m.publishStateChangeLocked(order, prevStatus, order.Status)
	}
}

func (m *Manager) GetOrder(internalID uuid.UUID) (*domain.Order, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	order, ok := m.orders[internalID]
	if !ok {
		return nil, false
	}
	copy := *order
	return &copy, true
}

func (m *Manager) GetActiveOrders() []domain.Order {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var active []domain.Order
	for _, order := range m.orders {
		if !order.Status.IsTerminal() {
			active = append(active, *order)
		}
	}
	return active
}

func (m *Manager) GetOrdersBySignal(signalID uuid.UUID) []domain.Order {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var orders []domain.Order
	for _, order := range m.orders {
		if order.SignalID == signalID {
			orders = append(orders, *order)
		}
	}
	return orders
}

func (m *Manager) updateStatus(internalID uuid.UUID, newStatus domain.OrderStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()

	order, ok := m.orders[internalID]
	if !ok {
		return
	}

	prevStatus := order.Status
	order.Status = newStatus
	order.UpdatedAt = time.Now()

	m.publishStateChangeLocked(order, prevStatus, newStatus)
}

func (m *Manager) publishStateChange(order *domain.Order, prev, new domain.OrderStatus) {
	change := domain.OrderStateChange{
		Order:      *order,
		PrevStatus: prev,
		NewStatus:  new,
		Timestamp:  time.Now(),
	}
	m.bus.PublishOrderState(change)
}

func (m *Manager) publishStateChangeLocked(order *domain.Order, prev, new domain.OrderStatus) {
	change := domain.OrderStateChange{
		Order:      *order,
		PrevStatus: prev,
		NewStatus:  new,
		Timestamp:  time.Now(),
	}
	m.bus.PublishOrderState(change)
}

func (m *Manager) CleanupStaleOrders(maxAge time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for id, order := range m.orders {
		if order.Status.IsTerminal() && order.UpdatedAt.Before(cutoff) {
			delete(m.orders, id)
			if order.VenueID != "" {
				delete(m.venueIDMap, order.VenueID)
			}
		}
	}
}
