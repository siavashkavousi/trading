package kcex

import (
	"context"
	"log/slog"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/gateway"
)

// Gateway implements the VenueGateway interface for the KCEX exchange.
// KCEX uses KuCoin-style API authentication with HMAC-SHA256 (Base64-encoded),
// API key, secret, and passphrase headers.
// Supports both spot (BTC-USDT format) and futures (BTCUSDTM format).
type Gateway struct {
	ws     *wsClient
	rest   *restClient
	rl     *gateway.RateLimiter
	logger *slog.Logger
}

// New creates a new KCEX gateway.
// apiKey, apiSecret, and passphrase are the KCEX API credentials.
// wsURL is the fallback WebSocket URL if the bullet endpoint fails.
func New(wsURL, restURL, apiKey, apiSecret, passphrase string, logger *slog.Logger) *Gateway {
	rl := gateway.NewRateLimiter()
	rl.AddBucket(domain.EndpointPublicData, 40, 20)
	rl.AddBucket(domain.EndpointPrivateData, 20, 10)
	rl.AddBucket(domain.EndpointOrderPlace, 15, 7)
	rl.AddBucket(domain.EndpointOrderCancel, 25, 12)
	rl.AddBucket(domain.EndpointAccount, 10, 5)

	rest := newRESTClient(restURL, apiKey, apiSecret, passphrase, rl, logger)

	return &Gateway{
		ws:     newWSClient(wsURL, rest, logger),
		rest:   rest,
		rl:     rl,
		logger: logger,
	}
}

func (g *Gateway) Name() string { return "kcex" }

func (g *Gateway) Connect(ctx context.Context) error {
	return g.ws.connect(ctx)
}

func (g *Gateway) Close() error {
	return g.ws.close()
}

func (g *Gateway) SubscribeOrderBook(ctx context.Context, symbol string) (<-chan domain.OrderBookDelta, error) {
	venueSymbol := domain.MapKCEXSymbol(symbol)
	ch := g.ws.subscribeOrderBook(venueSymbol)
	topic := "/market/level2:" + venueSymbol
	if err := g.ws.subscribe(topic, false); err != nil {
		return nil, err
	}
	go g.ws.readPump(ctx)
	return ch, nil
}

func (g *Gateway) SubscribeTrades(ctx context.Context, symbol string) (<-chan domain.Trade, error) {
	venueSymbol := domain.MapKCEXSymbol(symbol)
	ch := g.ws.subscribeTrades(venueSymbol)
	topic := "/market/match:" + venueSymbol
	if err := g.ws.subscribe(topic, false); err != nil {
		return nil, err
	}
	return ch, nil
}

func (g *Gateway) SubscribeFunding(ctx context.Context, symbol string) (<-chan domain.FundingRate, error) {
	venueSymbol := domain.MapKCEXSymbol(symbol)
	ch := g.ws.subscribeFunding(venueSymbol)
	topic := "/contract/instrument:" + venueSymbol
	if err := g.ws.subscribe(topic, false); err != nil {
		return nil, err
	}
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

func (g *Gateway) GetPositions(ctx context.Context) ([]domain.Position, error) {
	return g.rest.getPositions(ctx)
}

func (g *Gateway) GetFeeTier(ctx context.Context) (*domain.FeeTier, error) {
	return g.rest.getFeeTier(ctx)
}
