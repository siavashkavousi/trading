package risk

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestPnLTracker_AddRealized(t *testing.T) {
	tracker := NewPnLTracker()

	tracker.AddRealizedPnL(decimal.NewFromInt(100))
	tracker.AddRealizedPnL(decimal.NewFromInt(200))

	if !tracker.RealizedPnL().Equal(decimal.NewFromInt(300)) {
		t.Errorf("expected 300, got %s", tracker.RealizedPnL())
	}
}

func TestPnLTracker_UpdateUnrealized(t *testing.T) {
	tracker := NewPnLTracker()

	tracker.UpdateUnrealizedPnL(decimal.NewFromInt(-500))

	if !tracker.UnrealizedPnL().Equal(decimal.NewFromInt(-500)) {
		t.Errorf("expected -500, got %s", tracker.UnrealizedPnL())
	}

	tracker.UpdateUnrealizedPnL(decimal.NewFromInt(-300))
	if !tracker.UnrealizedPnL().Equal(decimal.NewFromInt(-300)) {
		t.Errorf("expected -300 after update, got %s", tracker.UnrealizedPnL())
	}
}

func TestPnLTracker_TotalPnL(t *testing.T) {
	tracker := NewPnLTracker()

	tracker.AddRealizedPnL(decimal.NewFromInt(-5000))
	tracker.UpdateUnrealizedPnL(decimal.NewFromInt(-3000))

	total := tracker.TotalDailyPnL()
	expected := decimal.NewFromInt(-8000)

	if !total.Equal(expected) {
		t.Errorf("expected %s, got %s", expected, total)
	}
}
