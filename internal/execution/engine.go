package execution

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"

	"github.com/crypto-trading/trading/internal/domain"
	"github.com/crypto-trading/trading/internal/eventbus"
	"github.com/crypto-trading/trading/internal/order"
	"github.com/crypto-trading/trading/internal/risk"
)

type Engine struct {
	orderMgr       *order.Manager
	riskMgr        *risk.Manager
	bus            *eventbus.EventBus
	qualityTracker *QualityTracker
	logger         *slog.Logger

	triArbFillTimeout  time.Duration
	basisArbFillTimeout time.Duration
	maxRetries         int
	retryBackoff       time.Duration
}

func NewEngine(
	orderMgr *order.Manager,
	riskMgr *risk.Manager,
	bus *eventbus.EventBus,
	triArbTimeout, basisArbTimeout time.Duration,
	maxRetries int,
	logger *slog.Logger,
) *Engine {
	return &Engine{
		orderMgr:           orderMgr,
		riskMgr:            riskMgr,
		bus:                bus,
		qualityTracker:     NewQualityTracker(1000),
		logger:             logger,
		triArbFillTimeout:  triArbTimeout,
		basisArbFillTimeout: basisArbTimeout,
		maxRetries:         maxRetries,
		retryBackoff:       50 * time.Millisecond,
	}
}

func (e *Engine) Run(ctx context.Context) {
	signalCh := e.bus.SubscribeSignal()

	e.logger.Info("execution engine started")

	for {
		select {
		case <-ctx.Done():
			e.logger.Info("execution engine stopped")
			return
		case signal, ok := <-signalCh:
			if !ok {
				return
			}
			go e.executeSignal(ctx, signal)
		}
	}
}

func (e *Engine) executeSignal(ctx context.Context, signal domain.TradeSignal) {
	result := e.riskMgr.ValidateSignal(signal)
	if !result.Approved {
		e.logger.Info("signal rejected by risk manager",
			"signal_id", signal.SignalID,
			"reason", result.Reason,
			"details", result.Details,
		)
		return
	}

	e.logger.Info("executing signal",
		"signal_id", signal.SignalID,
		"strategy", signal.Strategy,
		"venue", signal.Venue,
		"legs", len(signal.Legs),
	)

	startedAt := time.Now()

	switch signal.Strategy {
	case domain.StrategyTriArb:
		e.executeTriArb(ctx, signal, startedAt)
	case domain.StrategyBasisArb:
		e.executeBasisArb(ctx, signal, startedAt)
	}
}

func (e *Engine) executeTriArb(ctx context.Context, signal domain.TradeSignal, startedAt time.Time) {
	timeout := e.triArbFillTimeout
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var legExecutions []domain.LegExecution
	var allOrders []*domain.Order
	totalFees := decimal.Zero

	for i, leg := range signal.Legs {
		req := domain.OrderRequest{
			InternalID:     order.NewOrderID(),
			SignalID:       signal.SignalID,
			Venue:          signal.Venue,
			Symbol:         leg.Symbol,
			Side:           leg.Side,
			InstrumentType: leg.InstrumentType,
			OrderType:      leg.OrderType,
			Price:          leg.Price,
			Size:           leg.Size,
			IdempotencyKey: fmt.Sprintf("%s-leg-%d", signal.SignalID, i),
		}

		ord, err := e.submitWithRetry(execCtx, req)
		if err != nil {
			e.logger.Error("tri-arb leg failed",
				"signal_id", signal.SignalID,
				"leg", i,
				"error", err)
			e.abortCycle(ctx, allOrders)
			e.publishReport(signal, legExecutions, "aborted", startedAt, totalFees)
			return
		}

		allOrders = append(allOrders, ord)

		slippageBps := decimal.Zero
		if !leg.Price.IsZero() {
			slippageBps = ord.AvgFillPrice.Sub(leg.Price).Div(leg.Price).Mul(decimal.NewFromInt(10000))
		}

		legExec := domain.LegExecution{
			Symbol:        leg.Symbol,
			Side:          leg.Side,
			ExpectedPrice: leg.Price,
			ActualPrice:   ord.AvgFillPrice,
			ExpectedSize:  leg.Size,
			ActualSize:    ord.FilledSize,
			SlippageBps:   slippageBps,
		}
		legExecutions = append(legExecutions, legExec)

		e.qualityTracker.RecordFill(leg.Symbol, string(leg.Side), leg.Price, ord.AvgFillPrice)
	}

	e.publishReport(signal, legExecutions, "completed", startedAt, totalFees)
}

func (e *Engine) executeBasisArb(ctx context.Context, signal domain.TradeSignal, startedAt time.Time) {
	timeout := e.basisArbFillTimeout
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var legExecutions []domain.LegExecution
	var allOrders []*domain.Order
	totalFees := decimal.Zero

	for i, leg := range signal.Legs {
		req := domain.OrderRequest{
			InternalID:     order.NewOrderID(),
			SignalID:       signal.SignalID,
			Venue:          signal.Venue,
			Symbol:         leg.Symbol,
			Side:           leg.Side,
			InstrumentType: leg.InstrumentType,
			OrderType:      leg.OrderType,
			Price:          leg.Price,
			Size:           leg.Size,
			IdempotencyKey: fmt.Sprintf("%s-leg-%d", signal.SignalID, i),
		}

		ord, err := e.submitWithRetry(execCtx, req)
		if err != nil {
			e.logger.Error("basis-arb leg failed",
				"signal_id", signal.SignalID,
				"leg", i,
				"error", err)
			e.abortCycle(ctx, allOrders)
			e.publishReport(signal, legExecutions, "aborted", startedAt, totalFees)
			return
		}

		allOrders = append(allOrders, ord)

		slippageBps := decimal.Zero
		if !leg.Price.IsZero() {
			slippageBps = ord.AvgFillPrice.Sub(leg.Price).Div(leg.Price).Mul(decimal.NewFromInt(10000))
		}

		legExec := domain.LegExecution{
			Symbol:        leg.Symbol,
			Side:          leg.Side,
			ExpectedPrice: leg.Price,
			ActualPrice:   ord.AvgFillPrice,
			ExpectedSize:  leg.Size,
			ActualSize:    ord.FilledSize,
			SlippageBps:   slippageBps,
		}
		legExecutions = append(legExecutions, legExec)

		e.qualityTracker.RecordFill(leg.Symbol, string(leg.Side), leg.Price, ord.AvgFillPrice)
	}

	e.publishReport(signal, legExecutions, "completed", startedAt, totalFees)
}

func (e *Engine) submitWithRetry(ctx context.Context, req domain.OrderRequest) (*domain.Order, error) {
	var lastErr error
	for attempt := 0; attempt <= e.maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(e.retryBackoff * time.Duration(attempt)):
			}
		}

		ord, err := e.orderMgr.SubmitOrder(ctx, req)
		if err == nil {
			return ord, nil
		}

		lastErr = err
		e.logger.Warn("order submission failed, retrying",
			"attempt", attempt+1,
			"order_id", req.InternalID,
			"error", err)
	}
	return nil, fmt.Errorf("order failed after %d retries: %w", e.maxRetries+1, lastErr)
}

func (e *Engine) abortCycle(ctx context.Context, orders []*domain.Order) {
	for _, ord := range orders {
		if ord == nil || ord.Status.IsTerminal() {
			continue
		}
		if err := e.orderMgr.CancelOrder(ctx, ord.InternalID); err != nil {
			e.logger.Error("failed to cancel order during abort",
				"order_id", ord.InternalID,
				"error", err)
		}
	}
}

func (e *Engine) publishReport(
	signal domain.TradeSignal,
	legs []domain.LegExecution,
	status string,
	startedAt time.Time,
	totalFees decimal.Decimal,
) {
	realizedEdge := decimal.Zero
	totalSlippage := decimal.Zero
	for _, leg := range legs {
		totalSlippage = totalSlippage.Add(leg.SlippageBps)
	}
	if len(legs) > 0 {
		realizedEdge = signal.ExpectedEdgeBps.Sub(totalSlippage.Div(decimal.NewFromInt(int64(len(legs)))))
	}

	report := domain.ExecutionReport{
		SignalID:        signal.SignalID,
		Strategy:        signal.Strategy,
		Venue:           signal.Venue,
		Legs:            legs,
		ExpectedEdgeBps: signal.ExpectedEdgeBps,
		RealizedEdgeBps: realizedEdge,
		TotalFees:       totalFees,
		SlippageBps:     totalSlippage,
		Status:          status,
		StartedAt:       startedAt,
		CompletedAt:     time.Now(),
	}

	e.bus.PublishExecutionReport(report)

	e.logger.Info("execution report",
		"signal_id", signal.SignalID,
		"strategy", signal.Strategy,
		"status", status,
		"expected_edge_bps", signal.ExpectedEdgeBps.String(),
		"realized_edge_bps", realizedEdge.String(),
		"latency_ms", time.Since(startedAt).Milliseconds(),
	)
}

func (e *Engine) KillSwitchHandler(ctx context.Context) func() {
	return func() {
		e.logger.Error("KILL SWITCH: cancelling all orders")
		e.orderMgr.CancelAllOrders(ctx)
	}
}

