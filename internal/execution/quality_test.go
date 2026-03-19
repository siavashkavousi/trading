package execution

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestQualityTrackerRecordFill(t *testing.T) {
	qt := NewQualityTracker(100)

	qt.RecordFill("BTC/USDT", "BUY", decimal.NewFromInt(50000), decimal.NewFromInt(50010))

	records := qt.RecentRecords(10)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Symbol != "BTC/USDT" {
		t.Errorf("expected BTC/USDT, got %s", records[0].Symbol)
	}
	if records[0].SlippageBps.IsZero() {
		t.Error("expected non-zero slippage")
	}
}

func TestQualityTrackerSkipsZeroExpected(t *testing.T) {
	qt := NewQualityTracker(100)

	qt.RecordFill("BTC/USDT", "BUY", decimal.Zero, decimal.NewFromInt(50000))

	records := qt.RecentRecords(10)
	if len(records) != 0 {
		t.Errorf("expected no records for zero expected price, got %d", len(records))
	}
}

func TestQualityTrackerCapAtMaxSize(t *testing.T) {
	qt := NewQualityTracker(5)

	for i := 0; i < 10; i++ {
		qt.RecordFill("BTC/USDT", "BUY",
			decimal.NewFromInt(50000),
			decimal.NewFromInt(50000+int64(i)))
	}

	records := qt.RecentRecords(100)
	if len(records) != 5 {
		t.Errorf("expected 5 records (capped), got %d", len(records))
	}
}

func TestQualityTrackerAverageSlippage(t *testing.T) {
	qt := NewQualityTracker(100)

	if avg := qt.AverageSlippageBps(); !avg.IsZero() {
		t.Errorf("expected zero average for empty tracker, got %s", avg)
	}

	qt.RecordFill("BTC/USDT", "BUY", decimal.NewFromInt(100), decimal.NewFromInt(101))
	qt.RecordFill("BTC/USDT", "BUY", decimal.NewFromInt(100), decimal.NewFromInt(102))

	avg := qt.AverageSlippageBps()
	if avg.LessThanOrEqual(decimal.Zero) {
		t.Errorf("expected positive average slippage for buys above expected, got %s", avg)
	}
}

func TestQualityTrackerSellSlippageNegated(t *testing.T) {
	qt := NewQualityTracker(100)

	qt.RecordFill("BTC/USDT", "SELL", decimal.NewFromInt(100), decimal.NewFromInt(101))

	records := qt.RecentRecords(1)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].SlippageBps.IsPositive() {
		t.Errorf("expected negative/zero slippage for sell with higher actual, got %s",
			records[0].SlippageBps)
	}
}

func TestQualityTrackerRecentRecordsClamp(t *testing.T) {
	qt := NewQualityTracker(100)

	qt.RecordFill("BTC/USDT", "BUY", decimal.NewFromInt(100), decimal.NewFromInt(101))

	records := qt.RecentRecords(100)
	if len(records) != 1 {
		t.Errorf("expected 1 record when requesting more than available, got %d", len(records))
	}
}
