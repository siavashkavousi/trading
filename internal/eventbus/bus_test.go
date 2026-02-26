package eventbus

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/shopspring/decimal"
)

func TestEventBusOrderBook(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	bus := New(10, logger)
	defer bus.Close()

	ch := bus.SubscribeOrderBook()

	snap := domain.OrderBookSnapshot{
		Venue:  "test",
		Symbol: "BTC/USDT",
		Bids: []domain.PriceLevel{
			{Price: decimal.NewFromInt(50000), Size: decimal.NewFromFloat(1.5)},
		},
		Asks: []domain.PriceLevel{
			{Price: decimal.NewFromInt(50001), Size: decimal.NewFromFloat(2.0)},
		},
	}

	bus.PublishOrderBook(snap)

	select {
	case received := <-ch:
		if received.Venue != "test" {
			t.Errorf("expected venue 'test', got '%s'", received.Venue)
		}
		if received.Symbol != "BTC/USDT" {
			t.Errorf("expected symbol 'BTC/USDT', got '%s'", received.Symbol)
		}
		if len(received.Bids) != 1 {
			t.Errorf("expected 1 bid, got %d", len(received.Bids))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for order book event")
	}
}

func TestEventBusSignal(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	bus := New(10, logger)
	defer bus.Close()

	ch := bus.SubscribeSignal()

	signal := domain.TradeSignal{
		Strategy: domain.StrategyTriArb,
		Venue:    "nobitex",
	}

	bus.PublishSignal(signal)

	select {
	case received := <-ch:
		if received.Strategy != domain.StrategyTriArb {
			t.Errorf("expected strategy TRI_ARB, got %s", received.Strategy)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for signal event")
	}
}

func TestEventBusDropsOnFull(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	bus := New(1, logger)
	defer bus.Close()

	_ = bus.SubscribeOrderBook()

	for i := 0; i < 10; i++ {
		bus.PublishOrderBook(domain.OrderBookSnapshot{
			Venue:  "test",
			Symbol: "BTC/USDT",
		})
	}
}
