//go:build !tinygo

package host

import (
	"fmt"
	"moonraker2mqtt/bridge"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"moonraker2mqtt/mqtt"
)

func NewApp(configFile string) (*bridge.App, error) {
	cfg, err := config.LoadOrCreateConfig(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	logger := logger.New(&cfg.Logging, cfg.Environment)

	if logger == nil {
		return nil, fmt.Errorf("failed to create logger")
	}

	mqttClient := mqtt.NewPahoClient(
		cfg.MQTT.Host,
		cfg.MQTT.Port,
		cfg.MQTT.ClientID,
		cfg.MQTT.Username,
		cfg.MQTT.Password,
		cfg.MQTT.UseTLS,
		logger,
	)

	return bridge.New(cfg, mqttClient, logger), nil
}
