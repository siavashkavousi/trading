package strategy

import (
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/costmodel"
	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/eventbus"
)

type BasisArbModule struct {
	mu sync.RWMutex

	spotBooks     map[string]*domain.OrderBookSnapshot // "venue:symbol" → spot book
	perpBooks     map[string]*domain.OrderBookSnapshot // "venue:symbol" → perp book
	fundingRates  map[string][]domain.FundingRate      // "venue:symbol" → recent funding rates

	costModel costmodel.CostModelService
	bus       *eventbus.EventBus
	logger    *slog.Logger

	minNetEdgeBps     int
	holdingHorizonH   int
	venues            []string
	assets            []string
	spotSymbolMap     map[string]string // asset → spot symbol
	perpSymbolMap     map[string]string // asset → perp symbol
}

func NewBasisArbModule(
	venues []string,
	assets []string,
	costModel costmodel.CostModelService,
	bus *eventbus.EventBus,
	minNetEdgeBps int,
	holdingHorizonH int,
	logger *slog.Logger,
) *BasisArbModule {
	spotMap := make(map[string]string, len(assets))
	perpMap := make(map[string]string, len(assets))
	for _, asset := range assets {
		spotMap[asset] = asset + "/USDT"
		perpMap[asset] = asset + "USDT"
	}

	return &BasisArbModule{
		spotBooks:       make(map[string]*domain.OrderBookSnapshot),
		perpBooks:       make(map[string]*domain.OrderBookSnapshot),
		fundingRates:    make(map[string][]domain.FundingRate),
		costModel:       costModel,
		bus:             bus,
		logger:          logger,
		minNetEdgeBps:   minNetEdgeBps,
		holdingHorizonH: holdingHorizonH,
		venues:          venues,
		assets:          assets,
		spotSymbolMap:   spotMap,
		perpSymbolMap:   perpMap,
	}
}

func (m *BasisArbModule) OnOrderBookUpdate(snap domain.OrderBookSnapshot) {
	m.mu.Lock()
	key := snap.Venue + ":" + snap.Symbol
	isSpot := false
	for _, sym := range m.spotSymbolMap {
		if sym == snap.Symbol {
			isSpot = true
			break
		}
	}
	if isSpot {
		m.spotBooks[key] = &snap
	} else {
		m.perpBooks[key] = &snap
	}
	m.mu.Unlock()

	m.evaluate(snap.Venue, snap.LocalTimestamp)
}

func (m *BasisArbModule) OnFundingRateUpdate(rate domain.FundingRate) {
	m.mu.Lock()
	key := rate.Venue + ":" + rate.Symbol
	m.fundingRates[key] = append(m.fundingRates[key], rate)
	if len(m.fundingRates[key]) > 100 {
		m.fundingRates[key] = m.fundingRates[key][len(m.fundingRates[key])-100:]
	}
	m.mu.Unlock()
}

func (m *BasisArbModule) evaluate(venue string, mdTimestamp time.Time) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, asset := range m.assets {
		spotSymbol := m.spotSymbolMap[asset]
		perpSymbol := m.perpSymbolMap[asset]

		spotKey := venue + ":" + spotSymbol
		perpKey := venue + ":" + perpSymbol

		spotBook, spotOK := m.spotBooks[spotKey]
		perpBook, perpOK := m.perpBooks[perpKey]
		if !spotOK || !perpOK {
			continue
		}

		spotMid, spotValid := spotBook.MidPrice()
		perpMid, perpValid := perpBook.MidPrice()
		if !spotValid || !perpValid {
			continue
		}

		if spotMid.IsZero() {
			continue
		}

		basis := perpMid.Sub(spotMid).Div(spotMid)
		holdingDays := decimal.NewFromInt(int64(m.holdingHorizonH)).Div(decimal.NewFromInt(24))
		if holdingDays.IsZero() {
			continue
		}

		annualizedBasis := basis.Mul(decimal.NewFromInt(365)).Div(holdingDays)
		_ = annualizedBasis

		fundingCapture := m.estimateFundingCapture(venue, perpSymbol)
		regime := m.classifyFundingRegime(venue, perpSymbol)

		totalEdgeBps := basis.Abs().Add(fundingCapture.Abs()).Mul(decimal.NewFromInt(10000))

		costEst, err := m.costModel.EstimateCost(venue, spotSymbol, domain.SideBuy, decimal.NewFromFloat(1), domain.OrderTypeLimit)
		if err != nil {
			continue
		}

		netEdgeBps := totalEdgeBps.Sub(costEst.TotalBps)
		minEdge := decimal.NewFromInt(int64(m.minNetEdgeBps))

		if netEdgeBps.GreaterThanOrEqual(minEdge) {
			var spotSide, perpSide domain.Side
			if perpMid.GreaterThan(spotMid) {
				spotSide = domain.SideBuy
				perpSide = domain.SideSell
			} else {
				spotSide = domain.SideSell
				perpSide = domain.SideBuy
			}

			spotAsk, _ := spotBook.BestAsk()
			perpBid, _ := perpBook.BestBid()

			size := decimal.Min(spotAsk.Size, perpBid.Size)
			if size.IsZero() {
				continue
			}

			signal := domain.TradeSignal{
				SignalID:  uuid.Must(uuid.NewV7()),
				Strategy:  domain.StrategyBasisArb,
				Venue:     venue,
				Legs: []domain.LegSpec{
					{
						Symbol:         spotSymbol,
						Side:           spotSide,
						InstrumentType: domain.InstrumentSpot,
						Price:          spotAsk.Price,
						Size:           size,
						OrderType:      domain.OrderTypeLimit,
					},
					{
						Symbol:         perpSymbol,
						Side:           perpSide,
						InstrumentType: domain.InstrumentPerp,
						Price:          perpBid.Price,
						Size:           size,
						OrderType:      domain.OrderTypeLimit,
					},
				},
				ExpectedEdgeBps:     netEdgeBps,
				CostEstimate:        costEst,
				Confidence:          costEst.Confidence,
				CreatedAt:           time.Now(),
				MarketDataTimestamp: mdTimestamp,
			}

			m.bus.PublishSignal(signal)
			m.logger.Info("basis-arb signal detected",
				"venue", venue,
				"asset", asset,
				"net_edge_bps", netEdgeBps.String(),
				"regime", string(regime),
				"signal_id", signal.SignalID.String(),
			)
		}
	}
}

func (m *BasisArbModule) estimateFundingCapture(venue, symbol string) decimal.Decimal {
	key := venue + ":" + symbol
	rates, ok := m.fundingRates[key]
	if !ok || len(rates) == 0 {
		return decimal.Zero
	}

	n := 12
	if n > len(rates) {
		n = len(rates)
	}

	sum := decimal.Zero
	totalWeight := decimal.Zero
	for i := len(rates) - n; i < len(rates); i++ {
		weight := decimal.NewFromInt(int64(i - (len(rates) - n) + 1))
		sum = sum.Add(rates[i].Rate.Mul(weight))
		totalWeight = totalWeight.Add(weight)
	}

	if totalWeight.IsZero() {
		return decimal.Zero
	}

	avgRate := sum.Div(totalWeight)
	intervals := decimal.NewFromInt(int64(m.holdingHorizonH)).Div(decimal.NewFromInt(8))
	return avgRate.Mul(intervals)
}

func (m *BasisArbModule) classifyFundingRegime(venue, symbol string) domain.FundingRegime {
	key := venue + ":" + symbol
	rates, ok := m.fundingRates[key]
	if !ok || len(rates) < 3 {
		return domain.FundingRegimeVolatile
	}

	n := 12
	if n > len(rates) {
		n = len(rates)
	}

	recentRates := rates[len(rates)-n:]

	mean := decimal.Zero
	for _, r := range recentRates {
		mean = mean.Add(r.Rate)
	}
	mean = mean.Div(decimal.NewFromInt(int64(len(recentRates))))

	variance := decimal.Zero
	for _, r := range recentRates {
		diff := r.Rate.Sub(mean)
		variance = variance.Add(diff.Mul(diff))
	}
	variance = variance.Div(decimal.NewFromInt(int64(len(recentRates))))

	stdDev := decimal.NewFromFloat(math.Sqrt(variance.InexactFloat64()))

	threshold := decimal.NewFromFloat(0.0001)
	if stdDev.LessThan(threshold) {
		return domain.FundingRegimeStable
	}
	return domain.FundingRegimeVolatile
}
