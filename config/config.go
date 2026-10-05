//go:build !tinygo

package config

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

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
