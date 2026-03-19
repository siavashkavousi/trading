package dashboard

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/domain"
)

type pageData struct {
	Page   string
	System systemInfo
}

type overviewData struct {
	pageData
	PnL       pnlInfo
	Risk      riskInfo
	Orders    ordersInfo
	Venues    []venueInfo
	Positions []positionInfo
}

type positionsData struct {
	pageData
	Positions []positionInfo
	Balances  []balanceInfo
}

type ordersData struct {
	pageData
	Active []orderInfo
}

type riskData struct {
	pageData
	Risk           riskInfo
	PnL            pnlInfo
	PositionLimits []limitInfo
	NotionalLimits []limitInfo
	OrderLimits    orderLimitsInfo
}

type systemInfo struct {
	InstanceID  string
	TradingMode string
	Uptime      string
	RiskMode    string
}

type pnlInfo struct {
	Realized   string
	Unrealized string
	Total      string
	LossCap    string
	IsNegative bool
	UtilPct    float64
}

type riskInfo struct {
	Mode             string
	KillSwitchActive bool
	KillSwitchReason string
}

type ordersInfo struct {
	ActiveCount int
	ByVenue     []venueOrderCount
}

type venueOrderCount struct {
	Venue string
	Count int
}

type venueInfo struct {
	Name       string
	FreshFeeds int
	StaleFeeds int
	TotalFeeds int
}

type positionInfo struct {
	Venue         string
	Asset         string
	Instrument    string
	Size          string
	EntryPrice    string
	UnrealizedPnL string
	IsLong        bool
}

type balanceInfo struct {
	Venue  string
	Asset  string
	Free   string
	Locked string
	Total  string
}

type orderInfo struct {
	ID        string
	Venue     string
	Symbol    string
	Side      string
	Type      string
	Price     string
	Size      string
	Filled    string
	Status    string
	CreatedAt string
	IsBuy     bool
}

type limitInfo struct {
	Name    string
	Current string
	Max     string
	Pct     float64
}

type orderLimitsInfo struct {
	Global    limitInfo
	PerVenue  []limitInfo
	PerSymbol []limitInfo
}

func (s *Server) getSystemInfo() systemInfo {
	return systemInfo{
		InstanceID:  s.cfg.System.InstanceID,
		TradingMode: s.cfg.System.TradingMode,
		Uptime:      formatDuration(time.Since(s.startTime)),
		RiskMode:    string(s.riskMgr.GetMode()),
	}
}

func (s *Server) getPnLInfo() pnlInfo {
	state := s.riskMgr.GetCheckpointState()
	total := state.DailyRealizedPnL.Add(state.DailyUnrealizedPnL)
	lossCap := s.cfg.Risk.DailyLossCapUSDT

	utilPct := 0.0
	if lossCap.IsPositive() {
		absTotal := total.Abs()
		if total.IsNegative() {
			utilPct = absTotal.Div(lossCap).InexactFloat64() * 100
		}
	}

	return pnlInfo{
		Realized:   formatUSDT(state.DailyRealizedPnL),
		Unrealized: formatUSDT(state.DailyUnrealizedPnL),
		Total:      formatUSDT(total),
		LossCap:    formatUSDT(lossCap),
		IsNegative: total.IsNegative(),
		UtilPct:    math.Min(utilPct, 100),
	}
}

func (s *Server) getRiskInfo() riskInfo {
	state := s.riskMgr.GetCheckpointState()
	return riskInfo{
		Mode:             string(state.Mode),
		KillSwitchActive: state.KillSwitchActive,
		KillSwitchReason: state.KillSwitchReason,
	}
}

func (s *Server) getOrdersInfo() ordersInfo {
	active := s.orderMgr.GetActiveOrders()
	byVenue := make(map[string]int)
	for _, o := range active {
		byVenue[o.Venue]++
	}

	counts := make([]venueOrderCount, 0, len(byVenue))
	for v, c := range byVenue {
		counts = append(counts, venueOrderCount{Venue: v, Count: c})
	}

	return ordersInfo{
		ActiveCount: len(active),
		ByVenue:     counts,
	}
}

func (s *Server) getVenuesInfo() []venueInfo {
	feedStatus := s.mdService.GetFeedStatus()
	venueFeeds := make(map[string]*venueInfo)

	for key, lastUpdate := range feedStatus {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		venueName := parts[0]

		vi, ok := venueFeeds[venueName]
		if !ok {
			vi = &venueInfo{Name: venueName}
			venueFeeds[venueName] = vi
		}
		vi.TotalFeeds++
		if time.Since(lastUpdate) < s.mdService.StaleDuration() {
			vi.FreshFeeds++
		} else {
			vi.StaleFeeds++
		}
	}

	for venueName := range s.cfg.Venues {
		if _, ok := venueFeeds[venueName]; !ok {
			venueFeeds[venueName] = &venueInfo{Name: venueName}
		}
	}

	result := make([]venueInfo, 0, len(venueFeeds))
	for _, vi := range venueFeeds {
		result = append(result, *vi)
	}
	return result
}

func (s *Server) getPositionsInfo() []positionInfo {
	allPos := s.portfolioMgr.GetAllPositions()
	result := make([]positionInfo, 0, len(allPos))
	for _, pos := range allPos {
		if pos.Size.IsZero() {
			continue
		}
		result = append(result, positionInfo{
			Venue:         pos.Venue,
			Asset:         pos.Asset,
			Instrument:    string(pos.InstrumentType),
			Size:          pos.Size.StringFixed(8),
			EntryPrice:    formatUSDT(pos.EntryPrice),
			UnrealizedPnL: formatUSDT(pos.UnrealizedPnL),
			IsLong:        pos.Size.IsPositive(),
		})
	}
	return result
}

func (s *Server) getBalancesInfo() []balanceInfo {
	allBal := s.portfolioMgr.GetAllBalances()
	result := make([]balanceInfo, 0, len(allBal))
	for _, bal := range allBal {
		result = append(result, balanceInfo{
			Venue:  bal.Venue,
			Asset:  bal.Asset,
			Free:   bal.Free.StringFixed(8),
			Locked: bal.Locked.StringFixed(8),
			Total:  bal.Total.StringFixed(8),
		})
	}
	return result
}

func (s *Server) getActiveOrdersInfo() []orderInfo {
	active := s.orderMgr.GetActiveOrders()
	result := make([]orderInfo, 0, len(active))
	for _, o := range active {
		result = append(result, orderInfo{
			ID:        o.InternalID.String()[:8],
			Venue:     o.Venue,
			Symbol:    o.Symbol,
			Side:      string(o.Side),
			Type:      string(o.OrderType),
			Price:     formatUSDT(o.Price),
			Size:      o.Size.StringFixed(8),
			Filled:    o.FilledSize.StringFixed(8),
			Status:    string(o.Status),
			CreatedAt: o.CreatedAt.Format("15:04:05"),
			IsBuy:     o.Side == domain.SideBuy,
		})
	}
	return result
}

func (s *Server) getPositionLimits() []limitInfo {
	state := s.riskMgr.GetState()
	var limits []limitInfo

	for asset, maxPos := range s.cfg.Risk.MaxPosition {
		current := decimal.Zero
		for key, pos := range state.Positions {
			if key.Asset == asset {
				current = current.Add(pos.Size.Abs())
			}
		}
		pct := 0.0
		if maxPos.IsPositive() {
			pct = current.Div(maxPos).InexactFloat64() * 100
		}
		limits = append(limits, limitInfo{
			Name:    asset,
			Current: current.StringFixed(4),
			Max:     maxPos.StringFixed(4),
			Pct:     math.Min(pct, 100),
		})
	}
	return limits
}

func (s *Server) getNotionalLimits() []limitInfo {
	state := s.riskMgr.GetState()
	var limits []limitInfo

	for venue, maxNotional := range s.cfg.Risk.MaxNotionalPerVenue {
		current := state.VenueNotionals[venue]
		pct := 0.0
		if maxNotional.IsPositive() {
			pct = current.Div(maxNotional).InexactFloat64() * 100
		}
		limits = append(limits, limitInfo{
			Name:    venue,
			Current: formatUSDT(current),
			Max:     formatUSDT(maxNotional),
			Pct:     math.Min(pct, 100),
		})
	}
	return limits
}

func (s *Server) getOrderLimits() orderLimitsInfo {
	state := s.riskMgr.GetState()
	maxOrders := s.cfg.Risk.MaxOpenOrders

	globalPct := 0.0
	if maxOrders.Global > 0 {
		globalPct = float64(state.OpenOrderCounts.Global) / float64(maxOrders.Global) * 100
	}

	perVenue := make([]limitInfo, 0)
	for venue, count := range state.OpenOrderCounts.PerVenue {
		pct := 0.0
		if maxOrders.PerVenue > 0 {
			pct = float64(count) / float64(maxOrders.PerVenue) * 100
		}
		perVenue = append(perVenue, limitInfo{
			Name:    venue,
			Current: fmt.Sprintf("%d", count),
			Max:     fmt.Sprintf("%d", maxOrders.PerVenue),
			Pct:     math.Min(pct, 100),
		})
	}

	perSymbol := make([]limitInfo, 0)
	for symbol, count := range state.OpenOrderCounts.PerSymbol {
		if count == 0 {
			continue
		}
		pct := 0.0
		if maxOrders.PerSymbol > 0 {
			pct = float64(count) / float64(maxOrders.PerSymbol) * 100
		}
		perSymbol = append(perSymbol, limitInfo{
			Name:    symbol,
			Current: fmt.Sprintf("%d", count),
			Max:     fmt.Sprintf("%d", maxOrders.PerSymbol),
			Pct:     math.Min(pct, 100),
		})
	}

	return orderLimitsInfo{
		Global: limitInfo{
			Name:    "Global",
			Current: fmt.Sprintf("%d", state.OpenOrderCounts.Global),
			Max:     fmt.Sprintf("%d", maxOrders.Global),
			Pct:     math.Min(globalPct, 100),
		},
		PerVenue:  perVenue,
		PerSymbol: perSymbol,
	}
}

// --- Page Handlers ---

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.render(w, "overview", s.buildOverviewData())
}

func (s *Server) handlePositions(w http.ResponseWriter, r *http.Request) {
	s.render(w, "positions", s.buildPositionsData())
}

func (s *Server) handleOrders(w http.ResponseWriter, r *http.Request) {
	s.render(w, "orders", s.buildOrdersData())
}

func (s *Server) handleRisk(w http.ResponseWriter, r *http.Request) {
	s.render(w, "risk", s.buildRiskData())
}

// --- Fragment Handlers (HTMX partial updates) ---

func (s *Server) handleOverviewFragment(w http.ResponseWriter, r *http.Request) {
	s.renderFragment(w, "overview", "overview-data", s.buildOverviewData())
}

func (s *Server) handlePositionsFragment(w http.ResponseWriter, r *http.Request) {
	s.renderFragment(w, "positions", "positions-data", s.buildPositionsData())
}

func (s *Server) handleOrdersFragment(w http.ResponseWriter, r *http.Request) {
	s.renderFragment(w, "orders", "orders-data", s.buildOrdersData())
}

func (s *Server) handleRiskFragment(w http.ResponseWriter, r *http.Request) {
	s.renderFragment(w, "risk", "risk-data", s.buildRiskData())
}

// --- Kill Switch Handlers ---

func (s *Server) handleKillSwitchActivate(w http.ResponseWriter, r *http.Request) {
	reason := r.FormValue("reason")
	if reason == "" {
		reason = "manual activation via dashboard"
	}
	s.riskMgr.ActivateKillSwitch(reason)
	s.logger.Warn("kill switch activated via dashboard", "reason", reason)

	s.renderFragment(w, "overview", "overview-data", s.buildOverviewData())
}

func (s *Server) handleKillSwitchDeactivate(w http.ResponseWriter, r *http.Request) {
	s.riskMgr.DeactivateKillSwitch()
	s.logger.Info("kill switch deactivated via dashboard")

	s.renderFragment(w, "overview", "overview-data", s.buildOverviewData())
}

// --- Data Builders ---

func (s *Server) buildOverviewData() overviewData {
	return overviewData{
		pageData: pageData{
			Page:   "overview",
			System: s.getSystemInfo(),
		},
		PnL:       s.getPnLInfo(),
		Risk:      s.getRiskInfo(),
		Orders:    s.getOrdersInfo(),
		Venues:    s.getVenuesInfo(),
		Positions: s.getPositionsInfo(),
	}
}

func (s *Server) buildPositionsData() positionsData {
	return positionsData{
		pageData: pageData{
			Page:   "positions",
			System: s.getSystemInfo(),
		},
		Positions: s.getPositionsInfo(),
		Balances:  s.getBalancesInfo(),
	}
}

func (s *Server) buildOrdersData() ordersData {
	return ordersData{
		pageData: pageData{
			Page:   "orders",
			System: s.getSystemInfo(),
		},
		Active: s.getActiveOrdersInfo(),
	}
}

func (s *Server) buildRiskData() riskData {
	return riskData{
		pageData: pageData{
			Page:   "risk",
			System: s.getSystemInfo(),
		},
		Risk:           s.getRiskInfo(),
		PnL:            s.getPnLInfo(),
		PositionLimits: s.getPositionLimits(),
		NotionalLimits: s.getNotionalLimits(),
		OrderLimits:    s.getOrderLimits(),
	}
}

// --- Formatting Helpers ---

func formatUSDT(d decimal.Decimal) string {
	return d.StringFixed(2)
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}
