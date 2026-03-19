package strategy

import (
	"context"
	"log/slog"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/eventbus"
)

type Module interface {
	OnOrderBookUpdate(snap domain.OrderBookSnapshot)
	OnFundingRateUpdate(rate domain.FundingRate)
}

type Engine struct {
	modules []Module
	bus     *eventbus.EventBus
	logger  *slog.Logger
}

func NewEngine(bus *eventbus.EventBus, logger *slog.Logger) *Engine {
	return &Engine{
		bus:    bus,
		logger: logger,
	}
}

func (e *Engine) RegisterModule(m Module) {
	e.modules = append(e.modules, m)
}

func (e *Engine) Run(ctx context.Context) {
	obCh := e.bus.SubscribeOrderBook()
	frCh := e.bus.SubscribeFundingRate()

	e.logger.Info("strategy engine started", "modules", len(e.modules))

	for {
		select {
		case <-ctx.Done():
			e.logger.Info("strategy engine stopped")
			return

		case snap, ok := <-obCh:
			if !ok {
				return
			}
			for _, m := range e.modules {
				m.OnOrderBookUpdate(snap)
			}

		case rate, ok := <-frCh:
			if !ok {
				return
			}
			for _, m := range e.modules {
				m.OnFundingRateUpdate(rate)
			}
		}
	}
}
