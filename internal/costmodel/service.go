package costmodel

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/gateway"
)

type CostModelService interface {
	EstimateCost(venue, symbol string, side domain.Side, size decimal.Decimal, orderType domain.OrderType) (domain.CostEstimate, error)
}

type Service struct {
	mu sync.RWMutex

	feeTiers      map[string]*domain.FeeTier // keyed by venue
	slippageCurves map[string]*SlippageCurve  // keyed by "venue:symbol"
	fundingRates   map[string][]domain.FundingRate // keyed by "venue:symbol"

	gateways map[string]gateway.VenueGateway
	logger   *slog.Logger

	feeTierRefreshInterval time.Duration
	fundingLookback        int
}

func NewService(
	gateways map[string]gateway.VenueGateway,
	feeTierRefresh time.Duration,
	fundingLookback int,
	logger *slog.Logger,
) *Service {
	return &Service{
		feeTiers:               make(map[string]*domain.FeeTier),
		slippageCurves:         make(map[string]*SlippageCurve),
		fundingRates:           make(map[string][]domain.FundingRate),
		gateways:               gateways,
		logger:                 logger,
		feeTierRefreshInterval: feeTierRefresh,
		fundingLookback:        fundingLookback,
	}
}

func (s *Service) EstimateCost(venue, symbol string, side domain.Side, size decimal.Decimal, orderType domain.OrderType) (domain.CostEstimate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	feeBps := s.getFeeBps(venue, orderType)
	slippageBps := s.getSlippageBps(venue, symbol, size)
	fundingBps := s.getFundingBps(venue, symbol)

	total := feeBps.Add(slippageBps)
	if fundingBps != nil {
		total = total.Add(*fundingBps)
	}

	confidence := decimal.NewFromFloat(0.8)
	if feeBps.IsZero() {
		confidence = decimal.NewFromFloat(0.5)
	}

	return domain.CostEstimate{
		FeeBps:      feeBps,
		SlippageBps: slippageBps,
		FundingBps:  fundingBps,
		TotalBps:    total,
		Confidence:  confidence,
	}, nil
}

func (s *Service) getFeeBps(venue string, orderType domain.OrderType) decimal.Decimal {
	tier, ok := s.feeTiers[venue]
	if !ok {
		return decimal.NewFromFloat(10)
	}

	if orderType == domain.OrderTypeMarket {
		return tier.TakerFeeBps
	}
	return tier.MakerFeeBps
}

func (s *Service) getSlippageBps(venue, symbol string, size decimal.Decimal) decimal.Decimal {
	key := venue + ":" + symbol
	curve, ok := s.slippageCurves[key]
	if !ok {
		curve = NewSlippageCurve()
		s.slippageCurves[key] = curve
	}
	return curve.EstimateSlippage(size)
}

func (s *Service) getFundingBps(venue, symbol string) *decimal.Decimal {
	key := venue + ":" + symbol
	rates, ok := s.fundingRates[key]
	if !ok || len(rates) == 0 {
		return nil
	}

	n := s.fundingLookback
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
		return nil
	}

	avg := sum.Div(totalWeight).Mul(decimal.NewFromInt(10000))
	return &avg
}

func (s *Service) UpdateFeeTier(venue string, tier *domain.FeeTier) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.feeTiers[venue] = tier
}

func (s *Service) AddFundingRate(venue, symbol string, rate domain.FundingRate) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := venue + ":" + symbol
	s.fundingRates[key] = append(s.fundingRates[key], rate)

	maxLen := s.fundingLookback * 2
	if len(s.fundingRates[key]) > maxLen {
		s.fundingRates[key] = s.fundingRates[key][len(s.fundingRates[key])-maxLen:]
	}
}

func (s *Service) RefreshFeeTiers(ctx context.Context) {
	for name, gw := range s.gateways {
		tier, err := gw.GetFeeTier(ctx)
		if err != nil {
			s.logger.Error("failed to refresh fee tier", "venue", name, "error", err)
			continue
		}
		s.UpdateFeeTier(name, tier)
		s.logger.Info("fee tier refreshed", "venue", name,
			"maker_bps", tier.MakerFeeBps.String(),
			"taker_bps", tier.TakerFeeBps.String())
	}
}

func (s *Service) RunFeeTierRefresher(ctx context.Context) {
	s.RefreshFeeTiers(ctx)

	ticker := time.NewTicker(s.feeTierRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.RefreshFeeTiers(ctx)
		}
	}
}
