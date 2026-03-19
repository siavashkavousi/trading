package marketdata

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/eventbus"
)

func TestOrderBookUpdate(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	bus := eventbus.New(10, logger)
	svc := NewService(bus, 500*time.Millisecond, 2*time.Second, logger)

	snap := domain.OrderBookSnapshot{
		Venue:  "nobitex",
		Symbol: "BTC/USDT",
		Bids: []domain.PriceLevel{
			{Price: decimal.NewFromInt(50000), Size: decimal.NewFromFloat(1.5)},
			{Price: decimal.NewFromInt(49999), Size: decimal.NewFromFloat(2.0)},
		},
		Asks: []domain.PriceLevel{
			{Price: decimal.NewFromInt(50001), Size: decimal.NewFromFloat(1.0)},
			{Price: decimal.NewFromInt(50002), Size: decimal.NewFromFloat(3.0)},
		},
	}

	svc.UpdateOrderBook(snap)

	book, ok := svc.GetOrderBook("nobitex", "BTC/USDT")
	if !ok {
		t.Fatal("expected order book to exist")
	}

	bid, hasBid := book.BestBid()
	if !hasBid {
		t.Fatal("expected best bid")
	}
	if !bid.Price.Equal(decimal.NewFromInt(50000)) {
		t.Errorf("expected best bid 50000, got %s", bid.Price)
	}

	ask, hasAsk := book.BestAsk()
	if !hasAsk {
		t.Fatal("expected best ask")
	}
	if !ask.Price.Equal(decimal.NewFromInt(50001)) {
		t.Errorf("expected best ask 50001, got %s", ask.Price)
	}

	mid, valid := book.MidPrice()
	if !valid {
		t.Fatal("expected valid mid price")
	}
	expectedMid := decimal.NewFromFloat(50000.5)
	if !mid.Equal(expectedMid) {
		t.Errorf("expected mid %s, got %s", expectedMid, mid)
	}
}

func TestDataFreshness(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	bus := eventbus.New(10, logger)
	svc := NewService(bus, 100*time.Millisecond, 200*time.Millisecond, logger)

	svc.UpdateOrderBook(domain.OrderBookSnapshot{
		Venue:  "test",
		Symbol: "BTC/USDT",
	})

	if !svc.IsDataFresh("test", "BTC/USDT") {
		t.Error("data should be fresh right after update")
	}

	time.Sleep(150 * time.Millisecond)
	if svc.IsDataFresh("test", "BTC/USDT") {
		t.Error("data should be stale after 150ms with 100ms threshold")
	}

	time.Sleep(100 * time.Millisecond)
	if !svc.IsDataBlocked("test", "BTC/USDT") {
		t.Error("data should be blocked after 250ms with 200ms threshold")
	}
}

func TestTradeRingBuffer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	bus := eventbus.New(10, logger)
	svc := NewService(bus, time.Second, 2*time.Second, logger)

	for i := 0; i < 5; i++ {
		svc.RecordTrade(domain.Trade{
			Venue:  "test",
			Symbol: "BTC/USDT",
			Price:  decimal.NewFromInt(int64(50000 + i)),
		})
	}

	trades := svc.GetRecentTrades("test", "BTC/USDT", 3)
	if len(trades) != 3 {
		t.Errorf("expected 3 recent trades, got %d", len(trades))
	}
}

func TestMissingDataReturnsFalse(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	bus := eventbus.New(10, logger)
	svc := NewService(bus, time.Second, 2*time.Second, logger)

	_, ok := svc.GetOrderBook("nonexistent", "BTC/USDT")
	if ok {
		t.Error("expected false for nonexistent venue")
	}

	if svc.IsDataFresh("nonexistent", "BTC/USDT") {
		t.Error("expected not fresh for nonexistent data")
	}

	if !svc.IsDataBlocked("nonexistent", "BTC/USDT") {
		t.Error("expected blocked for nonexistent data")
	}
}
