package costmodel

import (
	"sync"

	"github.com/shopspring/decimal"
)

type SlippagePoint struct {
	Size        decimal.Decimal
	SlippageBps decimal.Decimal
}

type SlippageCurve struct {
	mu     sync.RWMutex
	points []SlippagePoint
}

func NewSlippageCurve() *SlippageCurve {
	return &SlippageCurve{
		points: defaultSlippageCurve(),
	}
}

func defaultSlippageCurve() []SlippagePoint {
	return []SlippagePoint{
		{Size: decimal.NewFromFloat(0.01), SlippageBps: decimal.NewFromFloat(1)},
		{Size: decimal.NewFromFloat(0.1), SlippageBps: decimal.NewFromFloat(2)},
		{Size: decimal.NewFromFloat(1), SlippageBps: decimal.NewFromFloat(5)},
		{Size: decimal.NewFromFloat(10), SlippageBps: decimal.NewFromFloat(10)},
		{Size: decimal.NewFromFloat(100), SlippageBps: decimal.NewFromFloat(20)},
		{Size: decimal.NewFromFloat(1000), SlippageBps: decimal.NewFromFloat(50)},
	}
}

func (sc *SlippageCurve) EstimateSlippage(orderSize decimal.Decimal) decimal.Decimal {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	if len(sc.points) == 0 {
		return decimal.NewFromFloat(5)
	}

	if orderSize.LessThanOrEqual(sc.points[0].Size) {
		return sc.points[0].SlippageBps
	}

	last := sc.points[len(sc.points)-1]
	if orderSize.GreaterThanOrEqual(last.Size) {
		return last.SlippageBps
	}

	for i := 1; i < len(sc.points); i++ {
		if orderSize.LessThanOrEqual(sc.points[i].Size) {
			prev := sc.points[i-1]
			curr := sc.points[i]

			ratio := orderSize.Sub(prev.Size).Div(curr.Size.Sub(prev.Size))
			return prev.SlippageBps.Add(ratio.Mul(curr.SlippageBps.Sub(prev.SlippageBps)))
		}
	}

	return last.SlippageBps
}

func (sc *SlippageCurve) UpdateFromFills(fills []SlippagePoint) {
	if len(fills) == 0 {
		return
	}

	sc.mu.Lock()
	defer sc.mu.Unlock()

	sc.points = make([]SlippagePoint, len(fills))
	copy(sc.points, fills)

	for i := 1; i < len(sc.points); i++ {
		for j := i; j > 0; j-- {
			if sc.points[j].Size.LessThan(sc.points[j-1].Size) {
				sc.points[j], sc.points[j-1] = sc.points[j-1], sc.points[j]
			} else {
				break
			}
		}
	}
}
