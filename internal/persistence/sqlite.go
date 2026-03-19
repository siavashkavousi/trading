package persistence

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db     *sql.DB
	logger *slog.Logger
}

func NewSQLiteStore(dbPath string, logger *slog.Logger) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)

	store := &SQLiteStore{db: db, logger: logger}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate sqlite: %w", err)
	}

	return store, nil
}

func (s *SQLiteStore) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS risk_checkpoints (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			state_json TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS recent_trades (
			id TEXT PRIMARY KEY,
			signal_id TEXT NOT NULL,
			venue TEXT NOT NULL,
			symbol TEXT NOT NULL,
			side TEXT NOT NULL,
			price TEXT NOT NULL,
			size TEXT NOT NULL,
			fee TEXT NOT NULL,
			executed_at TIMESTAMP NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS order_log (
			id TEXT PRIMARY KEY,
			signal_id TEXT NOT NULL,
			venue TEXT NOT NULL,
			symbol TEXT NOT NULL,
			side TEXT NOT NULL,
			order_type TEXT NOT NULL,
			price TEXT NOT NULL,
			size TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
	}

	for _, m := range migrations {
		if _, err := s.db.Exec(m); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) WriteRiskCheckpoint(payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal risk state: %w", err)
	}

	_, err = s.db.Exec(
		"INSERT INTO risk_checkpoints (state_json) VALUES (?)",
		string(data),
	)
	return err
}

func (s *SQLiteStore) LoadLatestCheckpoint() ([]byte, error) {
	var data string
	err := s.db.QueryRow(
		"SELECT state_json FROM risk_checkpoints ORDER BY id DESC LIMIT 1",
	).Scan(&data)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return []byte(data), nil
}

func (s *SQLiteStore) CleanupOldCheckpoints(maxAge time.Duration) error {
	cutoff := time.Now().Add(-maxAge)
	_, err := s.db.Exec(
		"DELETE FROM risk_checkpoints WHERE created_at < ?",
		cutoff,
	)
	return err
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
