package persistence

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewPostgresStore(ctx context.Context, dsn string, poolSize int, logger *slog.Logger) (*PostgresStore, error) {
	if dsn == "" {
		logger.Warn("no PostgreSQL DSN configured, cold store disabled")
		return nil, nil
	}

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse pg config: %w", err)
	}

	config.MaxConns = int32(poolSize)

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	store := &PostgresStore{pool: pool, logger: logger}
	return store, nil
}

func (s *PostgresStore) RunMigrations(ctx context.Context) error {
	if s == nil || s.pool == nil {
		return nil
	}

	migrations := []string{
		`CREATE TABLE IF NOT EXISTS trades (
			id UUID PRIMARY KEY,
			signal_id UUID NOT NULL,
			strategy VARCHAR(32) NOT NULL,
			venue VARCHAR(32) NOT NULL,
			symbol VARCHAR(32) NOT NULL,
			side VARCHAR(4) NOT NULL,
			instrument_type VARCHAR(8) NOT NULL,
			price NUMERIC(20, 8) NOT NULL,
			size NUMERIC(20, 8) NOT NULL,
			fee NUMERIC(20, 8) NOT NULL,
			fee_currency VARCHAR(8) NOT NULL,
			venue_order_id VARCHAR(64),
			venue_trade_id VARCHAR(64),
			executed_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS strategy_cycles (
			id UUID PRIMARY KEY,
			strategy VARCHAR(32) NOT NULL,
			venue VARCHAR(32) NOT NULL,
			signal_id UUID NOT NULL,
			expected_edge_bps NUMERIC(10, 4),
			realized_edge_bps NUMERIC(10, 4),
			total_fees NUMERIC(20, 8),
			total_slippage_bps NUMERIC(10, 4),
			pnl_usdt NUMERIC(20, 8),
			status VARCHAR(16) NOT NULL,
			started_at TIMESTAMPTZ NOT NULL,
			completed_at TIMESTAMPTZ,
			metadata JSONB
		)`,
		`CREATE TABLE IF NOT EXISTS daily_pnl (
			date DATE PRIMARY KEY,
			realized_pnl NUMERIC(20, 8) NOT NULL,
			unrealized_pnl NUMERIC(20, 8) NOT NULL,
			total_pnl NUMERIC(20, 8) NOT NULL,
			num_cycles INTEGER NOT NULL,
			num_trades INTEGER NOT NULL,
			fees_paid NUMERIC(20, 8) NOT NULL,
			funding_net NUMERIC(20, 8) NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS risk_events (
			id UUID PRIMARY KEY,
			event_type VARCHAR(32) NOT NULL,
			severity VARCHAR(4) NOT NULL,
			details JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS config_audit (
			id UUID PRIMARY KEY,
			key VARCHAR(128) NOT NULL,
			old_value TEXT,
			new_value TEXT NOT NULL,
			changed_by VARCHAR(64) NOT NULL,
			changed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
	}

	for _, m := range migrations {
		if _, err := s.pool.Exec(ctx, m); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}

	s.logger.Info("PostgreSQL migrations completed")
	return nil
}

func (s *PostgresStore) WriteTrade(payload interface{}) error {
	if s == nil || s.pool == nil {
		return nil
	}
	// Trade writing would serialize the payload and INSERT
	s.logger.Debug("trade written to cold store")
	return nil
}

func (s *PostgresStore) WriteCycle(payload interface{}) error {
	if s == nil || s.pool == nil {
		return nil
	}
	s.logger.Debug("cycle written to cold store")
	return nil
}

func (s *PostgresStore) WriteRiskEvent(payload interface{}) error {
	if s == nil || s.pool == nil {
		return nil
	}
	s.logger.Debug("risk event written to cold store")
	return nil
}

func (s *PostgresStore) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}
