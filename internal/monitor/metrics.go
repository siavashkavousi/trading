package monitor

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	MDToDecisionLatency  *prometheus.HistogramVec
	DecisionToAckLatency *prometheus.HistogramVec
	E2ETickToAckLatency  *prometheus.HistogramVec
	RealizedEdgeBps      *prometheus.HistogramVec
	ExpectedEdgeBps      *prometheus.HistogramVec
	FillSlippageBps      *prometheus.HistogramVec
	FundingPaidReceived  *prometheus.CounterVec
	RiskLimitUtilization *prometheus.GaugeVec
	RiskLimitBreach      *prometheus.CounterVec
	OrderRejectTotal     *prometheus.CounterVec
	OrderCancelTotal     *prometheus.CounterVec
	OpenOrderCount       *prometheus.GaugeVec
	PositionNetExposure  *prometheus.GaugeVec
	DailyPnLUSDT        prometheus.Gauge
	VenueWSReconnect     *prometheus.CounterVec
	VenueAPIError        *prometheus.CounterVec

	DryRunSignalsTotal      prometheus.Counter
	DryRunSimulatedFills    prometheus.Counter
	DryRunPnLUSDT           prometheus.Gauge
	DryRunEdgeRealizedBps   *prometheus.HistogramVec
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		MDToDecisionLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "md_to_decision_latency_ms",
			Help:    "Latency from market data receipt to signal decision",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10),
		}, []string{"strategy", "venue", "symbol"}),

		DecisionToAckLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "decision_to_ack_latency_ms",
			Help:    "Latency from signal decision to venue order ack",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10),
		}, []string{"strategy", "venue", "symbol"}),

		E2ETickToAckLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "e2e_tick_to_ack_latency_ms",
			Help:    "End-to-end tick to order ack latency",
			Buckets: prometheus.ExponentialBuckets(1, 2, 12),
		}, []string{"strategy", "venue", "symbol"}),

		RealizedEdgeBps: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "realized_edge_bps",
			Help:    "Realized edge in basis points",
			Buckets: prometheus.LinearBuckets(-50, 5, 30),
		}, []string{"strategy", "venue", "mode"}),

		ExpectedEdgeBps: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "expected_edge_bps",
			Help:    "Expected edge in basis points",
			Buckets: prometheus.LinearBuckets(0, 5, 20),
		}, []string{"strategy", "venue", "mode"}),

		FillSlippageBps: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "fill_slippage_bps",
			Help:    "Fill slippage in basis points",
			Buckets: prometheus.LinearBuckets(-20, 2, 20),
		}, []string{"venue", "symbol", "side"}),

		FundingPaidReceived: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "funding_paid_received_usdt",
			Help: "Funding payments paid or received",
		}, []string{"venue", "asset", "direction"}),

		RiskLimitUtilization: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "risk_limit_utilization_pct",
			Help: "Risk limit utilization percentage",
		}, []string{"limit_type", "venue", "asset"}),

		RiskLimitBreach: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "risk_limit_breach_total",
			Help: "Total risk limit breach count",
		}, []string{"limit_type"}),

		OrderRejectTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "order_reject_total",
			Help: "Total order rejections",
		}, []string{"venue", "reason"}),

		OrderCancelTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "order_cancel_total",
			Help: "Total order cancellations",
		}, []string{"venue", "reason"}),

		OpenOrderCount: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "open_order_count",
			Help: "Current open order count",
		}, []string{"venue", "symbol"}),

		PositionNetExposure: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "position_net_exposure",
			Help: "Net position exposure per asset",
		}, []string{"asset"}),

		DailyPnLUSDT: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "daily_pnl_usdt",
			Help: "Daily PnL in USDT",
		}),

		VenueWSReconnect: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "venue_ws_reconnect_total",
			Help: "Total venue WebSocket reconnections",
		}, []string{"venue"}),

		VenueAPIError: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "venue_api_error_total",
			Help: "Total venue API errors",
		}, []string{"venue", "endpoint", "error_code"}),

		DryRunSignalsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "dry_run_signals_total",
			Help: "Total signals in dry run mode",
		}),

		DryRunSimulatedFills: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "dry_run_simulated_fills_total",
			Help: "Total simulated fills in dry run mode",
		}),

		DryRunPnLUSDT: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "dry_run_pnl_usdt",
			Help: "Cumulative simulated PnL",
		}),

		DryRunEdgeRealizedBps: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "dry_run_edge_realized_bps",
			Help:    "Realized edge on dry run trades",
			Buckets: prometheus.LinearBuckets(-50, 5, 30),
		}, []string{"strategy", "venue"}),
	}

	reg.MustRegister(
		m.MDToDecisionLatency,
		m.DecisionToAckLatency,
		m.E2ETickToAckLatency,
		m.RealizedEdgeBps,
		m.ExpectedEdgeBps,
		m.FillSlippageBps,
		m.FundingPaidReceived,
		m.RiskLimitUtilization,
		m.RiskLimitBreach,
		m.OrderRejectTotal,
		m.OrderCancelTotal,
		m.OpenOrderCount,
		m.PositionNetExposure,
		m.DailyPnLUSDT,
		m.VenueWSReconnect,
		m.VenueAPIError,
		m.DryRunSignalsTotal,
		m.DryRunSimulatedFills,
		m.DryRunPnLUSDT,
		m.DryRunEdgeRealizedBps,
	)

	return m
}

func MetricsHandler() http.Handler {
	return promhttp.Handler()
}
