package websocket

import (
	"context"
	"math"
	"moonraker2mqtt/logger"
	"time"
)

func NewRetry(enabled bool, maxAttempts int, logger logger.Logger) *Retry {
	return &Retry{
		enabled:      enabled,
		maxAttempts:  maxAttempts,
		currentDelay: time.Duration(INITIAL_RETRY_DELAY) * time.Second,
		attempt:      0,
		logger:       logger,
	}
}

func (m *Retry) ShouldReconnect() bool {
	if !m.enabled {
		return false
	}

	if m.maxAttempts > 0 && m.attempt >= m.maxAttempts {
		m.logger.Info("Max reconnection attempts (%d) reached", m.maxAttempts)
		return false
	}

	return true
}

func (m *Retry) WaitBeforeReconnect(ctx context.Context) error {
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

func (m *Retry) Reset() {
	m.attempt = 0
	m.currentDelay = time.Duration(INITIAL_RETRY_DELAY) * time.Second
	if m.enabled {
		m.logger.Info("Reconnection manager reset - connection successful")
	}
}

func (m *Retry) GetAttempt() int {
	return m.attempt
}

func (m *Retry) IsEnabled() bool {
	return m.enabled
}
