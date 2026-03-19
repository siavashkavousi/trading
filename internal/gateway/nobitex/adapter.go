package nobitex

import (
	"context"
	"log/slog"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/gateway"
)

// Gateway implements the VenueGateway interface for Nobitex exchange.
// Nobitex is a spot-only Iranian cryptocurrency exchange.
// Authentication uses Token-based auth (Authorization: Token xxx).
// The API base URL is https://api.nobitex.ir.
type Gateway struct {
	ws     *wsClient
	rest   *restClient
	rl     *gateway.RateLimiter
	logger *slog.Logger
}

// New creates a new Nobitex gateway.
// token is the Nobitex API authentication token obtained from the user's account panel
// or via the /auth/login/ endpoint.
func New(wsURL, restURL, token string, logger *slog.Logger) *Gateway {
	rl := gateway.NewRateLimiter()
	// Nobitex rate limits per their documentation
	rl.AddBucket(domain.EndpointPublicData, 30, 15)
	rl.AddBucket(domain.EndpointPrivateData, 20, 10)
	rl.AddBucket(domain.EndpointOrderPlace, 10, 5)
	rl.AddBucket(domain.EndpointOrderCancel, 20, 10)
	rl.AddBucket(domain.EndpointAccount, 10, 5)

	return &Gateway{
		ws:     newWSClient(wsURL, logger),
		rest:   newRESTClient(restURL, token, rl, logger),
		rl:     rl,
		logger: logger,
	}
}

func (g *Gateway) Name() string { return "nobitex" }

func (g *Gateway) Connect(ctx context.Context) error {
	return g.ws.connect(ctx)
}

func (g *Gateway) Close() error {
	return g.ws.close()
}

func (g *Gateway) SubscribeOrderBook(ctx context.Context, symbol string) (<-chan domain.OrderBookDelta, error) {
	venueSymbol := domain.MapSymbol(symbol, domain.NobitexOrderBookSymbolMap)
	ch := g.ws.subscribeOrderBook(venueSymbol)
	if err := g.ws.subscribe(venueSymbol, "orderbook"); err != nil {
		return nil, err
	}
	go g.ws.readPump(ctx)
	return ch, nil
}

func (g *Gateway) SubscribeTrades(ctx context.Context, symbol string) (<-chan domain.Trade, error) {
	venueSymbol := domain.MapSymbol(symbol, domain.NobitexOrderBookSymbolMap)
	ch := g.ws.subscribeTrades(venueSymbol)
	if err := g.ws.subscribe(venueSymbol, "trades"); err != nil {
		return nil, err
	}
	return ch, nil
}

// SubscribeFunding returns a channel that will never receive data since
// Nobitex is a spot-only exchange with no perpetual contracts or funding rates.
func (g *Gateway) SubscribeFunding(ctx context.Context, symbol string) (<-chan domain.FundingRate, error) {
	venueSymbol := domain.MapSymbol(symbol, domain.NobitexOrderBookSymbolMap)
	ch := g.ws.subscribeFunding(venueSymbol)
	return ch, nil
}

func (g *Gateway) PlaceOrder(ctx context.Context, req domain.OrderRequest) (*domain.OrderAck, error) {
	return g.rest.placeOrder(ctx, req)
}

func (g *Gateway) CancelOrder(ctx context.Context, orderID string) (*domain.CancelAck, error) {
	return g.rest.cancelOrder(ctx, orderID)
}

func (g *Gateway) GetOpenOrders(ctx context.Context, symbol string) ([]domain.Order, error) {
	return g.rest.getOpenOrders(ctx, symbol)
}

func (g *Gateway) GetBalances(ctx context.Context) (map[string]domain.Balance, error) {
	return g.rest.getBalances(ctx)
}

// GetPositions always returns empty since Nobitex is spot-only.
func (g *Gateway) GetPositions(ctx context.Context) ([]domain.Position, error) {
	return g.rest.getPositions(ctx)
}

func (g *Gateway) GetFeeTier(ctx context.Context) (*domain.FeeTier, error) {
	return g.rest.getFeeTier(ctx)
}
