CREATE TABLE IF NOT EXISTS trades (
    id              UUID PRIMARY KEY,
    signal_id       UUID NOT NULL,
    strategy        VARCHAR(32) NOT NULL,
    venue           VARCHAR(32) NOT NULL,
    symbol          VARCHAR(32) NOT NULL,
    side            VARCHAR(4) NOT NULL,
    instrument_type VARCHAR(8) NOT NULL,
    price           NUMERIC(20, 8) NOT NULL,
    size            NUMERIC(20, 8) NOT NULL,
    fee             NUMERIC(20, 8) NOT NULL,
    fee_currency    VARCHAR(8) NOT NULL,
    venue_order_id  VARCHAR(64),
    venue_trade_id  VARCHAR(64),
    executed_at     TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_trades_signal_id ON trades(signal_id);
CREATE INDEX idx_trades_venue_symbol ON trades(venue, symbol);
CREATE INDEX idx_trades_executed_at ON trades(executed_at);
