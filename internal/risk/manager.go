package risk

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/config"
	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/marketdata"
)

type RejectionReason string

const (
	RejectPositionLimit    RejectionReason = "position_limit_exceeded"
	RejectNotionalLimit    RejectionReason = "notional_limit_exceeded"
	RejectDailyLoss        RejectionReason = "daily_loss_cap"
	RejectGlobalOrders     RejectionReason = "global_order_limit"
	RejectVenueOrders      RejectionReason = "venue_order_limit"
	RejectSymbolOrders     RejectionReason = "symbol_order_limit"
	RejectDataStale        RejectionReason = "data_stale"
	RejectKillSwitch       RejectionReason = "kill_switch_active"
	RejectHalted           RejectionReason = "system_halted"
)

type ValidationResult struct {
	Approved bool
	Reason   RejectionReason
	Details  string
}

type Manager struct {
	mu sync.RWMutex

	state      *domain.RiskState
	pnlTracker *PnLTracker
	killSwitch *KillSwitch
	mdService  *marketdata.Service
	cfg        *config.RiskConfig
	logger     *slog.Logger

	onKillSwitch func()
}

func NewManager(
	cfg *config.RiskConfig,
	mdService *marketdata.Service,
	killSwitchPath string,
	logger *slog.Logger,
) *Manager {
	return &Manager{
		state: &domain.RiskState{
			Mode:            domain.RiskModeNormal,
			Positions:       make(map[domain.VenueAssetKey]*domain.Position),
			OpenOrderCounts: domain.OrderCountState{
				PerVenue:  make(map[string]int),
				PerSymbol: make(map[string]int),
			},
			VenueNotionals: make(map[string]decimal.Decimal),
		},
		pnlTracker: NewPnLTracker(),
		killSwitch: NewKillSwitch(killSwitchPath, logger),
		mdService:  mdService,
		cfg:        cfg,
		logger:     logger,
	}
}

func (m *Manager) SetKillSwitchCallback(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onKillSwitch = fn
}

func (m *Manager) ValidateSignal(signal domain.TradeSignal) ValidationResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.killSwitch.IsActive() {
		return ValidationResult{Approved: false, Reason: RejectKillSwitch, Details: m.killSwitch.Reason()}
	}

	if m.state.Mode == domain.RiskModeHalted {
		return ValidationResult{Approved: false, Reason: RejectHalted}
	}

	for _, leg := range signal.Legs {
		if m.mdService.IsDataBlocked(signal.Venue, leg.Symbol) {
			return ValidationResult{
				Approved: false,
				Reason:   RejectDataStale,
				Details:  fmt.Sprintf("data stale for %s:%s", signal.Venue, leg.Symbol),
			}
		}
	}

	for _, leg := range signal.Legs {
		asset := extractAsset(leg.Symbol)
		maxPos, ok := m.cfg.MaxPosition[asset]
		if ok {
			key := domain.VenueAssetKey{Venue: signal.Venue, Asset: asset}
			currentPos := decimal.Zero
			if pos, exists := m.state.Positions[key]; exists {
				currentPos = pos.Size.Abs()
			}
			newSize := currentPos.Add(leg.Size)
			if newSize.GreaterThan(maxPos) {
				return ValidationResult{
					Approved: false,
					Reason:   RejectPositionLimit,
					Details:  fmt.Sprintf("%s position would be %s > %s", asset, newSize.String(), maxPos.String()),
				}
			}
		}
	}

	maxNotional, ok := m.cfg.MaxNotionalPerVenue[signal.Venue]
	if ok {
		currentNotional := m.state.VenueNotionals[signal.Venue]
		additionalNotional := decimal.Zero
		for _, leg := range signal.Legs {
			additionalNotional = additionalNotional.Add(leg.Price.Mul(leg.Size))
		}
		if currentNotional.Add(additionalNotional).GreaterThan(maxNotional) {
			return ValidationResult{
				Approved: false,
				Reason:   RejectNotionalLimit,
				Details:  fmt.Sprintf("venue %s notional limit exceeded", signal.Venue),
			}
		}
	}

	totalPnL := m.pnlTracker.TotalDailyPnL()
	lossCapNeg := m.cfg.DailyLossCapUSDT.Neg()
	if totalPnL.LessThanOrEqual(lossCapNeg) {
		return ValidationResult{
			Approved: false,
			Reason:   RejectDailyLoss,
			Details:  fmt.Sprintf("daily PnL %s <= -%s", totalPnL.String(), m.cfg.DailyLossCapUSDT.String()),
		}
	}

	if m.state.OpenOrderCounts.Global >= m.cfg.MaxOpenOrders.Global {
		return ValidationResult{
			Approved: false,
			Reason:   RejectGlobalOrders,
			Details:  fmt.Sprintf("global orders %d >= %d", m.state.OpenOrderCounts.Global, m.cfg.MaxOpenOrders.Global),
		}
	}

	venueOrders := m.state.OpenOrderCounts.PerVenue[signal.Venue]
	if venueOrders >= m.cfg.MaxOpenOrders.PerVenue {
		return ValidationResult{
			Approved: false,
			Reason:   RejectVenueOrders,
			Details:  fmt.Sprintf("venue %s orders %d >= %d", signal.Venue, venueOrders, m.cfg.MaxOpenOrders.PerVenue),
		}
	}

	for _, leg := range signal.Legs {
		symbolOrders := m.state.OpenOrderCounts.PerSymbol[leg.Symbol]
		if symbolOrders >= m.cfg.MaxOpenOrders.PerSymbol {
			return ValidationResult{
				Approved: false,
				Reason:   RejectSymbolOrders,
				Details:  fmt.Sprintf("symbol %s orders %d >= %d", leg.Symbol, symbolOrders, m.cfg.MaxOpenOrders.PerSymbol),
			}
		}
	}

	return ValidationResult{Approved: true}
}

func (m *Manager) OnOrderFill(order domain.Order, pnl decimal.Decimal) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pnlTracker.AddRealizedPnL(pnl)

	asset := extractAsset(order.Symbol)
	key := domain.VenueAssetKey{Venue: order.Venue, Asset: asset}

	if pos, exists := m.state.Positions[key]; exists {
		if order.Side == domain.SideBuy {
			pos.Size = pos.Size.Add(order.FilledSize)
		} else {
			pos.Size = pos.Size.Sub(order.FilledSize)
		}
		pos.UpdatedAt = time.Now()
	} else {
		size := order.FilledSize
		if order.Side == domain.SideSell {
			size = size.Neg()
		}
		m.state.Positions[key] = &domain.Position{
			Venue:          order.Venue,
			Asset:          asset,
			InstrumentType: domain.InstrumentSpot,
			Size:           size,
			EntryPrice:     order.AvgFillPrice,
			UpdatedAt:      time.Now(),
		}
	}

	notional := order.AvgFillPrice.Mul(order.FilledSize)
	m.state.VenueNotionals[order.Venue] = m.state.VenueNotionals[order.Venue].Add(notional)

	m.checkPnLLimits()
}

func (m *Manager) OnOrderStateChange(change domain.OrderStateChange) {
	m.mu.Lock()
	defer m.mu.Unlock()

	order := change.Order
	isNew := !change.PrevStatus.IsTerminal() && !change.NewStatus.IsTerminal()
	isTerminal := change.NewStatus.IsTerminal()

	if isNew && change.PrevStatus == domain.OrderStatusPendingNew {
		m.state.OpenOrderCounts.Global++
		m.state.OpenOrderCounts.PerVenue[order.Venue]++
		m.state.OpenOrderCounts.PerSymbol[order.Symbol]++
	}

	if isTerminal {
		m.state.OpenOrderCounts.Global--
		m.state.OpenOrderCounts.PerVenue[order.Venue]--
		m.state.OpenOrderCounts.PerSymbol[order.Symbol]--

		if m.state.OpenOrderCounts.Global < 0 {
			m.state.OpenOrderCounts.Global = 0
		}
		if m.state.OpenOrderCounts.PerVenue[order.Venue] < 0 {
			m.state.OpenOrderCounts.PerVenue[order.Venue] = 0
		}
		if m.state.OpenOrderCounts.PerSymbol[order.Symbol] < 0 {
			m.state.OpenOrderCounts.PerSymbol[order.Symbol] = 0
		}
	}
}

func (m *Manager) checkPnLLimits() {
	totalPnL := m.pnlTracker.TotalDailyPnL()
	lossCap := m.cfg.DailyLossCapUSDT.Neg()
	warningLevel := lossCap.Mul(decimal.NewFromInt(int64(m.cfg.WarningThresholdPct))).Div(decimal.NewFromInt(100))

	if totalPnL.LessThanOrEqual(lossCap) {
		m.state.Mode = domain.RiskModeHalted
		m.killSwitch.Activate(fmt.Sprintf("daily PnL breach: %s", totalPnL.String()))
		m.logger.Error("DAILY PNL BREACH - KILL SWITCH ACTIVATED",
			"total_pnl", totalPnL.String(),
			"cap", m.cfg.DailyLossCapUSDT.String())

		if m.onKillSwitch != nil {
			go m.onKillSwitch()
		}
	} else if totalPnL.LessThanOrEqual(warningLevel) {
		if m.state.Mode == domain.RiskModeNormal {
			m.state.Mode = domain.RiskModeWarning
			m.logger.Warn("PnL warning threshold reached",
				"total_pnl", totalPnL.String(),
				"warning_level", warningLevel.String())
		}
	}
}

func (m *Manager) RunPeriodicCheck(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.mu.Lock()
			m.checkPnLLimits()
			m.mu.Unlock()
		}
	}
}

func (m *Manager) GetState() domain.RiskState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return *m.state
}

func (m *Manager) GetMode() domain.RiskMode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.Mode
}

func (m *Manager) IsKillSwitchActive() bool {
	return m.killSwitch.IsActive()
}

func (m *Manager) ActivateKillSwitch(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Mode = domain.RiskModeHalted
	m.killSwitch.Activate(reason)
}

func (m *Manager) DeactivateKillSwitch() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Mode = domain.RiskModeNormal
	m.killSwitch.Deactivate()
}

func (m *Manager) UpdatePosition(key domain.VenueAssetKey, pos *domain.Position) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Positions[key] = pos
}

func (m *Manager) GetCheckpointState() *domain.RiskState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cp := *m.state
	cp.DailyRealizedPnL = m.pnlTracker.RealizedPnL()
	cp.DailyUnrealizedPnL = m.pnlTracker.UnrealizedPnL()
	cp.LastCheckpoint = time.Now()
	cp.KillSwitchActive = m.killSwitch.IsActive()
	cp.KillSwitchReason = m.killSwitch.Reason()
	return &cp
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
