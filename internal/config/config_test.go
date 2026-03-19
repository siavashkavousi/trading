package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadValidConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	cfgContent := `
system:
  instance_id: "test-001"
  trading_mode: "dry_run"
  log_level: "INFO"
  timezone: "UTC"
  require_live_confirmation: true

venues:
  nobitex:
    enabled: true
    ws_url: "wss://example.com/ws"
    rest_url: "https://example.com/api"
    symbols:
      spot: ["BTC/USDT"]

strategies:
  triangular_arb:
    enabled: true
    min_edge_bps: 10
    fee_estimate_bps: 5
    slippage_buffer_bps: 3
    execution_risk_buffer_bps: 2
    fill_timeout_ms: 5000
    max_retries: 3
  basis_arb:
    enabled: false
    min_net_edge_bps: 15
    fee_estimate_bps: 5
    slippage_buffer_bps: 3
    funding_uncertainty_buffer_bps: 2
    transfer_cost_amortization_bps: 1
    fill_timeout_ms: 10000
    holding_horizon_hours: 24

risk:
  max_position:
    BTC: 1
    ETH: 10
    SOL: 100
  max_notional_per_venue:
    nobitex: 50000
  daily_loss_cap_usdt: 500
  warning_threshold_pct: 80
  max_open_orders:
    global: 20
    per_venue: 10
    per_symbol: 5
  data_freshness:
    warning_ms: 3000
    block_ms: 5000
  reconciliation:
    interval_seconds: 60
    mismatch_threshold_pct: 1.0
  checkpoint_interval_seconds: 30

cost_model:
  slippage_curve_lookback_fills: 100
  fee_tier_refresh_interval_seconds: 300
  funding_rate_lookback_intervals: 12

monitoring:
  metrics:
    flush_interval_seconds: 10
    ingestion_delay_sla_seconds: 5
  alerting:
    delivery_delay_sla_seconds: 30
    p1_ack_sla_minutes: 5
    p1_mitigation_sla_minutes: 30
    channels: ["log"]
  logging:
    availability_sla_pct: 99.9
    availability_window_minutes: 60

persistence:
  checkpoint_db: "./data/checkpoints.db"
  cold_store_pool_size: 5
  trade_log_retention_days: 30

dry_run:
  initial_capital_usdt: 100000
  simulated_latency_ms: 50
`

	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error loading config: %v", err)
	}

	if cfg.System.InstanceID != "test-001" {
		t.Errorf("expected instance_id test-001, got %s", cfg.System.InstanceID)
	}
	if cfg.System.TradingMode != "dry_run" {
		t.Errorf("expected trading_mode dry_run, got %s", cfg.System.TradingMode)
	}
	if !cfg.Strategies.TriangularArb.Enabled {
		t.Error("expected triangular arb to be enabled")
	}
	if cfg.Strategies.BasisArb.Enabled {
		t.Error("expected basis arb to be disabled")
	}
}

func TestLoadInvalidPath(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	if err == nil {
		t.Error("expected error for nonexistent config path")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bad.yaml")

	if err := os.WriteFile(cfgPath, []byte("{{invalid yaml"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := Load(cfgPath)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoadValidationFailure(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "incomplete.yaml")

	cfgContent := `
system:
  instance_id: ""
  trading_mode: "invalid_mode"
`

	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	_, err := Load(cfgPath)
	if err == nil {
		t.Error("expected validation error for incomplete config")
	}
}

func TestTriArbConfigFillTimeout(t *testing.T) {
	cfg := TriArbConfig{FillTimeoutMs: 5000}
	if cfg.FillTimeout() != 5*time.Second {
		t.Errorf("expected 5s, got %v", cfg.FillTimeout())
	}
}

func TestBasisArbConfigFillTimeout(t *testing.T) {
	cfg := BasisArbConfig{FillTimeoutMs: 10000}
	if cfg.FillTimeout() != 10*time.Second {
		t.Errorf("expected 10s, got %v", cfg.FillTimeout())
	}
}

func TestDataFreshnessDurations(t *testing.T) {
	cfg := DataFreshnessConfig{WarningMs: 3000, BlockMs: 5000}
	if cfg.WarningDuration() != 3*time.Second {
		t.Errorf("expected 3s warning, got %v", cfg.WarningDuration())
	}
	if cfg.BlockDuration() != 5*time.Second {
		t.Errorf("expected 5s block, got %v", cfg.BlockDuration())
	}
}

func TestRiskConfigCheckpointInterval(t *testing.T) {
	cfg := RiskConfig{CheckpointIntervalS: 30}
	if cfg.CheckpointInterval() != 30*time.Second {
		t.Errorf("expected 30s, got %v", cfg.CheckpointInterval())
	}
}

func TestReconciliationInterval(t *testing.T) {
	cfg := ReconciliationConfig{IntervalSeconds: 60}
	if cfg.Interval() != time.Minute {
		t.Errorf("expected 1m, got %v", cfg.Interval())
	}
}

func TestCostModelFeeTierRefreshInterval(t *testing.T) {
	cfg := CostModelConfig{FeeTierRefreshIntervalS: 300}
	if cfg.FeeTierRefreshInterval() != 5*time.Minute {
		t.Errorf("expected 5m, got %v", cfg.FeeTierRefreshInterval())
	}
}

func TestGetReturnsStoredConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	cfgContent := `
system:
  instance_id: "get-test"
  trading_mode: "dry_run"
  log_level: "INFO"
  timezone: "UTC"

venues:
  nobitex:
    enabled: true
    ws_url: "wss://example.com/ws"
    rest_url: "https://example.com/api"

strategies:
  triangular_arb:
    min_edge_bps: 10
    fee_estimate_bps: 5
    slippage_buffer_bps: 3
    execution_risk_buffer_bps: 2
    fill_timeout_ms: 5000
    max_retries: 3
  basis_arb:
    min_net_edge_bps: 15
    fee_estimate_bps: 5
    slippage_buffer_bps: 3
    funding_uncertainty_buffer_bps: 2
    transfer_cost_amortization_bps: 1
    fill_timeout_ms: 10000
    holding_horizon_hours: 24

risk:
  max_position:
    BTC: 1
  max_notional_per_venue:
    nobitex: 50000
  daily_loss_cap_usdt: 500
  warning_threshold_pct: 80
  max_open_orders:
    global: 20
    per_venue: 10
    per_symbol: 5
  data_freshness:
    warning_ms: 3000
    block_ms: 5000
  reconciliation:
    interval_seconds: 60
    mismatch_threshold_pct: 1.0
  checkpoint_interval_seconds: 30

cost_model:
  slippage_curve_lookback_fills: 100
  fee_tier_refresh_interval_seconds: 300
  funding_rate_lookback_intervals: 12

monitoring:
  metrics:
    flush_interval_seconds: 10
    ingestion_delay_sla_seconds: 5
  alerting:
    delivery_delay_sla_seconds: 30
    p1_ack_sla_minutes: 5
    p1_mitigation_sla_minutes: 30
    channels: ["log"]
  logging:
    availability_sla_pct: 99.9
    availability_window_minutes: 60

persistence:
  checkpoint_db: "./data/checkpoints.db"
  cold_store_pool_size: 5
  trade_log_retention_days: 30

dry_run:
  initial_capital_usdt: 100000
  simulated_latency_ms: 50
`

	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	_, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := Get()
	if got == nil {
		t.Fatal("expected non-nil config from Get()")
	}
	if got.System.InstanceID != "get-test" {
		t.Errorf("expected instance_id get-test, got %s", got.System.InstanceID)
	}
}
