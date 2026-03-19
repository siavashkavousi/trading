package persistence

import (
	"log/slog"
	"sync"
)

type WriteType int

const (
	WriteTypeTrade WriteType = iota
	WriteTypeCycle
	WriteTypePnL
	WriteTypeRiskEvent
	WriteTypeConfigAudit
	WriteTypeRiskCheckpoint
)

type WriteRequest struct {
	Type    WriteType
	Payload interface{}
}

type AsyncWriter struct {
	writeCh       chan WriteRequest
	riskCh        chan WriteRequest
	sqliteStore   *SQLiteStore
	postgresStore *PostgresStore
	logger        *slog.Logger
	wg            sync.WaitGroup
}

func NewAsyncWriter(
	sqliteStore *SQLiteStore,
	postgresStore *PostgresStore,
	bufferSize int,
	logger *slog.Logger,
) *AsyncWriter {
	return &AsyncWriter{
		writeCh:       make(chan WriteRequest, bufferSize),
		riskCh:        make(chan WriteRequest, 100),
		sqliteStore:   sqliteStore,
		postgresStore: postgresStore,
		logger:        logger,
	}
}

func (w *AsyncWriter) Write(req WriteRequest) {
	if req.Type == WriteTypeRiskCheckpoint {
		w.riskCh <- req
		return
	}

	select {
	case w.writeCh <- req:
	default:
		w.logger.Warn("write channel full, dropping non-critical write",
			"type", req.Type)
	}
}

func (w *AsyncWriter) Run() {
	w.wg.Add(2)
	go w.processWrites()
	go w.processRiskCheckpoints()
}

func (w *AsyncWriter) processWrites() {
	defer w.wg.Done()
	for req := range w.writeCh {
		w.handleWrite(req)
	}
}

func (w *AsyncWriter) processRiskCheckpoints() {
	defer w.wg.Done()
	for req := range w.riskCh {
		w.handleWrite(req)
	}
}

func (w *AsyncWriter) handleWrite(req WriteRequest) {
	switch req.Type {
	case WriteTypeRiskCheckpoint:
		if w.sqliteStore != nil {
			if err := w.sqliteStore.WriteRiskCheckpoint(req.Payload); err != nil {
				w.logger.Error("failed to write risk checkpoint", "error", err)
			}
		}
	case WriteTypeTrade:
		if w.postgresStore != nil {
			if err := w.postgresStore.WriteTrade(req.Payload); err != nil {
				w.logger.Error("failed to write trade", "error", err)
			}
		}
	case WriteTypeCycle:
		if w.postgresStore != nil {
			if err := w.postgresStore.WriteCycle(req.Payload); err != nil {
				w.logger.Error("failed to write cycle", "error", err)
			}
		}
	case WriteTypeRiskEvent:
		if w.postgresStore != nil {
			if err := w.postgresStore.WriteRiskEvent(req.Payload); err != nil {
				w.logger.Error("failed to write risk event", "error", err)
			}
		}
	default:
		w.logger.Warn("unknown write type", "type", req.Type)
	}
}

// Stop closes the write channels and waits for all pending writes to drain.
func (w *AsyncWriter) Stop() {
	close(w.writeCh)
	close(w.riskCh)
	w.wg.Wait()
}
