package portfolio

import (
	"log/slog"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/marketdata"
)

type Manager struct {
	mu sync.RWMutex

	spotBalances map[domain.VenueAssetKey]*domain.Balance
	perpPositions map[domain.VenueAssetKey]*domain.Position

	realizedPnL   decimal.Decimal
	unrealizedPnL decimal.Decimal
	dailyPnLStart time.Time

	mdService *marketdata.Service
	logger    *slog.Logger
	mode      string
}

func NewManager(mdService *marketdata.Service, mode string, logger *slog.Logger) *Manager {
	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	return &Manager{
		spotBalances:  make(map[domain.VenueAssetKey]*domain.Balance),
		perpPositions: make(map[domain.VenueAssetKey]*domain.Position),
		dailyPnLStart: dayStart,
		mdService:     mdService,
		logger:        logger,
		mode:          mode,
	}
}

func (m *Manager) UpdateBalance(venue, asset string, free, locked decimal.Decimal) {
	key := domain.VenueAssetKey{Venue: venue, Asset: asset}
	m.mu.Lock()
	defer m.mu.Unlock()

	m.spotBalances[key] = &domain.Balance{
		Venue:  venue,
		Asset:  asset,
		Free:   free,
		Locked: locked,
		Total:  free.Add(locked),
	}
}

func (m *Manager) UpdatePosition(pos domain.Position) {
	key := domain.VenueAssetKey{Venue: pos.Venue, Asset: pos.Asset}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.perpPositions[key] = &pos
}

func (m *Manager) OnFillEvent(order domain.Order) {
	m.mu.Lock()
	defer m.mu.Unlock()

	asset := extractAsset(order.Symbol)
	key := domain.VenueAssetKey{Venue: order.Venue, Asset: asset}

	if bal, ok := m.spotBalances[key]; ok {
		if order.Side == domain.SideBuy {
			cost := order.AvgFillPrice.Mul(order.FilledSize)
			bal.Free = bal.Free.Sub(cost)
		} else {
			revenue := order.AvgFillPrice.Mul(order.FilledSize)
			bal.Free = bal.Free.Add(revenue)
		}
		bal.Total = bal.Free.Add(bal.Locked)
	}
}

func (m *Manager) AddRealizedPnL(pnl decimal.Decimal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.realizedPnL = m.realizedPnL.Add(pnl)
}

func (m *Manager) ComputeUnrealizedPnL() decimal.Decimal {
	m.mu.RLock()
	defer m.mu.RUnlock()

	total := decimal.Zero
	for key, pos := range m.perpPositions {
		if pos.Size.IsZero() {
			continue
		}

		symbol := pos.Asset + "USDT"
		book, ok := m.mdService.GetOrderBook(key.Venue, symbol)
		if !ok {
			continue
		}

		mid, valid := book.MidPrice()
		if !valid {
			continue
		}

		pnl := mid.Sub(pos.EntryPrice).Mul(pos.Size)
		total = total.Add(pnl)
	}

	return total
}

func (m *Manager) GetNetExposure(asset string) decimal.Decimal {
	m.mu.RLock()
	defer m.mu.RUnlock()

	total := decimal.Zero
	for key, pos := range m.perpPositions {
		if key.Asset == asset {
			total = total.Add(pos.Size)
		}
	}
	return total
}

func (m *Manager) GetBalance(venue, asset string) (*domain.Balance, bool) {
	key := domain.VenueAssetKey{Venue: venue, Asset: asset}
	m.mu.RLock()
	defer m.mu.RUnlock()

	bal, ok := m.spotBalances[key]
	if !ok {
		return nil, false
	}
	b := *bal
	return &b, true
}

func (m *Manager) GetPosition(venue, asset string) (*domain.Position, bool) {
	key := domain.VenueAssetKey{Venue: venue, Asset: asset}
	m.mu.RLock()
	defer m.mu.RUnlock()

	pos, ok := m.perpPositions[key]
	if !ok {
		return nil, false
	}
	p := *pos
	return &p, true
}

func (m *Manager) GetAllPositions() map[domain.VenueAssetKey]*domain.Position {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[domain.VenueAssetKey]*domain.Position, len(m.perpPositions))
	for k, v := range m.perpPositions {
		p := *v
		result[k] = &p
	}
	return result
}

func (m *Manager) DailyRealizedPnL() decimal.Decimal {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.realizedPnL
}

func (m *Manager) ResetDaily() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.realizedPnL = decimal.Zero
	m.unrealizedPnL = decimal.Zero
	m.dailyPnLStart = todayUTC()
}

func todayUTC() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

func extractAsset(symbol string) string {
	for i := 0; i < len(symbol); i++ {
		if symbol[i] == '/' {
			return symbol[:i]
		}
	}
	assets := []string{"BTC", "ETH", "SOL"}
	for _, a := range assets {
		if len(symbol) >= len(a) && symbol[:len(a)] == a {
			return a
		}
	}
	return symbol
}
