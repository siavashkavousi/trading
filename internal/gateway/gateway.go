package gateway

import (
	"context"

	"github.com/crypto-trading/trading/internal/domain"
)

type VenueGateway interface {
	SubscribeOrderBook(ctx context.Context, symbol string) (<-chan domain.OrderBookDelta, error)
	SubscribeTrades(ctx context.Context, symbol string) (<-chan domain.Trade, error)
	SubscribeFunding(ctx context.Context, symbol string) (<-chan domain.FundingRate, error)

	PlaceOrder(ctx context.Context, req domain.OrderRequest) (*domain.OrderAck, error)
	CancelOrder(ctx context.Context, orderID string) (*domain.CancelAck, error)
	GetOpenOrders(ctx context.Context, symbol string) ([]domain.Order, error)

	GetBalances(ctx context.Context) (map[string]domain.Balance, error)
	GetPositions(ctx context.Context) ([]domain.Position, error)
	GetFeeTier(ctx context.Context) (*domain.FeeTier, error)

	Connect(ctx context.Context) error
	Close() error

	Name() string
}
