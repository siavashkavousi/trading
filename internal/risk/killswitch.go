package risk

import (
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"time"
)

type KillSwitch struct {
	mu       sync.RWMutex
	active   bool
	reason   string
	activatedAt time.Time
	filePath string
	logger   *slog.Logger
}

type killSwitchState struct {
	Active      bool      `json:"active"`
	Reason      string    `json:"reason"`
	ActivatedAt time.Time `json:"activated_at"`
}

func NewKillSwitch(filePath string, logger *slog.Logger) *KillSwitch {
	ks := &KillSwitch{
		filePath: filePath,
		logger:   logger,
	}
	ks.loadState()
	return ks
}

func (ks *KillSwitch) loadState() {
	data, err := os.ReadFile(ks.filePath)
	if err != nil {
		return
	}

	var state killSwitchState
	if err := json.Unmarshal(data, &state); err != nil {
		ks.logger.Warn("failed to parse kill switch state", "error", err)
		return
	}

	ks.active = state.Active
	ks.reason = state.Reason
	ks.activatedAt = state.ActivatedAt

	if ks.active {
		ks.logger.Warn("kill switch is ACTIVE from previous session",
			"reason", ks.reason,
			"activated_at", ks.activatedAt)
	}
}

func (ks *KillSwitch) persistState() {
	state := killSwitchState{
		Active:      ks.active,
		Reason:      ks.reason,
		ActivatedAt: ks.activatedAt,
	}

	data, err := json.Marshal(state)
	if err != nil {
		ks.logger.Error("failed to marshal kill switch state", "error", err)
		return
	}

	if err := os.WriteFile(ks.filePath, data, 0644); err != nil {
		ks.logger.Error("failed to persist kill switch state", "error", err)
	}
}

func (ks *KillSwitch) Activate(reason string) {
	ks.mu.Lock()
	defer ks.mu.Unlock()

	ks.active = true
	ks.reason = reason
	ks.activatedAt = time.Now()
	ks.persistState()

	ks.logger.Error("KILL SWITCH ACTIVATED",
		"reason", reason,
		"activated_at", ks.activatedAt)
}

func (ks *KillSwitch) Deactivate() {
	ks.mu.Lock()
	defer ks.mu.Unlock()

	ks.active = false
	ks.reason = ""
	ks.persistState()

	ks.logger.Warn("KILL SWITCH DEACTIVATED")
}

func (ks *KillSwitch) IsActive() bool {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return ks.active
}

func (ks *KillSwitch) Reason() string {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return ks.reason
}
