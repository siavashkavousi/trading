CREATE TABLE IF NOT EXISTS strategy_cycles (
    id                  UUID PRIMARY KEY,
    strategy            VARCHAR(32) NOT NULL,
    venue               VARCHAR(32) NOT NULL,
    signal_id           UUID NOT NULL,
    expected_edge_bps   NUMERIC(10, 4),
    realized_edge_bps   NUMERIC(10, 4),
    total_fees          NUMERIC(20, 8),
    total_slippage_bps  NUMERIC(10, 4),
    pnl_usdt            NUMERIC(20, 8),
    status              VARCHAR(16) NOT NULL,
    started_at          TIMESTAMPTZ NOT NULL,
    completed_at        TIMESTAMPTZ,
    metadata            JSONB
);

CREATE INDEX idx_strategy_cycles_signal_id ON strategy_cycles(signal_id);
CREATE INDEX idx_strategy_cycles_strategy ON strategy_cycles(strategy);
CREATE INDEX idx_strategy_cycles_started_at ON strategy_cycles(started_at);
