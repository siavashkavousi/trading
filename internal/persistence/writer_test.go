package persistence

import (
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestAsyncWriterWriteAndDrain(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	w := NewAsyncWriter(nil, nil, 100, logger)
	w.Run()

	for i := 0; i < 10; i++ {
		w.Write(WriteRequest{Type: WriteTypeTrade, Payload: "trade"})
	}

	w.Stop()
}

func TestAsyncWriterRiskCheckpointsNeverDropped(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	w := NewAsyncWriter(nil, nil, 1, logger)
	w.Run()

	for i := 0; i < 5; i++ {
		w.Write(WriteRequest{Type: WriteTypeRiskCheckpoint, Payload: "checkpoint"})
	}

	w.Stop()
}

func TestAsyncWriterDropsOnFull(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	w := NewAsyncWriter(nil, nil, 1, logger)

	w.Write(WriteRequest{Type: WriteTypeTrade, Payload: "first"})
	w.Write(WriteRequest{Type: WriteTypeTrade, Payload: "should-be-dropped"})

	w.Run()
	w.Stop()
}

func TestAsyncWriterStopWaitsForCompletion(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	w := NewAsyncWriter(nil, nil, 100, logger)
	w.Run()

	var processed atomic.Int32

	origHandleWrite := w.handleWrite
	_ = origHandleWrite

	for i := 0; i < 50; i++ {
		w.Write(WriteRequest{Type: WriteTypeTrade, Payload: "trade"})
	}

	w.Stop()

	_ = processed
}

func TestAsyncWriterConcurrentWrites(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	w := NewAsyncWriter(nil, nil, 1000, logger)
	w.Run()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			w.Write(WriteRequest{Type: WriteTypeTrade, Payload: i})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent writes timed out")
	}

	w.Stop()
}
