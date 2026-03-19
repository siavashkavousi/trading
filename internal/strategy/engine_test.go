package strategy

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/eventbus"
)

type testModule struct {
	obCount atomic.Int32
	frCount atomic.Int32
}

func (m *testModule) OnOrderBookUpdate(_ domain.OrderBookSnapshot) {
	m.obCount.Add(1)
}

func (m *testModule) OnFundingRateUpdate(_ domain.FundingRate) {
	m.frCount.Add(1)
}

func TestEngineDispatchesOrderBookToModules(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	bus := eventbus.New(64, logger)

	engine := NewEngine(bus, logger)
	mod1 := &testModule{}
	mod2 := &testModule{}
	engine.RegisterModule(mod1)
	engine.RegisterModule(mod2)

	ctx, cancel := context.WithCancel(context.Background())
	go engine.Run(ctx)

	time.Sleep(20 * time.Millisecond)

	bus.PublishOrderBook(domain.OrderBookSnapshot{
		Venue:  "test",
		Symbol: "BTC/USDT",
	})

	time.Sleep(50 * time.Millisecond)
	cancel()

	if mod1.obCount.Load() != 1 {
		t.Errorf("mod1 expected 1 order book update, got %d", mod1.obCount.Load())
	}
	if mod2.obCount.Load() != 1 {
		t.Errorf("mod2 expected 1 order book update, got %d", mod2.obCount.Load())
	}
}

func TestEngineDispatchesFundingRateToModules(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	bus := eventbus.New(64, logger)

	engine := NewEngine(bus, logger)
	mod := &testModule{}
	engine.RegisterModule(mod)

	ctx, cancel := context.WithCancel(context.Background())
	go engine.Run(ctx)

	time.Sleep(20 * time.Millisecond)

	bus.PublishFundingRate(domain.FundingRate{
		Venue:  "test",
		Symbol: "BTCUSDT",
	})

	time.Sleep(50 * time.Millisecond)
	cancel()

	if mod.frCount.Load() != 1 {
		t.Errorf("expected 1 funding rate update, got %d", mod.frCount.Load())
	}
}

func TestEngineStopsOnContextCancel(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	bus := eventbus.New(64, logger)

	engine := NewEngine(bus, logger)
	engine.RegisterModule(&testModule{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		engine.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("engine did not stop after context cancellation")
	}
}

func TestEngineNoModulesNoPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	bus := eventbus.New(64, logger)

	engine := NewEngine(bus, logger)

	ctx, cancel := context.WithCancel(context.Background())
	go engine.Run(ctx)

	time.Sleep(20 * time.Millisecond)

	bus.PublishOrderBook(domain.OrderBookSnapshot{Venue: "test", Symbol: "BTC/USDT"})
	bus.PublishFundingRate(domain.FundingRate{Venue: "test", Symbol: "BTCUSDT"})

	time.Sleep(50 * time.Millisecond)
	cancel()
}
