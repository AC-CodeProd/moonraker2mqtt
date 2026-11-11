package esp32s3

import (
	"fmt"
	"moonraker2mqtt/config"
	"strconv"
)

// Link-time configuration: no secrets are stored in tracked defaults.
// All variables are strings so TinyGo -ldflags -X can set them.
var (
	WiFiSSID        string
	WiFiPassword    string
	MoonrakerHost   string
	MoonrakerPort   = "7125"
	MoonrakerAPIKey string
	MQTTHost        string
	MQTTPort        = "1883"
	MQTTUsername    string
	MQTTPassword    string
	MQTTClientID    = "moonraker2mqtt-esp32"
	MQTTTopicPrefix = "moonraker"
	CommandsEnabled = "false"
	// QualificationReadOnly inhibits commands and all settings writes, including
	// pending boot operations. It is an image flag, never a stored-setting edit.
	QualificationReadOnly = "false"
	CallInterval          = "5"
	LogLevel              = "info"
)

func applyQualificationSafety(cfg *config.Config) error {
	switch QualificationReadOnly {
	case "false":
		return nil
	case "true":
		cfg.MQTT.CommandsEnabled = false
		return nil
	default:
		return fmt.Errorf("invalid qualification safety mode")
	}
}

func Config() (*config.Config, error) {
	if WiFiSSID == "" {
		return nil, fmt.Errorf("WiFiSSID must be supplied at build time")
	}
	cfg := config.DefaultConfig()
	cfg.Environment = "production"
	cfg.Moonraker.Host = MoonrakerHost
	cfg.Moonraker.APIKey = MoonrakerAPIKey
	cfg.MQTT.Host = MQTTHost
	cfg.MQTT.Username = MQTTUsername
	cfg.MQTT.Password = MQTTPassword
	cfg.MQTT.ClientID = MQTTClientID
	cfg.MQTT.TopicPrefix = MQTTTopicPrefix
	cfg.Logging.Level = LogLevel
	var err error
	cfg.Moonraker.Port, err = strconv.Atoi(MoonrakerPort)
	if err != nil {
		return nil, fmt.Errorf("MoonrakerPort: %w", err)
	}
	cfg.MQTT.Port, err = strconv.Atoi(MQTTPort)
	if err != nil {
		return nil, fmt.Errorf("MQTTPort: %w", err)
	}
	cfg.Moonraker.CallInterval, err = strconv.Atoi(CallInterval)
	if err != nil {
		return nil, fmt.Errorf("CallInterval: %w", err)
	}
	cfg.MQTT.CommandsEnabled, err = strconv.ParseBool(CommandsEnabled)
	if err != nil {
		return nil, fmt.Errorf("CommandsEnabled: %w", err)
	}
	if cfg.MQTT.Password != "" && cfg.MQTT.Username == "" {
		return nil, fmt.Errorf("MQTT password requires a username")
	}
	if err = cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}
