package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"moonraker2mqtt/moonraker"
	"moonraker2mqtt/mqtt"
	"moonraker2mqtt/version"
	"moonraker2mqtt/websocket"
	"time"
)

// moonrakerAdapter is the protocol boundary used by the bridge and local tests.
type moonrakerAdapter interface {
	Connect(context.Context) error
	Disconnect() error
	IsConnected() bool
	HandleCommand(string, []byte)
	GetServerInfo(context.Context) (*websocket.ServerInfo, error)
	GetHostInfo(context.Context) (*websocket.PrinterInfo, error)
	GetKlippyState(context.Context) (string, error)
	QueryObjects(context.Context, map[string]any) (map[string]any, error)
}

type App struct {
	config          *config.Config
	moonrakerClient moonrakerAdapter
	mqttClient      mqtt.MQTTClient
	logger          logger.Logger
}

// New assembles the shared bridge with platform-provided configuration and adapters.
func New(cfg *config.Config, client mqtt.MQTTClient, log logger.Logger) *App {
	app := &App{config: cfg, mqttClient: client, logger: log}
	app.moonrakerClient = moonraker.NewClient(&cfg.Moonraker, log, app)
	return app
}

// Configuration returns a value copy of the active platform configuration.
func (a *App) Configuration() config.Config { return *a.config }

func (a *App) OnStateChanged(state string) {
	a.logger.Debug("Moonraker state changed: %s", state)

	if a.mqttClient.IsConnected() {
		topic := fmt.Sprintf("%s/state", a.config.MQTT.TopicPrefix)
		payload := []byte(state)
		if err := a.mqttClient.Publish(topic, payload, a.config.MQTT.QoS, a.config.MQTT.Retain, 3); err != nil {
			a.logger.Error("Failed to publish state to MQTT after retries: %v", err)
		}
	} else {
		a.logger.Warn("Cannot publish state change - MQTT not connected")
	}
}

func (a *App) OnNotification(method string, params any) {
	a.logger.Debug("Received notification: %s", method)

	if a.mqttClient.IsConnected() {
		topic := fmt.Sprintf("%s/notifications/%s", a.config.MQTT.TopicPrefix, method)

		data, err := json.Marshal(params)
		if err != nil {
			a.logger.Error("Failed to marshal notification params: %v", err)
			return
		}

		if err := a.mqttClient.Publish(topic, data, a.config.MQTT.QoS, a.config.MQTT.Retain, 3); err != nil {
			a.logger.Error("Failed to publish notification to MQTT after retries: %v", err)
		}
	} else {
		a.logger.Warn("Cannot publish notification '%s' - MQTT not connected", method)
	}
}

func (a *App) OnException(err error) {
	a.logger.Error("Moonraker exception: %v", err)
}

func (a *App) Run(ctx context.Context) error {
	a.logger.Info("Starting Moonraker2MQTT")
	a.logger.Info("Version: %s, Git Commit: %s, Build Date: %s", version.Version, version.GitCommit, version.BuildDate)

	if err := a.mqttClient.Connect(); err != nil {
		return fmt.Errorf("failed to connect to MQTT broker: %w", err)
	}
	defer func() {
		if err := a.mqttClient.Disconnect(); err != nil {
			a.logger.Error("Failed to disconnect from MQTT broker: %v", err)
		}
	}()

	if err := a.moonrakerClient.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to Moonraker: %w", err)
	}
	defer func() {
		if err := a.moonrakerClient.Disconnect(); err != nil {
			a.logger.Error("Failed to disconnect from Moonraker: %v", err)
		}
	}()

	a.logger.Info("Successfully connected to both Moonraker and MQTT")

	if a.config.MQTT.CommandsEnabled {
		commandTopic := fmt.Sprintf("%s/%s", a.config.MQTT.TopicPrefix, "commands")
		if err := a.mqttClient.Subscribe(commandTopic, a.moonrakerClient.HandleCommand); err != nil {
			a.logger.Warn("Failed to subscribe to command topic %s: %v", commandTopic, err)
		} else {
			a.logger.Info("Subscribed to command topic: %s", commandTopic)
		}
	}

	maxRetries := 3
	for retries := 0; retries < maxRetries; retries++ {
		if err := a.publishInitialInfo(ctx); err != nil {
			a.logger.Warn("Failed to publish initial info (attempt %d/%d): %v", retries+1, maxRetries, err)
			if retries == maxRetries-1 {
				a.logger.Error("Failed to publish initial info after %d attempts, continuing anyway", maxRetries)
			} else {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Second * time.Duration(retries+1)):
				}
			}
		} else {
			a.logger.Info("Successfully published initial info")
			break
		}
	}

	if endSessionOnDisconnect {
		// Return through both deferred Disconnect calls before supervision
		// can reset; do not leave Run blocked on a still-healthy Wi-Fi ctx.
		err := a.periodicMonitoring(ctx)
		a.logger.Info("Shutting down...")
		return err
	}

	// Preserve hosted reconnection and context-driven shutdown behavior.
	go a.periodicMonitoring(ctx)
	<-ctx.Done()
	a.logger.Info("Shutting down...")
	return nil
}

func (a *App) publishInitialInfo(ctx context.Context) error {
	serverInfo, err := a.moonrakerClient.GetServerInfo(ctx)
	if err != nil {
		return fmt.Errorf("failed to get server info: %w", err)
	}

	data, err := json.Marshal(serverInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal server info: %w", err)
	}

	topic := fmt.Sprintf("%s/server/info", a.config.MQTT.TopicPrefix)
	if err := a.mqttClient.Publish(topic, data, a.config.MQTT.QoS, a.config.MQTT.Retain, 3); err != nil {
		return fmt.Errorf("failed to publish server info: %w", err)
	}

	printerInfo, err := a.moonrakerClient.GetHostInfo(ctx)
	if err != nil {
		return fmt.Errorf("failed to get printer info: %w", err)
	}

	data, err = json.Marshal(printerInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal printer info: %w", err)
	}

	topic = fmt.Sprintf("%s/printer/info", a.config.MQTT.TopicPrefix)
	if err := a.mqttClient.Publish(topic, data, a.config.MQTT.QoS, a.config.MQTT.Retain, 3); err != nil {
		return fmt.Errorf("failed to publish printer info: %w", err)
	}

	return nil
}

func (a *App) periodicMonitoring(ctx context.Context) error {
	ticker := time.NewTicker(time.Duration(a.config.Moonraker.CallInterval) * time.Second)
	defer func() { ticker.Stop() }()

	consecutiveErrors := 0
	maxConsecutiveErrors := 5
	lastReconnectAttempt := time.Time{}
	reconnectCooldown := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			mqttConnected := a.mqttClient.IsConnected()
			moonrakerConnected := a.moonrakerClient.IsConnected()

			// Reconnecting allocates fresh goroutine stacks. On the MCU, end
			// this session before either Connect can touch a fragmented heap.
			if endSessionOnDisconnect {
				if !mqttConnected {
					return fmt.Errorf("MQTT disconnected; session recovery required")
				}
				if !moonrakerConnected {
					return fmt.Errorf("Moonraker disconnected; session recovery required")
				}
			}

			if !moonrakerConnected && time.Since(lastReconnectAttempt) > reconnectCooldown {
				a.logger.Warn("Moonraker disconnected, attempting reconnection...")
				lastReconnectAttempt = time.Now()

				if err := a.moonrakerClient.Connect(ctx); err != nil {
					a.logger.Error("Failed to reconnect Moonraker: %v", err)
					consecutiveErrors++
					continue
				} else {
					a.logger.Info("Moonraker reconnected successfully")
				}
			}

			if !mqttConnected && time.Since(lastReconnectAttempt) > reconnectCooldown {
				a.logger.Warn("MQTT disconnected, attempting reconnection...")
				lastReconnectAttempt = time.Now()

				if err := a.mqttClient.Connect(); err != nil {
					a.logger.Error("Failed to reconnect MQTT: %v", err)
					consecutiveErrors++
					continue
				} else {
					a.logger.Info("MQTT reconnected successfully")
					if a.config.MQTT.CommandsEnabled {
						commandTopic := fmt.Sprintf("%s/%s", a.config.MQTT.TopicPrefix, "commands")
						if err := a.mqttClient.Subscribe(commandTopic, a.moonrakerClient.HandleCommand); err != nil {
							a.logger.Warn("Failed to re-subscribe to command topic %s after reconnection: %v", commandTopic, err)
						} else {
							a.logger.Info("Re-subscribed to command topic: %s", commandTopic)
						}
					}
				}
			}

			if !mqttConnected || !moonrakerConnected {
				consecutiveErrors++
				if consecutiveErrors <= 5 {
					a.logger.Warn("Skipping status publication - MQTT=%t, Moonraker=%t", mqttConnected, moonrakerConnected)
				}
				continue
			}

			if err := a.publishStatus(ctx); err != nil {
				consecutiveErrors++
				a.logger.Error("Failed to publish periodic status (error %d/%d): %v", consecutiveErrors, maxConsecutiveErrors, err)

				if consecutiveErrors >= maxConsecutiveErrors {
					a.logger.Warn("Too many consecutive errors, slowing down polling interval")
					ticker.Stop()
					ticker = time.NewTicker(time.Duration(a.config.Moonraker.CallInterval*2) * time.Second)
				}
			} else {
				if consecutiveErrors > 0 {
					a.logger.Info("Successfully published status after %d errors, resuming normal polling", consecutiveErrors)
					consecutiveErrors = 0
					ticker.Stop()
					ticker = time.NewTicker(time.Duration(a.config.Moonraker.CallInterval) * time.Second)
				}
			}
		}
	}
}

func (a *App) publishStatus(ctx context.Context) error {
	klippyState, err := a.moonrakerClient.GetKlippyState(ctx)
	if err != nil {
		return fmt.Errorf("failed to get klipper state: %w", err)
	}

	topic := fmt.Sprintf("%s/klipper/state", a.config.MQTT.TopicPrefix)
	if err := a.mqttClient.Publish(topic, []byte(klippyState), a.config.MQTT.QoS, false, 3); err != nil {
		return fmt.Errorf("failed to publish klipper state: %w", err)
	}

	objects, err := a.config.Moonraker.GetMonitoredObjects()
	if err != nil {
		a.logger.Warn("Failed to get monitored objects from config, using defaults: %v", err)
		objects = map[string]any{
			"print_stats": nil,
			"toolhead":    []string{"position"},
			"extruder":    []string{"temperature", "target"},
			"heater_bed":  []string{"temperature", "target"},
		}
	}

	result, err := a.moonrakerClient.QueryObjects(ctx, objects)
	if err != nil {
		return fmt.Errorf("failed to query objects: %w", err)
	}

	errorCount := 0
	totalObjects := len(result)
	for objectName, objectData := range result {
		if objectName == "eventtime" {
			continue
		}

		data, err := json.Marshal(objectData)
		if err != nil {
			a.logger.Error("Failed to marshal object %s: %v", objectName, err)
			errorCount++
			continue
		}

		topic := fmt.Sprintf("%s/objects/%s", a.config.MQTT.TopicPrefix, objectName)
		if err := a.mqttClient.Publish(topic, data, a.config.MQTT.QoS, false, 3); err != nil {
			a.logger.Error("Failed to publish object %s after retries: %v", objectName, err)
			errorCount++
		}
	}

	if errorCount > 0 {
		a.logger.Warn("Published objects with %d/%d errors", errorCount, totalObjects)
		if errorCount >= totalObjects/2 {
			return fmt.Errorf("too many object publication failures (%d/%d)", errorCount, totalObjects)
		}
	}

	return nil
}
