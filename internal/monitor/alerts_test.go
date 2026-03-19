package monitor

import (
	"log/slog"
	"os"
	"testing"
)

func TestAlertManagerFire(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	am := NewAlertManager([]string{"log"}, logger)

	am.Fire(AlertLevelP1, "test_alert", "condition met", "something broke")

	active := am.ActiveAlerts()
	if len(active) != 1 {
		t.Fatalf("expected 1 active alert, got %d", len(active))
	}
	if active[0].Name != "test_alert" {
		t.Errorf("expected alert name test_alert, got %s", active[0].Name)
	}
	if active[0].Level != AlertLevelP1 {
		t.Errorf("expected P1 level, got %s", active[0].Level)
	}
}

func TestAlertManagerAcknowledge(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	am := NewAlertManager([]string{"log"}, logger)

	am.Fire(AlertLevelP1, "alert_a", "cond", "msg")
	am.Fire(AlertLevelP2, "alert_b", "cond", "msg")

	am.AcknowledgeAlert("alert_a")

	active := am.ActiveAlerts()
	if len(active) != 1 {
		t.Fatalf("expected 1 active alert after ack, got %d", len(active))
	}
	if active[0].Name != "alert_b" {
		t.Errorf("expected alert_b to remain active, got %s", active[0].Name)
	}
}

func TestAlertManagerNoActive(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	am := NewAlertManager([]string{"log"}, logger)

	active := am.ActiveAlerts()
	if len(active) != 0 {
		t.Errorf("expected no active alerts, got %d", len(active))
	}
}

func TestAlertManagerMultipleChannels(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	am := NewAlertManager([]string{"telegram", "pagerduty", "log"}, logger)

	am.Fire(AlertLevelP1, "multi_channel", "cond", "msg")

	active := am.ActiveAlerts()
	if len(active) != 1 {
		t.Errorf("expected 1 alert, got %d", len(active))
	}
}

func TestAlertManagerAcknowledgeNonexistent(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	am := NewAlertManager([]string{"log"}, logger)

	am.Fire(AlertLevelP1, "real_alert", "cond", "msg")
	am.AcknowledgeAlert("nonexistent")

	active := am.ActiveAlerts()
	if len(active) != 1 {
		t.Errorf("expected 1 alert still active, got %d", len(active))
	}
}
