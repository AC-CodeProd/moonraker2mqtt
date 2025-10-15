package main

import (
	"os"
	"strings"
	"testing"

	"moonraker2mqtt/config"
)

func TestNewApp_ValidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := tmpDir + "/test_config.yaml"

	validConfig := `environment: testing
moonraker:
  host: localhost
  port: 7125
  timeout: 30
  call_interval: 2
mqtt:
  host: localhost
  port: 1883
  client_id: test-client
  topic_prefix: test
  qos: 0
logging:
  level: info
  format: text`

	if err := os.WriteFile(configPath, []byte(validConfig), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	app, err := NewApp(configPath)
	if err != nil {
		t.Errorf("NewApp() failed with valid config: %v", err)
		return
	}

	if app == nil {
		t.Error("NewApp() returned nil app")
		return
	}

	if app.config.Environment != "testing" {
		t.Errorf("Expected environment 'testing', got '%s'", app.config.Environment)
	}

	if app.config.MQTT.ClientID != "test-client" {
		t.Errorf("Expected client ID 'test-client', got '%s'", app.config.MQTT.ClientID)
	}
}

func TestNewApp_InvalidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := tmpDir + "/invalid_config.yaml"

	invalidConfig := `environment: invalid_environment
moonraker:
  host: ""  # Invalid empty host
  port: 0   # Invalid port
mqtt:
  host: localhost
  port: 1883
  client_id: test
  topic_prefix: test`

	if err := os.WriteFile(configPath, []byte(invalidConfig), 0644); err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	app, err := NewApp(configPath)
	if err == nil {
		t.Error("NewApp() should fail with invalid config")
	}

	if app != nil {
		t.Error("NewApp() should return nil app with invalid config")
	}
}

func TestApp_ConfigValidation_Integration(t *testing.T) {
	tests := []struct {
		name        string
		configYAML  string
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid complete config",
			configYAML: `environment: development
moonraker:
  host: localhost
  port: 7125
  timeout: 30
  call_interval: 2
mqtt:
  host: localhost
  port: 1883
  client_id: test-client
  topic_prefix: test
  qos: 0
logging:
  level: info
  format: text`,
			expectError: false,
		},
		{
			name: "invalid MQTT port",
			configYAML: `environment: development
moonraker:
  host: localhost
  port: 7125
  timeout: 30
  call_interval: 2
mqtt:
  host: localhost
  port: 99999  # Invalid port
  client_id: test-client
  topic_prefix: test
  qos: 0
logging:
  level: info
  format: text`,
			expectError: true,
			errorMsg:    "mqtt port must be between 1 and 65535",
		},
		{
			name: "invalid log level",
			configYAML: `environment: development
moonraker:
  host: localhost
  port: 7125
  timeout: 30
  call_interval: 2
mqtt:
  host: localhost
  port: 1883
  client_id: test-client
  topic_prefix: test
  qos: 0
logging:
  level: invalid_level  # Invalid log level
  format: text`,
			expectError: true,
			errorMsg:    "invalid log level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := tmpDir + "/test_config.yaml"

			if err := os.WriteFile(configPath, []byte(tt.configYAML), 0644); err != nil {
				t.Fatalf("Failed to create test config: %v", err)
			}

			app, err := NewApp(configPath)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
					return
				}
				if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error to contain '%s', got: %v", tt.errorMsg, err)
				}
				if app != nil {
					t.Error("Expected nil app when error occurs")
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
					return
				}
				if app == nil {
					t.Error("Expected valid app, got nil")
				}
			}
		})
	}
}

func TestDefaultConfigGeneration(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := tmpDir + "/generated_config.yaml"

	err := config.GenerateDefaultConfig(configPath)
	if err != nil {
		t.Errorf("GenerateDefaultConfig() failed: %v", err)
		return
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("Default config file was not created")
		return
	}

	app, err := NewApp(configPath)
	if err != nil {
		t.Errorf("Failed to load generated config: %v", err)
		return
	}

	if app == nil {
		t.Error("App is nil with generated config")
		return
	}

	if app.config.Environment != "development" {
		t.Errorf("Expected default environment 'development', got '%s'", app.config.Environment)
	}

	if app.config.Moonraker.Port != 7125 {
		t.Errorf("Expected default Moonraker port 7125, got %d", app.config.Moonraker.Port)
	}

	if app.config.MQTT.Port != 1883 {
		t.Errorf("Expected default MQTT port 1883, got %d", app.config.MQTT.Port)
	}
}
