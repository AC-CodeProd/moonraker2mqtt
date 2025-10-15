package retry

import (
	"context"
	"math"
	"time"

	"moonraker2mqtt/logger"
)

const (
	INITIAL_RETRY_DELAY      = 1
	MAX_RETRY_DELAY          = 60
	RETRY_BACKOFF_MULTIPLIER = 2
)

type Manager struct {
	enabled      bool
	maxAttempts  int
	currentDelay time.Duration
	attempt      int
	logger       logger.Logger
}

func NewManager(enabled bool, maxAttempts int, logger logger.Logger) *Manager {
	return &Manager{
		enabled:      enabled,
		maxAttempts:  maxAttempts,
		currentDelay: time.Duration(INITIAL_RETRY_DELAY) * time.Second,
		attempt:      0,
		logger:       logger,
	}
}

func (m *Manager) ShouldReconnect() bool {
	if !m.enabled {
		return false
	}

	if m.maxAttempts > 0 && m.attempt >= m.maxAttempts {
		m.logger.Info("Max reconnection attempts (%d) reached", m.maxAttempts)
		return false
	}

	return true
}

func (m *Manager) WaitBeforeReconnect(ctx context.Context) error {
	if m.attempt == 0 {
		m.attempt++
		return nil
	}

	m.logger.Warn("Waiting %v before reconnection attempt %d", m.currentDelay, m.attempt+1)

	timer := time.NewTimer(m.currentDelay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		m.currentDelay = time.Duration(
			math.Min(
				float64(m.currentDelay)*RETRY_BACKOFF_MULTIPLIER,
				float64(MAX_RETRY_DELAY)*float64(time.Second),
			),
		)
		m.attempt++
		return nil
	}
}

func (m *Manager) Reset() {
	m.attempt = 0
	m.currentDelay = time.Duration(INITIAL_RETRY_DELAY) * time.Second
	if m.enabled {
		m.logger.Info("Reconnection manager reset - connection successful")
	}
}

func (m *Manager) GetAttempt() int {
	return m.attempt
}

func (m *Manager) IsEnabled() bool {
	return m.enabled
}
