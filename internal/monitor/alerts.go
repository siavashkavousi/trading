package monitor

import (
	"log/slog"
	"sync"
	"time"
)

type AlertLevel string

const (
	AlertLevelP1 AlertLevel = "P1"
	AlertLevelP2 AlertLevel = "P2"
)

type Alert struct {
	Level       AlertLevel
	Name        string
	Condition   string
	Message     string
	FiredAt     time.Time
	AckedAt     *time.Time
}

type AlertManager struct {
	mu       sync.RWMutex
	alerts   []Alert
	channels []string
	logger   *slog.Logger
}

func NewAlertManager(channels []string, logger *slog.Logger) *AlertManager {
	return &AlertManager{
		alerts:   make([]Alert, 0),
		channels: channels,
		logger:   logger,
	}
}

func (am *AlertManager) Fire(level AlertLevel, name, condition, message string) {
	alert := Alert{
		Level:     level,
		Name:      name,
		Condition: condition,
		Message:   message,
		FiredAt:   time.Now(),
	}

	am.mu.Lock()
	am.alerts = append(am.alerts, alert)
	am.mu.Unlock()

	am.logger.Error("ALERT FIRED",
		"level", string(level),
		"name", name,
		"condition", condition,
		"message", message,
	)

	am.dispatch(alert)
}

func (am *AlertManager) dispatch(alert Alert) {
	for _, ch := range am.channels {
		am.logger.Info("alert dispatched",
			"channel", ch,
			"level", string(alert.Level),
			"name", alert.Name,
		)
	}
}

func (am *AlertManager) ActiveAlerts() []Alert {
	am.mu.RLock()
	defer am.mu.RUnlock()

	var active []Alert
	for _, a := range am.alerts {
		if a.AckedAt == nil {
			active = append(active, a)
		}
	}
	return active
}

func (am *AlertManager) AcknowledgeAlert(name string) {
	am.mu.Lock()
	defer am.mu.Unlock()

	now := time.Now()
	for i := range am.alerts {
		if am.alerts[i].Name == name && am.alerts[i].AckedAt == nil {
			am.alerts[i].AckedAt = &now
		}
	}
}
