CREATE TABLE IF NOT EXISTS daily_pnl (
    date            DATE PRIMARY KEY,
    realized_pnl    NUMERIC(20, 8) NOT NULL,
    unrealized_pnl  NUMERIC(20, 8) NOT NULL,
    total_pnl       NUMERIC(20, 8) NOT NULL,
    num_cycles      INTEGER NOT NULL,
    num_trades      INTEGER NOT NULL,
    fees_paid       NUMERIC(20, 8) NOT NULL,
    funding_net     NUMERIC(20, 8) NOT NULL
);
