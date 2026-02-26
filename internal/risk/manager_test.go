package risk

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/config"
	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/eventbus"
	"github.com/crypto-trading/trading/internal/marketdata"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	bus := eventbus.New(10, logger)
	mdSvc := marketdata.NewService(bus, 500*time.Millisecond, 2*time.Second, logger)

	mdSvc.UpdateOrderBook(domain.OrderBookSnapshot{
		Venue:  "nobitex",
		Symbol: "BTC/USDT",
		Bids:   []domain.PriceLevel{{Price: decimal.NewFromInt(50000), Size: decimal.NewFromInt(1)}},
		Asks:   []domain.PriceLevel{{Price: decimal.NewFromInt(50001), Size: decimal.NewFromInt(1)}},
	})

	cfg := &config.RiskConfig{
		MaxPosition: map[string]decimal.Decimal{
			"BTC": decimal.NewFromFloat(1.5),
			"ETH": decimal.NewFromInt(25),
			"SOL": decimal.NewFromInt(800),
		},
		MaxNotionalPerVenue: map[string]decimal.Decimal{
			"nobitex": decimal.NewFromInt(250000),
			"kcex":    decimal.NewFromInt(200000),
		},
		DailyLossCapUSDT:    decimal.NewFromInt(12500),
		WarningThresholdPct: 80,
		MaxOpenOrders: config.MaxOpenOrdersConfig{
			Global:    120,
			PerVenue:  70,
			PerSymbol: 20,
		},
		DataFreshness: config.DataFreshnessConfig{
			WarningMs: 500,
			BlockMs:   2000,
		},
	}

	return NewManager(cfg, mdSvc, os.TempDir()+"/test_killswitch.json", logger)
}

func TestValidateSignal_Approved(t *testing.T) {
	mgr := newTestManager(t)

	signal := domain.TradeSignal{
		SignalID:  uuid.Must(uuid.NewV7()),
		Strategy:  domain.StrategyTriArb,
		Venue:     "nobitex",
		Legs: []domain.LegSpec{
			{
				Symbol:    "BTC/USDT",
				Side:      domain.SideBuy,
				Price:     decimal.NewFromInt(50000),
				Size:      decimal.NewFromFloat(0.5),
				OrderType: domain.OrderTypeLimit,
			},
		},
	}

	result := mgr.ValidateSignal(signal)
	if !result.Approved {
		t.Errorf("expected signal to be approved, got rejected: %s - %s", result.Reason, result.Details)
	}
}

func TestValidateSignal_PositionLimit(t *testing.T) {
	mgr := newTestManager(t)

	signal := domain.TradeSignal{
		SignalID:  uuid.Must(uuid.NewV7()),
		Strategy:  domain.StrategyTriArb,
		Venue:     "nobitex",
		Legs: []domain.LegSpec{
			{
				Symbol:    "BTC/USDT",
				Side:      domain.SideBuy,
				Price:     decimal.NewFromInt(50000),
				Size:      decimal.NewFromFloat(2.0),
				OrderType: domain.OrderTypeLimit,
			},
		},
	}

	result := mgr.ValidateSignal(signal)
	if result.Approved {
		t.Error("expected signal to be rejected due to position limit")
	}
	if result.Reason != RejectPositionLimit {
		t.Errorf("expected reason %s, got %s", RejectPositionLimit, result.Reason)
	}
}

func TestValidateSignal_KillSwitch(t *testing.T) {
	mgr := newTestManager(t)
	mgr.ActivateKillSwitch("test reason")

	signal := domain.TradeSignal{
		SignalID:  uuid.Must(uuid.NewV7()),
		Strategy:  domain.StrategyTriArb,
		Venue:     "nobitex",
		Legs: []domain.LegSpec{
			{
				Symbol:    "BTC/USDT",
				Side:      domain.SideBuy,
				Price:     decimal.NewFromInt(50000),
				Size:      decimal.NewFromFloat(0.1),
				OrderType: domain.OrderTypeLimit,
			},
		},
	}

	result := mgr.ValidateSignal(signal)
	if result.Approved {
		t.Error("expected signal to be rejected due to kill switch")
	}
	if result.Reason != RejectKillSwitch {
		t.Errorf("expected reason %s, got %s", RejectKillSwitch, result.Reason)
	}

	mgr.DeactivateKillSwitch()
	result = mgr.ValidateSignal(signal)
	if !result.Approved {
		t.Error("expected signal to be approved after deactivating kill switch")
	}
}

func TestDailyPnLTracking(t *testing.T) {
	tracker := NewPnLTracker()

	tracker.AddRealizedPnL(decimal.NewFromInt(-5000))
	if !tracker.TotalDailyPnL().Equal(decimal.NewFromInt(-5000)) {
		t.Errorf("expected -5000, got %s", tracker.TotalDailyPnL())
	}

	tracker.UpdateUnrealizedPnL(decimal.NewFromInt(-3000))
	expected := decimal.NewFromInt(-8000)
	if !tracker.TotalDailyPnL().Equal(expected) {
		t.Errorf("expected %s, got %s", expected, tracker.TotalDailyPnL())
	}
}
