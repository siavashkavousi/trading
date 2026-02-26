package domain

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestToFixed(t *testing.T) {
	tests := []struct {
		name     string
		input    decimal.Decimal
		expected FixedPrice
	}{
		{"zero", decimal.Zero, 0},
		{"one", decimal.NewFromInt(1), FixedPrice(PricePrecision)},
		{"fractional", decimal.NewFromFloat(0.5), FixedPrice(PricePrecision / 2)},
		{"large", decimal.NewFromInt(50000), FixedPrice(50000 * PricePrecision)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToFixed(tt.input)
			if got != tt.expected {
				t.Errorf("ToFixed(%s) = %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}

func TestFixedRoundTrip(t *testing.T) {
	values := []decimal.Decimal{
		decimal.NewFromFloat(0.123456789),
		decimal.NewFromInt(50000),
		decimal.NewFromFloat(100.5),
		decimal.Zero,
	}

	for _, v := range values {
		fixed := ToFixed(v)
		back := fixed.ToDecimal()
		diff := v.Sub(back).Abs()
		epsilon := decimal.NewFromFloat(0.000000001)
		if diff.GreaterThan(epsilon) {
			t.Errorf("round trip failed: %s -> %d -> %s (diff: %s)", v, fixed, back, diff)
		}
	}
}

func TestFixedArithmetic(t *testing.T) {
	a := ToFixed(decimal.NewFromInt(100))
	b := ToFixed(decimal.NewFromInt(50))

	if sum := a.Add(b); sum != ToFixed(decimal.NewFromInt(150)) {
		t.Errorf("Add: got %d, want %d", sum, ToFixed(decimal.NewFromInt(150)))
	}

	if diff := a.Sub(b); diff != ToFixed(decimal.NewFromInt(50)) {
		t.Errorf("Sub: got %d, want %d", diff, ToFixed(decimal.NewFromInt(50)))
	}

	if !a.GT(b) {
		t.Error("100 should be GT 50")
	}

	if a.LT(b) {
		t.Error("100 should not be LT 50")
	}
}

func TestFixedFromBps(t *testing.T) {
	bps18 := FixedFromBps(18)
	expected := ToFixed(decimal.NewFromFloat(0.0018))
	diff := bps18 - expected
	if diff < -1 || diff > 1 {
		t.Errorf("FixedFromBps(18) = %d, want ~%d", bps18, expected)
	}
}
