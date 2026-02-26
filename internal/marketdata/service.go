package marketdata

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/eventbus"
)

type Service struct {
	mu    sync.RWMutex
	books map[string]*domain.OrderBookSnapshot // key: "venue:symbol"

	tradeBuffers map[string]*TradeRingBuffer // key: "venue:symbol"
	fundingRates map[string]*domain.FundingRate

	lastUpdate   map[string]time.Time // key: "venue:symbol"

	bus    *eventbus.EventBus
	logger *slog.Logger

	staleDuration time.Duration
	blockDuration time.Duration
	heartbeatInterval time.Duration
}

func NewService(
	bus *eventbus.EventBus,
	staleDuration, blockDuration time.Duration,
	logger *slog.Logger,
) *Service {
	return &Service{
		books:             make(map[string]*domain.OrderBookSnapshot),
		tradeBuffers:      make(map[string]*TradeRingBuffer),
		fundingRates:      make(map[string]*domain.FundingRate),
		lastUpdate:        make(map[string]time.Time),
		bus:               bus,
		logger:            logger,
		staleDuration:     staleDuration,
		blockDuration:     blockDuration,
		heartbeatInterval: 500 * time.Millisecond,
	}
}

func bookKey(venue, symbol string) string {
	return venue + ":" + symbol
}

func (s *Service) UpdateOrderBook(snap domain.OrderBookSnapshot) {
	key := bookKey(snap.Venue, snap.Symbol)
	snap.LocalTimestamp = time.Now()

	s.mu.Lock()
	s.books[key] = &snap
	s.lastUpdate[key] = snap.LocalTimestamp
	s.mu.Unlock()

	s.bus.PublishOrderBook(snap)
}

func (s *Service) ApplyDelta(delta domain.OrderBookDelta) {
	key := bookKey(delta.Venue, delta.Symbol)
	now := time.Now()

	s.mu.Lock()
	book, exists := s.books[key]
	if !exists {
		book = &domain.OrderBookSnapshot{
			Venue:  delta.Venue,
			Symbol: delta.Symbol,
			Bids:   make([]domain.PriceLevel, 0, 20),
			Asks:   make([]domain.PriceLevel, 0, 20),
		}
		s.books[key] = book
	}

	book.Bids = applyLevelDeltas(book.Bids, delta.Bids, true)
	book.Asks = applyLevelDeltas(book.Asks, delta.Asks, false)
	book.Sequence = delta.Sequence
	book.VenueTimestamp = delta.VenueTimestamp
	book.LocalTimestamp = now
	s.lastUpdate[key] = now
	snap := *book
	s.mu.Unlock()

	s.bus.PublishOrderBook(snap)
}

func applyLevelDeltas(levels []domain.PriceLevel, deltas []domain.PriceLevel, descending bool) []domain.PriceLevel {
	for _, d := range deltas {
		found := false
		for i, l := range levels {
			if l.Price.Equal(d.Price) {
				if d.Size.IsZero() {
					levels = append(levels[:i], levels[i+1:]...)
				} else {
					levels[i].Size = d.Size
				}
				found = true
				break
			}
		}
		if !found && !d.Size.IsZero() {
			levels = append(levels, d)
		}
	}

	sortLevels(levels, descending)
	return levels
}

func sortLevels(levels []domain.PriceLevel, descending bool) {
	n := len(levels)
	for i := 1; i < n; i++ {
		for j := i; j > 0; j-- {
			swap := false
			if descending {
				swap = levels[j].Price.GreaterThan(levels[j-1].Price)
			} else {
				swap = levels[j].Price.LessThan(levels[j-1].Price)
			}
			if swap {
				levels[j], levels[j-1] = levels[j-1], levels[j]
			} else {
				break
			}
		}
	}
}

func (s *Service) RecordTrade(trade domain.Trade) {
	key := bookKey(trade.Venue, trade.Symbol)

	s.mu.Lock()
	buf, exists := s.tradeBuffers[key]
	if !exists {
		buf = NewTradeRingBuffer(1000)
		s.tradeBuffers[key] = buf
	}
	s.mu.Unlock()

	buf.Push(&trade)
	s.bus.PublishTrade(trade)
}

func (s *Service) UpdateFundingRate(rate domain.FundingRate) {
	key := bookKey(rate.Venue, rate.Symbol)

	s.mu.Lock()
	s.fundingRates[key] = &rate
	s.mu.Unlock()

	s.bus.PublishFundingRate(rate)
}

func (s *Service) GetOrderBook(venue, symbol string) (*domain.OrderBookSnapshot, bool) {
	key := bookKey(venue, symbol)
	s.mu.RLock()
	defer s.mu.RUnlock()
	book, ok := s.books[key]
	if !ok {
		return nil, false
	}
	snap := *book
	return &snap, true
}

func (s *Service) GetFundingRate(venue, symbol string) (*domain.FundingRate, bool) {
	key := bookKey(venue, symbol)
	s.mu.RLock()
	defer s.mu.RUnlock()
	rate, ok := s.fundingRates[key]
	if !ok {
		return nil, false
	}
	r := *rate
	return &r, true
}

func (s *Service) GetRecentTrades(venue, symbol string, n int) []*domain.Trade {
	key := bookKey(venue, symbol)
	s.mu.RLock()
	buf, exists := s.tradeBuffers[key]
	s.mu.RUnlock()
	if !exists {
		return nil
	}
	return buf.Recent(n)
}

func (s *Service) IsDataFresh(venue, symbol string) bool {
	key := bookKey(venue, symbol)
	s.mu.RLock()
	t, ok := s.lastUpdate[key]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	return time.Since(t) < s.staleDuration
}

func (s *Service) IsDataBlocked(venue, symbol string) bool {
	key := bookKey(venue, symbol)
	s.mu.RLock()
	t, ok := s.lastUpdate[key]
	s.mu.RUnlock()
	if !ok {
		return true
	}
	return time.Since(t) > s.blockDuration
}

func (s *Service) DataAge(venue, symbol string) time.Duration {
	key := bookKey(venue, symbol)
	s.mu.RLock()
	t, ok := s.lastUpdate[key]
	s.mu.RUnlock()
	if !ok {
		return time.Duration(1<<63 - 1)
	}
	return time.Since(t)
}

func (s *Service) RunHeartbeatMonitor(ctx context.Context) {
	ticker := time.NewTicker(s.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkStaleness()
		}
	}
}

func (s *Service) checkStaleness() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	for key, t := range s.lastUpdate {
		age := now.Sub(t)
		if age > s.blockDuration {
			s.logger.Warn("market data blocked: exceeds block threshold",
				"feed", key, "age_ms", age.Milliseconds())
		} else if age > s.staleDuration {
			s.logger.Warn("market data stale: exceeds warning threshold",
				"feed", key, "age_ms", age.Milliseconds())
		}
	}
}
