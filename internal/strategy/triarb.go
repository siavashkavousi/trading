package strategy

import (
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/costmodel"
	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/eventbus"
)

type TriangularPath struct {
	Venue string
	Legs  [3]TriangularLeg
}

type TriangularLeg struct {
	Symbol string
	Side   domain.Side
}

type TriArbModule struct {
	mu sync.RWMutex

	paths     []TriangularPath
	books     map[string]*domain.OrderBookSnapshot
	costModel costmodel.CostModelService
	bus       *eventbus.EventBus
	logger    *slog.Logger

	minEdgeBps int64
	venue      string
}

func NewTriArbModule(
	venue string,
	paths []TriangularPath,
	costModel costmodel.CostModelService,
	bus *eventbus.EventBus,
	minEdgeBps int,
	logger *slog.Logger,
) *TriArbModule {
	return &TriArbModule{
		paths:      paths,
		books:      make(map[string]*domain.OrderBookSnapshot),
		costModel:  costModel,
		bus:        bus,
		logger:     logger,
		minEdgeBps: int64(minEdgeBps),
		venue:      venue,
	}
}

func (m *TriArbModule) OnOrderBookUpdate(snap domain.OrderBookSnapshot) {
	if snap.Venue != m.venue {
		return
	}

	m.mu.Lock()
	m.books[snap.Symbol] = &snap
	m.mu.Unlock()

	m.evaluate(snap.Symbol, snap.LocalTimestamp)
}

func (m *TriArbModule) OnFundingRateUpdate(_ domain.FundingRate) {}

func (m *TriArbModule) evaluate(updatedSymbol string, mdTimestamp time.Time) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, path := range m.paths {
		if !m.pathInvolves(path, updatedSymbol) {
			continue
		}

		if !m.allBooksAvailable(path) {
			continue
		}

		edgeBps := m.computeEdge(path)
		threshold := domain.FixedFromBps(m.minEdgeBps)

		if edgeBps.GT(threshold) {
			signal := m.buildSignal(path, edgeBps, mdTimestamp)
			if signal != nil {
				m.bus.PublishSignal(*signal)
				m.logger.Info("tri-arb signal detected",
					"venue", m.venue,
					"edge_bps", edgeBps.ToDecimal().String(),
					"signal_id", signal.SignalID.String(),
				)
			}
		}
	}
}

func (m *TriArbModule) pathInvolves(path TriangularPath, symbol string) bool {
	for _, leg := range path.Legs {
		if leg.Symbol == symbol {
			return true
		}
	}
	return false
}

func (m *TriArbModule) allBooksAvailable(path TriangularPath) bool {
	for _, leg := range path.Legs {
		if _, ok := m.books[leg.Symbol]; !ok {
			return false
		}
	}
	return true
}

func (m *TriArbModule) computeEdge(path TriangularPath) domain.FixedPrice {
	impliedRate := domain.ToFixed(decimal.NewFromInt(1))

	for _, leg := range path.Legs {
		book := m.books[leg.Symbol]
		if leg.Side == domain.SideBuy {
			ask, ok := book.BestAsk()
			if !ok {
				return 0
			}
			price := domain.ToFixed(ask.Price)
			if price == 0 {
				return 0
			}
			impliedRate = impliedRate.Div(price)
		} else {
			bid, ok := book.BestBid()
			if !ok {
				return 0
			}
			price := domain.ToFixed(bid.Price)
			impliedRate = impliedRate.Mul(price)
		}
	}

	one := domain.ToFixed(decimal.NewFromInt(1))
	if impliedRate.GT(one) {
		return impliedRate.Sub(one)
	}
	return 0
}

func (m *TriArbModule) buildSignal(path TriangularPath, edgeBps domain.FixedPrice, mdTimestamp time.Time) *domain.TradeSignal {
	legs := make([]domain.LegSpec, 3)
	minSize := decimal.NewFromInt(999999999)

	for i, leg := range path.Legs {
		book := m.books[leg.Symbol]
		var price, size decimal.Decimal

		if leg.Side == domain.SideBuy {
			ask, ok := book.BestAsk()
			if !ok {
				return nil
			}
			price = ask.Price
			size = ask.Size
		} else {
			bid, ok := book.BestBid()
			if !ok {
				return nil
			}
			price = bid.Price
			size = bid.Size
		}

		notional := price.Mul(size)
		if notional.LessThan(minSize) {
			minSize = notional
		}

		legs[i] = domain.LegSpec{
			Symbol:         leg.Symbol,
			Side:           leg.Side,
			InstrumentType: domain.InstrumentSpot,
			Price:          price,
			Size:           size,
			OrderType:      domain.OrderTypeLimit,
		}
	}

	for i := range legs {
		if legs[i].Price.IsPositive() {
			legs[i].Size = minSize.Div(legs[i].Price)
		}
	}

	costEst, err := m.costModel.EstimateCost(m.venue, legs[0].Symbol, legs[0].Side, legs[0].Size, domain.OrderTypeLimit)
	if err != nil {
		m.logger.Warn("cost estimate failed for tri-arb signal", "error", err)
		return nil
	}

	edgeDecimal := edgeBps.ToDecimal().Mul(decimal.NewFromInt(10000))
	netEdge := edgeDecimal.Sub(costEst.TotalBps)

	if netEdge.LessThanOrEqual(decimal.Zero) {
		return nil
	}

	return &domain.TradeSignal{
		SignalID:            uuid.Must(uuid.NewV7()),
		Strategy:            domain.StrategyTriArb,
		Venue:               m.venue,
		Legs:                legs,
		ExpectedEdgeBps:     netEdge,
		CostEstimate:        costEst,
		Confidence:          costEst.Confidence,
		CreatedAt:           time.Now(),
		MarketDataTimestamp: mdTimestamp,
	}
}

func DefaultTriangularPaths(venue string) []TriangularPath {
	return []TriangularPath{
		{
			Venue: venue,
			Legs: [3]TriangularLeg{
				{Symbol: "BTC/USDT", Side: domain.SideBuy},
				{Symbol: "ETH/BTC", Side: domain.SideBuy},
				{Symbol: "ETH/USDT", Side: domain.SideSell},
			},
		},
		{
			Venue: venue,
			Legs: [3]TriangularLeg{
				{Symbol: "ETH/USDT", Side: domain.SideBuy},
				{Symbol: "ETH/BTC", Side: domain.SideSell},
				{Symbol: "BTC/USDT", Side: domain.SideSell},
			},
		},
		{
			Venue: venue,
			Legs: [3]TriangularLeg{
				{Symbol: "BTC/USDT", Side: domain.SideBuy},
				{Symbol: "SOL/BTC", Side: domain.SideBuy},
				{Symbol: "SOL/USDT", Side: domain.SideSell},
			},
		},
		{
			Venue: venue,
			Legs: [3]TriangularLeg{
				{Symbol: "SOL/USDT", Side: domain.SideBuy},
				{Symbol: "SOL/BTC", Side: domain.SideSell},
				{Symbol: "BTC/USDT", Side: domain.SideSell},
			},
		},
		{
			Venue: venue,
			Legs: [3]TriangularLeg{
				{Symbol: "ETH/USDT", Side: domain.SideBuy},
				{Symbol: "SOL/ETH", Side: domain.SideBuy},
				{Symbol: "SOL/USDT", Side: domain.SideSell},
			},
		},
		{
			Venue: venue,
			Legs: [3]TriangularLeg{
				{Symbol: "SOL/USDT", Side: domain.SideBuy},
				{Symbol: "SOL/ETH", Side: domain.SideSell},
				{Symbol: "ETH/USDT", Side: domain.SideSell},
			},
		},
	}
}
