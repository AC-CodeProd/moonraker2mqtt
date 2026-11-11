//go:build tinygo || lowmem

package bridge

import (
	"context"
	"fmt"
	"time"
)

func (a *App) mqttConnect(ctx context.Context) error {
	if c, ok := a.mqttClient.(interface{ ConnectContext(context.Context) error }); ok {
		return c.ConnectContext(ctx)
	}
	return a.mqttClient.Connect()
}
func (a *App) connectMQTT(ctx context.Context) error {
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		if err := a.mqttConnect(ctx); err == nil {
			return nil
		}
		a.logger.Info("MQTT startup waiting")
		timer.Reset(30 * time.Second)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (a *App) connectMoonraker(ctx context.Context) error {
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		if err := a.moonrakerClient.Connect(ctx); err == nil {
			return nil
		}
		a.logger.Info("Moonraker startup waiting")
		timer.Reset(30 * time.Second)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// IDs identify outages, not attempts. A loss before successful synchronous
// publication stays in the same outage, without renewing any observer deadline.
func (a *App) monitorReusable(ctx context.Context) error {
	ticker := time.NewTicker(time.Duration(a.config.Moonraker.CallInterval) * time.Second)
	defer ticker.Stop()
	var mqttID, mrID uint64
	var mqttLast, mrLast time.Time
	mqttWaiting, mqttReady, mrWaiting, mrReady := false, false, false, false
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if !a.mqttClient.IsConnected() {
				if !mqttWaiting {
					if mqttID == ^uint64(0) {
						return fmt.Errorf("MQTT recovery counter exhausted")
					}
					mqttID++
					mqttWaiting = true
					a.logger.Info("MQR1: WAIT %d", mqttID)
				}
				mqttReady = false
				if mqttLast.IsZero() || time.Since(mqttLast) >= 30*time.Second {
					mqttLast = time.Now()
					if err := a.mqttConnect(ctx); err == nil {
						a.logger.Info("MQR1: CONNECTED %d", mqttID)
					}
				}
			}
			if mqttWaiting && a.mqttClient.IsConnected() && !mqttReady {
				if a.config.MQTT.CommandsEnabled {
					topic := fmt.Sprintf("%s/commands", a.config.MQTT.TopicPrefix)
					if err := a.mqttClient.Subscribe(topic, a.moonrakerClient.HandleCommand); err == nil {
						mqttReady = true
					}
				} else {
					mqttReady = true
				}
			}
			if !a.moonrakerClient.IsConnected() {
				if !mrWaiting {
					if mrID == ^uint64(0) {
						return fmt.Errorf("Moonraker recovery counter exhausted")
					}
					mrID++
					mrWaiting = true
					a.logger.Info("MRR1: WAIT %d", mrID)
				}
				mrReady = false
				if mrLast.IsZero() || time.Since(mrLast) >= 30*time.Second {
					mrLast = time.Now()
					if err := a.moonrakerClient.Connect(ctx); err == nil {
						a.logger.Info("MRR1: CONNECTED %d", mrID)
						mrReady = true
					}
				}
			}
			if !a.mqttClient.IsConnected() || !a.moonrakerClient.IsConnected() || (mqttWaiting && !mqttReady) {
				continue
			}
			if err := a.publishStatus(ctx); err != nil {
				a.logger.Error("Failed to publish periodic status (error 1/5): %v", err)
				continue
			}
			if mqttWaiting && mqttReady {
				a.logger.Info("MQR1: PUBLISHED %d", mqttID)
				mqttWaiting = false
				mqttReady = false
			}
			if mrWaiting && mrReady {
				a.logger.Info("MRR1: PUBLISHED %d", mrID)
				mrWaiting = false
				mrReady = false
			}
		}
	}
}
