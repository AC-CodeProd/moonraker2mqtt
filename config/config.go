package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

const DEFAULT_REQUEST_TIMEOUT = 30
const DEFAULT_MAX_RECONNECT_ATTEMPTS = 10

func LoadConfig(filename string) (*Config, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			fmt.Printf("Warning: failed to close config file: %v\n", closeErr)
		}
	}()

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	overrideWithEnv(&config)

	return &config, nil
}

func overrideWithEnv(config *Config) {
	if err := godotenv.Load(".env"); err != nil {
		log.Printf("No .env file found or error loading it: %v", err)
	}

	if env := os.Getenv("ENVIRONMENT"); env != "" {
		config.Environment = env
	}

	if host := os.Getenv("MOONRAKER_HOST"); host != "" {
		config.Moonraker.Host = host
	}
	if port := os.Getenv("MOONRAKER_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			config.Moonraker.Port = p
		}
	}
	if apiKey := os.Getenv("MOONRAKER_API_KEY"); apiKey != "" {
		config.Moonraker.APIKey = apiKey
	}
	if ssl := os.Getenv("MOONRAKER_SSL"); ssl != "" {
		if s, err := strconv.ParseBool(ssl); err == nil {
			config.Moonraker.SSL = s
		}
	}
	if timeout := os.Getenv("MOONRAKER_TIMEOUT"); timeout != "" {
		if t, err := strconv.Atoi(timeout); err == nil {
			config.Moonraker.Timeout = t
		}
	}
	if autoReconnect := os.Getenv("MOONRAKER_AUTO_RECONNECT"); autoReconnect != "" {
		if ar, err := strconv.ParseBool(autoReconnect); err == nil {
			config.Moonraker.AutoReconnect = ar
		}
	}
	if maxReconnect := os.Getenv("MOONRAKER_MAX_RECONNECT_ATTEMPTS"); maxReconnect != "" {
		if mr, err := strconv.Atoi(maxReconnect); err == nil {
			config.Moonraker.MaxReconnectAttempts = mr
		}
	}

	if callInterval := os.Getenv("MOONRAKER_CALL_INTERVAL"); callInterval != "" {
		if ci, err := strconv.Atoi(callInterval); err == nil {
			config.Moonraker.CallInterval = ci
		}
	}

	if monitoredObjects := os.Getenv("MOONRAKER_MONITORED_OBJECTS"); monitoredObjects != "" {
		config.Moonraker.MonitoredObjects = monitoredObjects
	}

	if host := os.Getenv("MQTT_HOST"); host != "" {
		config.MQTT.Host = host
	}
	if port := os.Getenv("MQTT_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			config.MQTT.Port = p
		}
	}
	if username := os.Getenv("MQTT_USERNAME"); username != "" {
		config.MQTT.Username = username
	}
	if password := os.Getenv("MQTT_PASSWORD"); password != "" {
		config.MQTT.Password = password
	}
	if clientID := os.Getenv("MQTT_CLIENT_ID"); clientID != "" {
		config.MQTT.ClientID = clientID
	}
	if topicPrefix := os.Getenv("MQTT_TOPIC_PREFIX"); topicPrefix != "" {
		config.MQTT.TopicPrefix = topicPrefix
	}
	if qos := os.Getenv("MQTT_QOS"); qos != "" {
		if q, err := strconv.ParseUint(qos, 10, 8); err == nil {
			config.MQTT.QoS = byte(q)
		}
	}
	if retain := os.Getenv("MQTT_RETAIN"); retain != "" {
		if r, err := strconv.ParseBool(retain); err == nil {
			config.MQTT.Retain = r
		}
	}
	if autoReconnect := os.Getenv("MQTT_AUTO_RECONNECT"); autoReconnect != "" {
		if ar, err := strconv.ParseBool(autoReconnect); err == nil {
			config.MQTT.AutoReconnect = ar
		}
	}
	if maxReconnect := os.Getenv("MQTT_MAX_RECONNECT_ATTEMPTS"); maxReconnect != "" {
		if mr, err := strconv.Atoi(maxReconnect); err == nil {
			config.MQTT.MaxReconnectAttempts = mr
		}
	}

	if commandsEnabled := os.Getenv("MQTT_COMMANDS_ENABLED"); commandsEnabled != "" {
		if ce, err := strconv.ParseBool(commandsEnabled); err == nil {
			config.MQTT.CommandsEnabled = ce
		}
	}

	if level := os.Getenv("LOG_LEVEL"); level != "" {
		config.Logging.Level = level
	}
	if format := os.Getenv("LOG_FORMAT"); format != "" {
		config.Logging.Format = format
	}
}

func SaveConfig(config *Config, filename string) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			fmt.Printf("Warning: failed to close config file: %v\n", closeErr)
		}
	}()

	_, err = file.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

func LoadOrCreateConfig(filename string) (*Config, error) {
	if _, err := os.Stat(filename); errors.Is(err, os.ErrNotExist) {
		config := DefaultConfig()

		if err := config.Validate(); err != nil {
			return nil, fmt.Errorf("default config validation failed: %w", err)
		}

		if err := SaveConfig(config, filename); err != nil {
			return nil, fmt.Errorf("failed to save default config: %w", err)
		}
		return config, nil
	}

	config, err := LoadConfig(filename)
	if err != nil {
		return nil, err
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return config, nil
}

func GenerateDefaultConfig(filename string) error {
	config := DefaultConfig()
	return SaveConfig(config, filename)
}

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
