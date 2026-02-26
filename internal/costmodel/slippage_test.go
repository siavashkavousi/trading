package costmodel

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestSlippageCurve_Default(t *testing.T) {
	curve := NewSlippageCurve()

	tests := []struct {
		name     string
		size     decimal.Decimal
		minBps   decimal.Decimal
		maxBps   decimal.Decimal
	}{
		{"tiny order", decimal.NewFromFloat(0.001), decimal.NewFromFloat(0), decimal.NewFromFloat(2)},
		{"small order", decimal.NewFromFloat(0.5), decimal.NewFromFloat(1), decimal.NewFromFloat(5)},
		{"medium order", decimal.NewFromFloat(5), decimal.NewFromFloat(5), decimal.NewFromFloat(15)},
		{"large order", decimal.NewFromFloat(500), decimal.NewFromFloat(10), decimal.NewFromFloat(50)},
		{"huge order", decimal.NewFromFloat(5000), decimal.NewFromFloat(40), decimal.NewFromFloat(60)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slippage := curve.EstimateSlippage(tt.size)
			if slippage.LessThan(tt.minBps) || slippage.GreaterThan(tt.maxBps) {
				t.Errorf("size %s: expected slippage between %s and %s bps, got %s",
					tt.size, tt.minBps, tt.maxBps, slippage)
			}
		})
	}
}

func TestSlippageCurve_Monotonic(t *testing.T) {
	curve := NewSlippageCurve()

	sizes := []decimal.Decimal{
		decimal.NewFromFloat(0.01),
		decimal.NewFromFloat(0.1),
		decimal.NewFromFloat(1),
		decimal.NewFromFloat(10),
		decimal.NewFromFloat(100),
		decimal.NewFromFloat(1000),
	}

	prevSlippage := decimal.Zero
	for _, size := range sizes {
		slippage := curve.EstimateSlippage(size)
		if slippage.LessThan(prevSlippage) {
			t.Errorf("slippage should be monotonically increasing: size %s gave %s < previous %s",
				size, slippage, prevSlippage)
		}
		prevSlippage = slippage
	}
}

func TestSlippageCurve_Update(t *testing.T) {
	curve := NewSlippageCurve()

	newPoints := []SlippagePoint{
		{Size: decimal.NewFromFloat(0.1), SlippageBps: decimal.NewFromFloat(3)},
		{Size: decimal.NewFromFloat(1), SlippageBps: decimal.NewFromFloat(8)},
		{Size: decimal.NewFromFloat(10), SlippageBps: decimal.NewFromFloat(15)},
	}
	curve.UpdateFromFills(newPoints)

	slippage := curve.EstimateSlippage(decimal.NewFromFloat(0.5))
	if slippage.LessThan(decimal.NewFromFloat(3)) || slippage.GreaterThan(decimal.NewFromFloat(8)) {
		t.Errorf("expected slippage between 3 and 8, got %s", slippage)
	}
}
