package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const DEFAULT_REQUEST_TIMEOUT = 30
const DEFAULT_MAX_RECONNECT_ATTEMPTS = 10

func (m *MoonrakerConfig) GetWebSocketURL() string {
	protocol := "ws"
	if m.SSL {
		protocol = "wss"
	}
	return fmt.Sprintf("%s://%s:%d/websocket", protocol, m.Host, m.Port)
}

func (m *MoonrakerConfig) GetTimeout() time.Duration {
	if m.Timeout <= 0 {
		return time.Duration(DEFAULT_REQUEST_TIMEOUT) * time.Second
	}
	return time.Duration(m.Timeout) * time.Second
}

func (m *MQTTConfig) GetMQTTBrokerURL() string {
	return fmt.Sprintf("tcp://%s:%d", m.Host, m.Port)
}

func (m *MoonrakerConfig) GetMonitoredObjects() (map[string]any, error) {
	if m.MonitoredObjects == "" {
		return map[string]any{
			"print_stats": nil,
			"toolhead":    []string{"position"},
			"extruder":    []string{"temperature", "target"},
			"heater_bed":  []string{"temperature", "target"},
		}, nil
	}

	trimmed := strings.TrimSpace(m.MonitoredObjects)
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return nil, fmt.Errorf("monitored objects must be a valid JSON object, got: %s", trimmed)
	}

	var objects map[string]any
	if err := json.Unmarshal([]byte(trimmed), &objects); err != nil {
		return nil, fmt.Errorf("failed to parse monitored objects JSON: %w", err)
	}

	for objectName, objectValue := range objects {
		if objectValue == nil {
			continue
		}

		switch v := objectValue.(type) {
		case []interface{}:
			for i, item := range v {
				if _, ok := item.(string); !ok {
					return nil, fmt.Errorf("monitored object '%s' field %d must be a string, got %T", objectName, i, item)
				}
			}
		default:
			return nil, fmt.Errorf("monitored object '%s' must be null or an array of strings, got %T", objectName, v)
		}
	}

	return objects, nil
}

func DefaultConfig() *Config {
	return &Config{
		Environment: "development",
		Moonraker: MoonrakerConfig{
			Host:                 "localhost",
			Port:                 7125,
			APIKey:               "",
			SSL:                  false,
			Timeout:              DEFAULT_REQUEST_TIMEOUT,
			AutoReconnect:        true,
			MaxReconnectAttempts: DEFAULT_MAX_RECONNECT_ATTEMPTS,
			CallInterval:         2,
			MonitoredObjects:     `{"print_stats":null,"toolhead":["position"],"extruder":["temperature","target"],"heater_bed":["temperature","target"]}`,
		},
		MQTT: MQTTConfig{
			Host:                 "localhost",
			Port:                 1883,
			Username:             "",
			Password:             "",
			UseTLS:               false,
			ClientID:             "moonraker2mqtt",
			TopicPrefix:          "moonraker",
			QoS:                  0,
			Retain:               false,
			AutoReconnect:        true,
			MaxReconnectAttempts: DEFAULT_MAX_RECONNECT_ATTEMPTS,
			CommandsEnabled:      true,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
		},
	}
}

func (c *Config) Validate() error {
	if err := c.Moonraker.Validate(); err != nil {
		return fmt.Errorf("moonraker config validation failed: %w", err)
	}

	if err := c.MQTT.Validate(); err != nil {
		return fmt.Errorf("mqtt config validation failed: %w", err)
	}

	if err := c.Logging.Validate(); err != nil {
		return fmt.Errorf("logging config validation failed: %w", err)
	}

	validEnvs := []string{"development", "production", "testing"}
	found := false
	for _, env := range validEnvs {
		if c.Environment == env {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("invalid environment '%s', must be one of: %s", c.Environment, strings.Join(validEnvs, ", "))
	}

	return nil
}

func (m *MoonrakerConfig) Validate() error {
	if strings.TrimSpace(m.Host) == "" {
		return fmt.Errorf("moonraker host cannot be empty")
	}

	if m.Port <= 0 || m.Port > 65535 {
		return fmt.Errorf("moonraker port must be between 1 and 65535, got %d", m.Port)
	}

	if m.Timeout <= 0 {
		return fmt.Errorf("moonraker timeout must be positive, got %d", m.Timeout)
	}

	if m.MaxReconnectAttempts < 0 {
		return fmt.Errorf("moonraker max reconnect attempts must be non-negative, got %d", m.MaxReconnectAttempts)
	}

	if m.CallInterval <= 0 {
		return fmt.Errorf("moonraker call interval must be positive, got %d", m.CallInterval)
	}

	if m.MonitoredObjects != "" {
		_, err := m.GetMonitoredObjects()
		if err != nil {
			return fmt.Errorf("invalid monitored objects: %w", err)
		}
	}

	return nil
}

func (m *MQTTConfig) Validate() error {
	if strings.TrimSpace(m.Host) == "" {
		return fmt.Errorf("mqtt host cannot be empty")
	}

	if m.Port <= 0 || m.Port > 65535 {
		return fmt.Errorf("mqtt port must be between 1 and 65535, got %d", m.Port)
	}

	if strings.TrimSpace(m.ClientID) == "" {
		return fmt.Errorf("mqtt client ID cannot be empty")
	}

	if strings.TrimSpace(m.TopicPrefix) == "" {
		return fmt.Errorf("mqtt topic prefix cannot be empty")
	}

	if m.QoS > 2 {
		return fmt.Errorf("mqtt QoS must be 0, 1, or 2, got %d", m.QoS)
	}

	if m.MaxReconnectAttempts < 0 {
		return fmt.Errorf("mqtt max reconnect attempts must be non-negative, got %d", m.MaxReconnectAttempts)
	}

	if strings.HasPrefix(m.TopicPrefix, "/") || strings.HasSuffix(m.TopicPrefix, "/") {
		return fmt.Errorf("mqtt topic prefix should not start or end with '/', got '%s'", m.TopicPrefix)
	}

	return nil
}

func (l *LoggingConfig) Validate() error {
	validLevels := []string{"debug", "info", "warn", "warning", "error"}
	found := false
	level := strings.ToLower(l.Level)
	for _, validLevel := range validLevels {
		if level == validLevel {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("invalid log level '%s', must be one of: %s", l.Level, strings.Join(validLevels, ", "))
	}

	validFormats := []string{"text", "json"}
	found = false
	format := strings.ToLower(l.Format)
	for _, validFormat := range validFormats {
		if format == validFormat {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("invalid log format '%s', must be one of: %s", l.Format, strings.Join(validFormats, ", "))
	}

	return nil
}
