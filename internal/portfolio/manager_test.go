package portfolio

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/eventbus"
	"github.com/crypto-trading/trading/internal/marketdata"
)

func newTestManager() *Manager {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	bus := eventbus.New(64, logger)
	mdService := marketdata.NewService(bus, 5*time.Second, 10*time.Second, logger)
	return NewManager(mdService, "dry_run", logger)
}

func TestUpdateBalance(t *testing.T) {
	mgr := newTestManager()

	mgr.UpdateBalance("nobitex", "BTC", decimal.NewFromFloat(1.5), decimal.NewFromFloat(0.5))

	bal, ok := mgr.GetBalance("nobitex", "BTC")
	if !ok {
		t.Fatal("expected to find balance")
	}
	if !bal.Free.Equal(decimal.NewFromFloat(1.5)) {
		t.Errorf("expected free 1.5, got %s", bal.Free)
	}
	if !bal.Locked.Equal(decimal.NewFromFloat(0.5)) {
		t.Errorf("expected locked 0.5, got %s", bal.Locked)
	}
	if !bal.Total.Equal(decimal.NewFromFloat(2.0)) {
		t.Errorf("expected total 2.0, got %s", bal.Total)
	}
}

func TestGetBalanceNotFound(t *testing.T) {
	mgr := newTestManager()

	_, ok := mgr.GetBalance("nobitex", "DOGE")
	if ok {
		t.Error("expected not to find non-existent balance")
	}
}

func TestUpdatePosition(t *testing.T) {
	mgr := newTestManager()

	pos := domain.Position{
		Venue:          "kcex",
		Asset:          "BTC",
		InstrumentType: domain.InstrumentPerp,
		Size:           decimal.NewFromFloat(0.5),
		EntryPrice:     decimal.NewFromInt(50000),
	}
	mgr.UpdatePosition(pos)

	got, ok := mgr.GetPosition("kcex", "BTC")
	if !ok {
		t.Fatal("expected to find position")
	}
	if !got.Size.Equal(decimal.NewFromFloat(0.5)) {
		t.Errorf("expected size 0.5, got %s", got.Size)
	}
}

func TestGetPositionNotFound(t *testing.T) {
	mgr := newTestManager()

	_, ok := mgr.GetPosition("kcex", "DOGE")
	if ok {
		t.Error("expected not to find non-existent position")
	}
}

func TestOnFillEventBuy(t *testing.T) {
	mgr := newTestManager()

	mgr.UpdateBalance("nobitex", "BTC",
		decimal.NewFromFloat(100000),
		decimal.Zero)

	order := domain.Order{
		Venue:        "nobitex",
		Symbol:       "BTC/USDT",
		Side:         domain.SideBuy,
		FilledSize:   decimal.NewFromFloat(0.5),
		AvgFillPrice: decimal.NewFromInt(50000),
	}
	mgr.OnFillEvent(order)

	bal, _ := mgr.GetBalance("nobitex", "BTC")
	expected := decimal.NewFromFloat(100000).Sub(decimal.NewFromFloat(0.5).Mul(decimal.NewFromInt(50000)))
	if !bal.Free.Equal(expected) {
		t.Errorf("expected free %s, got %s", expected, bal.Free)
	}
}

func TestOnFillEventSell(t *testing.T) {
	mgr := newTestManager()

	mgr.UpdateBalance("nobitex", "ETH",
		decimal.NewFromFloat(10000),
		decimal.Zero)

	order := domain.Order{
		Venue:        "nobitex",
		Symbol:       "ETH/USDT",
		Side:         domain.SideSell,
		FilledSize:   decimal.NewFromFloat(1),
		AvgFillPrice: decimal.NewFromInt(3000),
	}
	mgr.OnFillEvent(order)

	bal, _ := mgr.GetBalance("nobitex", "ETH")
	expected := decimal.NewFromFloat(10000).Add(decimal.NewFromFloat(1).Mul(decimal.NewFromInt(3000)))
	if !bal.Free.Equal(expected) {
		t.Errorf("expected free %s, got %s", expected, bal.Free)
	}
}

func TestAddRealizedPnL(t *testing.T) {
	mgr := newTestManager()

	mgr.AddRealizedPnL(decimal.NewFromInt(100))
	mgr.AddRealizedPnL(decimal.NewFromInt(50))

	pnl := mgr.DailyRealizedPnL()
	if !pnl.Equal(decimal.NewFromInt(150)) {
		t.Errorf("expected 150, got %s", pnl)
	}
}

func TestResetDaily(t *testing.T) {
	mgr := newTestManager()

	mgr.AddRealizedPnL(decimal.NewFromInt(100))
	mgr.ResetDaily()

	pnl := mgr.DailyRealizedPnL()
	if !pnl.IsZero() {
		t.Errorf("expected zero after reset, got %s", pnl)
	}
}

func TestGetNetExposure(t *testing.T) {
	mgr := newTestManager()

	mgr.UpdatePosition(domain.Position{
		Venue: "kcex",
		Asset: "BTC",
		Size:  decimal.NewFromFloat(1.0),
	})
	mgr.UpdatePosition(domain.Position{
		Venue: "other",
		Asset: "BTC",
		Size:  decimal.NewFromFloat(0.5),
	})

	exposure := mgr.GetNetExposure("BTC")
	if !exposure.Equal(decimal.NewFromFloat(1.5)) {
		t.Errorf("expected net exposure 1.5, got %s", exposure)
	}
}

func TestGetAllPositions(t *testing.T) {
	mgr := newTestManager()

	mgr.UpdatePosition(domain.Position{
		Venue: "kcex",
		Asset: "BTC",
		Size:  decimal.NewFromFloat(1.0),
	})
	mgr.UpdatePosition(domain.Position{
		Venue: "kcex",
		Asset: "ETH",
		Size:  decimal.NewFromFloat(10.0),
	})

	all := mgr.GetAllPositions()
	if len(all) != 2 {
		t.Errorf("expected 2 positions, got %d", len(all))
	}
}
