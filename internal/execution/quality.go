package execution

import (
	"sync"

	"github.com/shopspring/decimal"
)

type FillQualityRecord struct {
	Symbol        string
	Side          string
	ExpectedPrice decimal.Decimal
	ActualPrice   decimal.Decimal
	SlippageBps   decimal.Decimal
}

type QualityTracker struct {
	mu      sync.RWMutex
	records []FillQualityRecord
	maxSize int
}

func NewQualityTracker(maxSize int) *QualityTracker {
	return &QualityTracker{
		records: make([]FillQualityRecord, 0, maxSize),
		maxSize: maxSize,
	}
}

func (qt *QualityTracker) RecordFill(symbol, side string, expected, actual decimal.Decimal) {
	if expected.IsZero() {
		return
	}

	slippage := actual.Sub(expected).Div(expected).Mul(decimal.NewFromInt(10000))
	if side == "SELL" {
		slippage = slippage.Neg()
	}

	record := FillQualityRecord{
		Symbol:        symbol,
		Side:          side,
		ExpectedPrice: expected,
		ActualPrice:   actual,
		SlippageBps:   slippage,
	}

	qt.mu.Lock()
	defer qt.mu.Unlock()

	qt.records = append(qt.records, record)
	if len(qt.records) > qt.maxSize {
		qt.records = qt.records[len(qt.records)-qt.maxSize:]
	}
}

func (qt *QualityTracker) AverageSlippageBps() decimal.Decimal {
	qt.mu.RLock()
	defer qt.mu.RUnlock()

	if len(qt.records) == 0 {
		return decimal.Zero
	}

	sum := decimal.Zero
	for _, r := range qt.records {
		sum = sum.Add(r.SlippageBps)
	}
	return sum.Div(decimal.NewFromInt(int64(len(qt.records))))
}

func (qt *QualityTracker) RecentRecords(n int) []FillQualityRecord {
	qt.mu.RLock()
	defer qt.mu.RUnlock()

	if n > len(qt.records) {
		n = len(qt.records)
	}
	result := make([]FillQualityRecord, n)
	copy(result, qt.records[len(qt.records)-n:])
	return result
}
