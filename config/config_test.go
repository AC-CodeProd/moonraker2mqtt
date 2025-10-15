package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMoonrakerConfig_GetMonitoredObjects(t *testing.T) {
	tests := []struct {
		name          string
		monitoredObjs string
		expectedKeys  []string
		wantErr       bool
		errMsg        string
	}{
		{
			name:          "empty string returns defaults",
			monitoredObjs: "",
			expectedKeys:  []string{"print_stats", "toolhead", "extruder", "heater_bed"},
			wantErr:       false,
		},
		{
			name:          "valid JSON with null values",
			monitoredObjs: `{"print_stats":null,"toolhead":["position"]}`,
			expectedKeys:  []string{"print_stats", "toolhead"},
			wantErr:       false,
		},
		{
			name:          "valid JSON with string arrays",
			monitoredObjs: `{"extruder":["temperature","target"],"heater_bed":["temperature"]}`,
			expectedKeys:  []string{"extruder", "heater_bed"},
			wantErr:       false,
		},
		{
			name:          "invalid JSON syntax",
			monitoredObjs: `{"invalid": json}`,
			wantErr:       true,
			errMsg:        "failed to parse monitored objects JSON",
		},
		{
			name:          "not a JSON object",
			monitoredObjs: `["not", "an", "object"]`,
			wantErr:       true,
			errMsg:        "monitored objects must be a valid JSON object",
		},
		{
			name:          "invalid value type - number",
			monitoredObjs: `{"print_stats":123}`,
			wantErr:       true,
			errMsg:        "monitored object 'print_stats' must be null or an array of strings",
		},
		{
			name:          "invalid array element - number",
			monitoredObjs: `{"toolhead":["position", 123]}`,
			wantErr:       true,
			errMsg:        "monitored object 'toolhead' field 1 must be a string",
		},
		{
			name:          "whitespace handling",
			monitoredObjs: `  {"print_stats":null}  `,
			expectedKeys:  []string{"print_stats"},
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := MoonrakerConfig{
				Host:             "localhost",
				Port:             7125,
				Timeout:          30,
				CallInterval:     2,
				MonitoredObjects: tt.monitoredObjs,
			}

			result, err := config.GetMonitoredObjects()
			if (err != nil) != tt.wantErr {
				t.Errorf("GetMonitoredObjects() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("GetMonitoredObjects() error = %v, expected to contain %v", err, tt.errMsg)
				}
				return
			}

			for _, key := range tt.expectedKeys {
				if _, exists := result[key]; !exists {
					t.Errorf("GetMonitoredObjects() missing expected key: %v", key)
				}
			}
		})
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: Config{
				Environment: "development",
				Moonraker: MoonrakerConfig{
					Host:                 "localhost",
					Port:                 7125,
					Timeout:              30,
					MaxReconnectAttempts: 10,
					CallInterval:         2,
				},
				MQTT: MQTTConfig{
					Host:                 "localhost",
					Port:                 1883,
					ClientID:             "test-client",
					TopicPrefix:          "test",
					QoS:                  0,
					MaxReconnectAttempts: 10,
				},
				Logging: LoggingConfig{
					Level:  "info",
					Format: "text",
				},
			},
			wantErr: false,
		},
		{
			name: "invalid environment",
			config: Config{
				Environment: "invalid-env",
				Moonraker:   MoonrakerConfig{Host: "localhost", Port: 7125, Timeout: 30, CallInterval: 2},
				MQTT:        MQTTConfig{Host: "localhost", Port: 1883, ClientID: "test", TopicPrefix: "test"},
				Logging:     LoggingConfig{Level: "info", Format: "text"},
			},
			wantErr: true,
			errMsg:  "invalid environment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("Config.Validate() error = %v, expected to contain %v", err, tt.errMsg)
			}
		})
	}
}

func TestMoonrakerConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  MoonrakerConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: MoonrakerConfig{
				Host:                 "localhost",
				Port:                 7125,
				Timeout:              30,
				MaxReconnectAttempts: 10,
				CallInterval:         2,
			},
			wantErr: false,
		},
		{
			name: "empty host",
			config: MoonrakerConfig{
				Host:         "",
				Port:         7125,
				Timeout:      30,
				CallInterval: 2,
			},
			wantErr: true,
			errMsg:  "moonraker host cannot be empty",
		},
		{
			name: "invalid port - too low",
			config: MoonrakerConfig{
				Host:         "localhost",
				Port:         0,
				Timeout:      30,
				CallInterval: 2,
			},
			wantErr: true,
			errMsg:  "moonraker port must be between 1 and 65535",
		},
		{
			name: "invalid port - too high",
			config: MoonrakerConfig{
				Host:         "localhost",
				Port:         70000,
				Timeout:      30,
				CallInterval: 2,
			},
			wantErr: true,
			errMsg:  "moonraker port must be between 1 and 65535",
		},
		{
			name: "invalid timeout",
			config: MoonrakerConfig{
				Host:         "localhost",
				Port:         7125,
				Timeout:      -1,
				CallInterval: 2,
			},
			wantErr: true,
			errMsg:  "moonraker timeout must be positive",
		},
		{
			name: "invalid call interval",
			config: MoonrakerConfig{
				Host:         "localhost",
				Port:         7125,
				Timeout:      30,
				CallInterval: 0,
			},
			wantErr: true,
			errMsg:  "moonraker call interval must be positive",
		},
		{
			name: "invalid monitored objects JSON",
			config: MoonrakerConfig{
				Host:             "localhost",
				Port:             7125,
				Timeout:          30,
				CallInterval:     2,
				MonitoredObjects: `{"invalid": json}`,
			},
			wantErr: true,
			errMsg:  "invalid monitored objects",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("MoonrakerConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("MoonrakerConfig.Validate() error = %v, expected to contain %v", err, tt.errMsg)
			}
		})
	}
}

func TestMQTTConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  MQTTConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: MQTTConfig{
				Host:        "localhost",
				Port:        1883,
				ClientID:    "test-client",
				TopicPrefix: "test",
				QoS:         0,
			},
			wantErr: false,
		},
		{
			name: "empty host",
			config: MQTTConfig{
				Host:        "",
				Port:        1883,
				ClientID:    "test-client",
				TopicPrefix: "test",
			},
			wantErr: true,
			errMsg:  "mqtt host cannot be empty",
		},
		{
			name: "invalid QoS",
			config: MQTTConfig{
				Host:        "localhost",
				Port:        1883,
				ClientID:    "test-client",
				TopicPrefix: "test",
				QoS:         3,
			},
			wantErr: true,
			errMsg:  "mqtt QoS must be 0, 1, or 2",
		},
		{
			name: "topic prefix with leading slash",
			config: MQTTConfig{
				Host:        "localhost",
				Port:        1883,
				ClientID:    "test-client",
				TopicPrefix: "/test",
				QoS:         0,
			},
			wantErr: true,
			errMsg:  "mqtt topic prefix should not start or end with '/'",
		},
		{
			name: "topic prefix with trailing slash",
			config: MQTTConfig{
				Host:        "localhost",
				Port:        1883,
				ClientID:    "test-client",
				TopicPrefix: "test/",
				QoS:         0,
			},
			wantErr: true,
			errMsg:  "mqtt topic prefix should not start or end with '/'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("MQTTConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("MQTTConfig.Validate() error = %v, expected to contain %v", err, tt.errMsg)
			}
		})
	}
}

func TestLoggingConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  LoggingConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid config - info/text",
			config:  LoggingConfig{Level: "info", Format: "text"},
			wantErr: false,
		},
		{
			name:    "valid config - debug/json",
			config:  LoggingConfig{Level: "debug", Format: "json"},
			wantErr: false,
		},
		{
			name:    "valid config - case insensitive",
			config:  LoggingConfig{Level: "INFO", Format: "TEXT"},
			wantErr: false,
		},
		{
			name:    "invalid log level",
			config:  LoggingConfig{Level: "invalid", Format: "text"},
			wantErr: true,
			errMsg:  "invalid log level",
		},
		{
			name:    "invalid log format",
			config:  LoggingConfig{Level: "info", Format: "invalid"},
			wantErr: true,
			errMsg:  "invalid log format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("LoggingConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("LoggingConfig.Validate() error = %v, expected to contain %v", err, tt.errMsg)
			}
		})
	}
}

func TestLoadOrCreateConfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "config_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("Failed to remove temp dir: %v", err)
		}
	}()

	tests := []struct {
		name       string
		setupFile  func() string
		wantErr    bool
		errMsg     string
		expectFile bool
	}{
		{
			name: "file doesn't exist - creates default",
			setupFile: func() string {
				return filepath.Join(tmpDir, "new_config.yaml")
			},
			wantErr:    false,
			expectFile: true,
		},
		{
			name: "valid existing file",
			setupFile: func() string {
				path := filepath.Join(tmpDir, "valid_config.yaml")
				validConfig := `environment: development
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
				if err := os.WriteFile(path, []byte(validConfig), 0644); err != nil {
					t.Fatalf("Failed to write valid config file: %v", err)
				}
				return path
			},
			wantErr:    false,
			expectFile: true,
		},
		{
			name: "invalid YAML file",
			setupFile: func() string {
				path := filepath.Join(tmpDir, "invalid_config.yaml")
				invalidConfig := `environment: development
moonraker:
  host: localhost
  port: invalid_port  # This should be a number
`
				if err := os.WriteFile(path, []byte(invalidConfig), 0644); err != nil {
					t.Fatalf("Failed to write invalid config file: %v", err)
				}
				return path
			},
			wantErr: true,
			errMsg:  "failed to parse config file",
		},
		{
			name: "config with validation errors",
			setupFile: func() string {
				path := filepath.Join(tmpDir, "validation_error_config.yaml")
				invalidConfig := `environment: invalid_environment
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
				if err := os.WriteFile(path, []byte(invalidConfig), 0644); err != nil {
					t.Fatalf("Failed to write invalid config file: %v", err)
				}
				return path
			},
			wantErr: true,
			errMsg:  "config validation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := tt.setupFile()

			config, err := LoadOrCreateConfig(configPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadOrCreateConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("LoadOrCreateConfig() error = %v, expected to contain %v", err, tt.errMsg)
				}
				return
			}

			if config == nil {
				t.Error("LoadOrCreateConfig() returned nil config without error")
				return
			}

			if err := config.Validate(); err != nil {
				t.Errorf("LoadOrCreateConfig() returned invalid config: %v", err)
			}

			if tt.expectFile {
				if _, err := os.Stat(configPath); os.IsNotExist(err) {
					t.Errorf("LoadOrCreateConfig() expected to create file %v but it doesn't exist", configPath)
				}
			}
		})
	}
}
