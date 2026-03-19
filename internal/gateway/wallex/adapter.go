package wallex

import (
	"context"
	"log/slog"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/gateway"
)

// Gateway implements the VenueGateway interface for Wallex exchange (wallex.ir).
// Wallex is a spot-only Iranian cryptocurrency exchange.
// Authentication uses API key via x-api-key HTTP header.
// The REST API base URL is https://api.wallex.ir.
// WebSocket uses socket.io at https://api.wallex.ir/socket.io.
type Gateway struct {
	ws     *wsClient
	rest   *restClient
	rl     *gateway.RateLimiter
	logger *slog.Logger
}

// New creates a new Wallex gateway.
// apiKey is obtained from the Wallex API Management panel (max 90-day validity).
func New(wsURL, restURL, apiKey string, logger *slog.Logger) *Gateway {
	rl := gateway.NewRateLimiter()
	rl.AddBucket(domain.EndpointPublicData, 30, 15)
	rl.AddBucket(domain.EndpointPrivateData, 20, 10)
	rl.AddBucket(domain.EndpointOrderPlace, 10, 5)
	rl.AddBucket(domain.EndpointOrderCancel, 20, 10)
	rl.AddBucket(domain.EndpointAccount, 10, 5)

	return &Gateway{
		ws:     newWSClient(wsURL, logger),
		rest:   newRESTClient(restURL, apiKey, rl, logger),
		rl:     rl,
		logger: logger,
	}
}

func (g *Gateway) Name() string { return "wallex" }

func (g *Gateway) Connect(ctx context.Context) error {
	return g.ws.connect(ctx)
}

func (g *Gateway) Close() error {
	return g.ws.close()
}

func (g *Gateway) SubscribeOrderBook(ctx context.Context, symbol string) (<-chan domain.OrderBookDelta, error) {
	venueSymbol := domain.MapSymbol(symbol, domain.WallexSymbolMap)
	ch := g.ws.subscribeOrderBook(venueSymbol)
	// Wallex uses separate channels for buy and sell depth
	if err := g.ws.subscribe(venueSymbol, "buyDepth"); err != nil {
		return nil, err
	}
	if err := g.ws.subscribe(venueSymbol, "sellDepth"); err != nil {
		return nil, err
	}
	go g.ws.readPump(ctx)
	return ch, nil
}

func (g *Gateway) SubscribeTrades(ctx context.Context, symbol string) (<-chan domain.Trade, error) {
	venueSymbol := domain.MapSymbol(symbol, domain.WallexSymbolMap)
	ch := g.ws.subscribeTrades(venueSymbol)
	if err := g.ws.subscribe(venueSymbol, "trade"); err != nil {
		return nil, err
	}
	return ch, nil
}

// SubscribeFunding returns a channel that will never receive data since
// Wallex is a spot-only exchange with no perpetual contracts or funding rates.
func (g *Gateway) SubscribeFunding(ctx context.Context, symbol string) (<-chan domain.FundingRate, error) {
	venueSymbol := domain.MapSymbol(symbol, domain.WallexSymbolMap)
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

// GetPositions always returns empty since Wallex is spot-only.
func (g *Gateway) GetPositions(ctx context.Context) ([]domain.Position, error) {
	return g.rest.getPositions(ctx)
}

func (g *Gateway) GetFeeTier(ctx context.Context) (*domain.FeeTier, error) {
	return g.rest.getFeeTier(ctx)
}
