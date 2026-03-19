package risk

import (
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

type PnLTracker struct {
	mu sync.RWMutex

	dailyRealizedPnL   decimal.Decimal
	dailyUnrealizedPnL decimal.Decimal
	lastReset          time.Time
}

func NewPnLTracker() *PnLTracker {
	return &PnLTracker{
		lastReset: todayUTC(),
	}
}

func todayUTC() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

func (p *PnLTracker) checkDailyReset() {
	today := todayUTC()
	if today.After(p.lastReset) {
		p.dailyRealizedPnL = decimal.Zero
		p.dailyUnrealizedPnL = decimal.Zero
		p.lastReset = today
	}
}

func (p *PnLTracker) AddRealizedPnL(amount decimal.Decimal) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.checkDailyReset()
	p.dailyRealizedPnL = p.dailyRealizedPnL.Add(amount)
}

func (p *PnLTracker) UpdateUnrealizedPnL(amount decimal.Decimal) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.checkDailyReset()
	p.dailyUnrealizedPnL = amount
}

func (p *PnLTracker) TotalDailyPnL() decimal.Decimal {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.dailyRealizedPnL.Add(p.dailyUnrealizedPnL)
}

func (p *PnLTracker) RealizedPnL() decimal.Decimal {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.dailyRealizedPnL
}

func (p *PnLTracker) UnrealizedPnL() decimal.Decimal {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.dailyUnrealizedPnL
}
