package config

import (
	"time"

	"github.com/shopspring/decimal"
)

type Config struct {
	System      SystemConfig                `mapstructure:"system" validate:"required"`
	Venues      map[string]VenueConfig      `mapstructure:"venues" validate:"required,dive"`
	Strategies  StrategiesConfig            `mapstructure:"strategies" validate:"required"`
	Risk        RiskConfig                  `mapstructure:"risk" validate:"required"`
	CostModel   CostModelConfig             `mapstructure:"cost_model" validate:"required"`
	Monitoring  MonitoringConfig            `mapstructure:"monitoring" validate:"required"`
	DryRun      DryRunConfig                `mapstructure:"dry_run"`
	Persistence PersistenceConfig           `mapstructure:"persistence" validate:"required"`
	Runtime     RuntimeConfig               `mapstructure:"runtime"`
}

type SystemConfig struct {
	InstanceID              string `mapstructure:"instance_id" validate:"required"`
	TradingMode             string `mapstructure:"trading_mode" validate:"required,oneof=live dry_run backtest"`
	RequireLiveConfirmation bool   `mapstructure:"require_live_confirmation"`
	LogLevel                string `mapstructure:"log_level" validate:"required,oneof=DEBUG INFO WARN ERROR FATAL"`
	Timezone                string `mapstructure:"timezone" validate:"required"`
}

type VenueConfig struct {
	Enabled    bool                          `mapstructure:"enabled"`
	WsURL      string                        `mapstructure:"ws_url" validate:"required_if=Enabled true,omitempty,url"`
	RestURL    string                        `mapstructure:"rest_url" validate:"required_if=Enabled true,omitempty,url"`
	RateLimits map[string]RateLimitConfig     `mapstructure:"rate_limits"`
	Symbols    VenueSymbolsConfig            `mapstructure:"symbols"`
}

type RateLimitConfig struct {
	Capacity        int `mapstructure:"capacity" validate:"required,gt=0"`
	RefillPerSecond int `mapstructure:"refill_per_second" validate:"required,gt=0"`
}

type VenueSymbolsConfig struct {
	Spot []string `mapstructure:"spot"`
	Perp []string `mapstructure:"perp"`
}

type StrategiesConfig struct {
	TriangularArb TriArbConfig `mapstructure:"triangular_arb"`
	BasisArb      BasisArbConfig `mapstructure:"basis_arb"`
}

type TriArbConfig struct {
	Enabled               bool `mapstructure:"enabled"`
	MinEdgeBps            int  `mapstructure:"min_edge_bps" validate:"gt=0"`
	FeeEstimateBps        int  `mapstructure:"fee_estimate_bps" validate:"gte=0"`
	SlippageBufferBps     int  `mapstructure:"slippage_buffer_bps" validate:"gte=0"`
	ExecutionRiskBufferBps int `mapstructure:"execution_risk_buffer_bps" validate:"gte=0"`
	FillTimeoutMs         int  `mapstructure:"fill_timeout_ms" validate:"gt=0"`
	MaxRetries            int  `mapstructure:"max_retries" validate:"gte=0"`
}

func (c TriArbConfig) FillTimeout() time.Duration {
	return time.Duration(c.FillTimeoutMs) * time.Millisecond
}

type BasisArbConfig struct {
	Enabled                        bool `mapstructure:"enabled"`
	MinNetEdgeBps                  int  `mapstructure:"min_net_edge_bps" validate:"gt=0"`
	FeeEstimateBps                 int  `mapstructure:"fee_estimate_bps" validate:"gte=0"`
	SlippageBufferBps              int  `mapstructure:"slippage_buffer_bps" validate:"gte=0"`
	FundingUncertaintyBufferBps    int  `mapstructure:"funding_uncertainty_buffer_bps" validate:"gte=0"`
	TransferCostAmortizationBps    int  `mapstructure:"transfer_cost_amortization_bps" validate:"gte=0"`
	FillTimeoutMs                  int  `mapstructure:"fill_timeout_ms" validate:"gt=0"`
	HoldingHorizonHours            int  `mapstructure:"holding_horizon_hours" validate:"gt=0"`
}

func (c BasisArbConfig) FillTimeout() time.Duration {
	return time.Duration(c.FillTimeoutMs) * time.Millisecond
}

type RiskConfig struct {
	MaxPosition          map[string]decimal.Decimal `mapstructure:"max_position" validate:"required"`
	MaxNotionalPerVenue  map[string]decimal.Decimal `mapstructure:"max_notional_per_venue" validate:"required"`
	DailyLossCapUSDT     decimal.Decimal            `mapstructure:"daily_loss_cap_usdt" validate:"required"`
	WarningThresholdPct  int                        `mapstructure:"warning_threshold_pct" validate:"required,gt=0,lte=100"`
	MaxOpenOrders        MaxOpenOrdersConfig        `mapstructure:"max_open_orders" validate:"required"`
	DataFreshness        DataFreshnessConfig        `mapstructure:"data_freshness" validate:"required"`
	Reconciliation       ReconciliationConfig       `mapstructure:"reconciliation" validate:"required"`
	CheckpointIntervalS  int                        `mapstructure:"checkpoint_interval_seconds" validate:"required,gt=0"`
}

func (c RiskConfig) CheckpointInterval() time.Duration {
	return time.Duration(c.CheckpointIntervalS) * time.Second
}

type MaxOpenOrdersConfig struct {
	Global    int `mapstructure:"global" validate:"required,gt=0"`
	PerVenue  int `mapstructure:"per_venue" validate:"required,gt=0"`
	PerSymbol int `mapstructure:"per_symbol" validate:"required,gt=0"`
}

type DataFreshnessConfig struct {
	WarningMs int `mapstructure:"warning_ms" validate:"required,gt=0"`
	BlockMs   int `mapstructure:"block_ms" validate:"required,gt=0"`
}

func (c DataFreshnessConfig) WarningDuration() time.Duration {
	return time.Duration(c.WarningMs) * time.Millisecond
}

func (c DataFreshnessConfig) BlockDuration() time.Duration {
	return time.Duration(c.BlockMs) * time.Millisecond
}

type ReconciliationConfig struct {
	IntervalSeconds      int     `mapstructure:"interval_seconds" validate:"required,gt=0"`
	MismatchThresholdPct float64 `mapstructure:"mismatch_threshold_pct" validate:"required,gt=0"`
}

func (c ReconciliationConfig) Interval() time.Duration {
	return time.Duration(c.IntervalSeconds) * time.Second
}

type CostModelConfig struct {
	SlippageCurveLookbackFills   int `mapstructure:"slippage_curve_lookback_fills" validate:"required,gt=0"`
	FeeTierRefreshIntervalS      int `mapstructure:"fee_tier_refresh_interval_seconds" validate:"required,gt=0"`
	FundingRateLookbackIntervals int `mapstructure:"funding_rate_lookback_intervals" validate:"required,gt=0"`
}

func (c CostModelConfig) FeeTierRefreshInterval() time.Duration {
	return time.Duration(c.FeeTierRefreshIntervalS) * time.Second
}

type MonitoringConfig struct {
	Metrics  MetricsConfig  `mapstructure:"metrics"`
	Alerting AlertingConfig `mapstructure:"alerting"`
	Logging  LoggingConfig  `mapstructure:"logging"`
}

type MetricsConfig struct {
	FlushIntervalS       int `mapstructure:"flush_interval_seconds" validate:"gt=0"`
	IngestionDelaySLAS   int `mapstructure:"ingestion_delay_sla_seconds" validate:"gt=0"`
}

type AlertingConfig struct {
	DeliveryDelaySLAS     int      `mapstructure:"delivery_delay_sla_seconds" validate:"gt=0"`
	P1AckSLAMinutes       int      `mapstructure:"p1_ack_sla_minutes" validate:"gt=0"`
	P1MitigationSLAMinutes int     `mapstructure:"p1_mitigation_sla_minutes" validate:"gt=0"`
	Channels              []string `mapstructure:"channels"`
}

type LoggingConfig struct {
	AvailabilitySLAPct     float64 `mapstructure:"availability_sla_pct"`
	AvailabilityWindowMin  int     `mapstructure:"availability_window_minutes"`
}

type DryRunConfig struct {
	InitialCapitalUSDT    decimal.Decimal `mapstructure:"initial_capital_usdt"`
	SimulatedLatencyMs    int             `mapstructure:"simulated_latency_ms"`
	RejectRatePct         float64         `mapstructure:"reject_rate_pct"`
	UseLiveSlippageModel  bool            `mapstructure:"use_live_slippage_model"`
	PersistToSeparateTable bool           `mapstructure:"persist_to_separate_table"`
}

type PersistenceConfig struct {
	CheckpointDB           string `mapstructure:"checkpoint_db" validate:"required"`
	ColdStoreDSN           string `mapstructure:"cold_store_dsn"`
	ColdStorePoolSize      int    `mapstructure:"cold_store_pool_size" validate:"gt=0"`
	TradeLogRetentionDays  int    `mapstructure:"trade_log_retention_days" validate:"gt=0"`
}

type RuntimeConfig struct {
	GoMaxProcs int    `mapstructure:"gomaxprocs"`
	GOGC       int    `mapstructure:"gogc"`
	GoMemLimit string `mapstructure:"gomemlimit"`
}
