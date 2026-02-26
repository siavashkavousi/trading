package eventbus

import (
	"log/slog"
	"sync"

	"github.com/crypto-trading/trading/internal/domain"
)

type EventBus struct {
	mu sync.RWMutex

	orderBookSubs  []chan domain.OrderBookSnapshot
	tradeSubs      []chan domain.Trade
	fundingRateSubs []chan domain.FundingRate
	signalSubs     []chan domain.TradeSignal
	orderStateSubs []chan domain.OrderStateChange
	execReportSubs []chan domain.ExecutionReport

	bufferSize int
	logger     *slog.Logger
}

func New(bufferSize int, logger *slog.Logger) *EventBus {
	return &EventBus{
		bufferSize: bufferSize,
		logger:     logger,
	}
}

func (eb *EventBus) SubscribeOrderBook() <-chan domain.OrderBookSnapshot {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	ch := make(chan domain.OrderBookSnapshot, eb.bufferSize)
	eb.orderBookSubs = append(eb.orderBookSubs, ch)
	return ch
}

func (eb *EventBus) PublishOrderBook(snap domain.OrderBookSnapshot) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	for _, ch := range eb.orderBookSubs {
		select {
		case ch <- snap:
		default:
			eb.logger.Warn("order book subscriber channel full, dropping event",
				"venue", snap.Venue, "symbol", snap.Symbol)
		}
	}
}

func (eb *EventBus) SubscribeTrade() <-chan domain.Trade {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	ch := make(chan domain.Trade, eb.bufferSize)
	eb.tradeSubs = append(eb.tradeSubs, ch)
	return ch
}

func (eb *EventBus) PublishTrade(trade domain.Trade) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	for _, ch := range eb.tradeSubs {
		select {
		case ch <- trade:
		default:
			eb.logger.Warn("trade subscriber channel full, dropping event",
				"venue", trade.Venue, "symbol", trade.Symbol)
		}
	}
}

func (eb *EventBus) SubscribeFundingRate() <-chan domain.FundingRate {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	ch := make(chan domain.FundingRate, eb.bufferSize)
	eb.fundingRateSubs = append(eb.fundingRateSubs, ch)
	return ch
}

func (eb *EventBus) PublishFundingRate(rate domain.FundingRate) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	for _, ch := range eb.fundingRateSubs {
		select {
		case ch <- rate:
		default:
			eb.logger.Warn("funding rate subscriber channel full, dropping event",
				"venue", rate.Venue, "symbol", rate.Symbol)
		}
	}
}

func (eb *EventBus) SubscribeSignal() <-chan domain.TradeSignal {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	ch := make(chan domain.TradeSignal, eb.bufferSize)
	eb.signalSubs = append(eb.signalSubs, ch)
	return ch
}

func (eb *EventBus) PublishSignal(signal domain.TradeSignal) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	for _, ch := range eb.signalSubs {
		select {
		case ch <- signal:
		default:
			eb.logger.Warn("signal subscriber channel full, dropping event",
				"strategy", signal.Strategy, "venue", signal.Venue)
		}
	}
}

func (eb *EventBus) SubscribeOrderState() <-chan domain.OrderStateChange {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	ch := make(chan domain.OrderStateChange, eb.bufferSize)
	eb.orderStateSubs = append(eb.orderStateSubs, ch)
	return ch
}

func (eb *EventBus) PublishOrderState(change domain.OrderStateChange) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	for _, ch := range eb.orderStateSubs {
		select {
		case ch <- change:
		default:
			eb.logger.Warn("order state subscriber channel full, dropping event",
				"order_id", change.Order.InternalID)
		}
	}
}

func (eb *EventBus) SubscribeExecutionReport() <-chan domain.ExecutionReport {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	ch := make(chan domain.ExecutionReport, eb.bufferSize)
	eb.execReportSubs = append(eb.execReportSubs, ch)
	return ch
}

func (eb *EventBus) PublishExecutionReport(report domain.ExecutionReport) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	for _, ch := range eb.execReportSubs {
		select {
		case ch <- report:
		default:
			eb.logger.Warn("execution report subscriber channel full, dropping event",
				"signal_id", report.SignalID)
		}
	}
}

func (eb *EventBus) Close() {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	for _, ch := range eb.orderBookSubs {
		close(ch)
	}
	for _, ch := range eb.tradeSubs {
		close(ch)
	}
	for _, ch := range eb.fundingRateSubs {
		close(ch)
	}
	for _, ch := range eb.signalSubs {
		close(ch)
	}
	for _, ch := range eb.orderStateSubs {
		close(ch)
	}
	for _, ch := range eb.execReportSubs {
		close(ch)
	}
}
