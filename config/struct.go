package config

type Config struct {
	Environment string          `yaml:"environment" env:"ENVIRONMENT"`
	Moonraker   MoonrakerConfig `yaml:"moonraker"`
	MQTT        MQTTConfig      `yaml:"mqtt"`
	Logging     LoggingConfig   `yaml:"logging"`
}

type MoonrakerConfig struct {
	Host                 string `yaml:"host" env:"MOONRAKER_HOST"`
	Port                 int    `yaml:"port" env:"MOONRAKER_PORT"`
	APIKey               string `yaml:"api_key" env:"MOONRAKER_API_KEY"`
	SSL                  bool   `yaml:"ssl" env:"MOONRAKER_SSL"`
	Timeout              int    `yaml:"timeout" env:"MOONRAKER_TIMEOUT"`
	AutoReconnect        bool   `yaml:"auto_reconnect" env:"MOONRAKER_AUTO_RECONNECT"`
	MaxReconnectAttempts int    `yaml:"max_reconnect_attempts" env:"MOONRAKER_MAX_RECONNECT_ATTEMPTS"`
	CallInterval         int    `yaml:"call_interval" env:"MOONRAKER_CALL_INTERVAL"`
	MonitoredObjects     string `yaml:"monitored_objects" env:"MOONRAKER_MONITORED_OBJECTS"`
}

type MQTTConfig struct {
	Host                 string `yaml:"host" env:"MQTT_HOST"`
	Port                 int    `yaml:"port" env:"MQTT_PORT"`
	Username             string `yaml:"username" env:"MQTT_USERNAME"`
	Password             string `yaml:"password" env:"MQTT_PASSWORD"`
	UseTLS               bool   `yaml:"use_tls" env:"MQTT_USE_TLS"`
	ClientID             string `yaml:"client_id" env:"MQTT_CLIENT_ID"`
	TopicPrefix          string `yaml:"topic_prefix" env:"MQTT_TOPIC_PREFIX"`
	QoS                  byte   `yaml:"qos" env:"MQTT_QOS"`
	Retain               bool   `yaml:"retain" env:"MQTT_RETAIN"`
	AutoReconnect        bool   `yaml:"auto_reconnect" env:"MQTT_AUTO_RECONNECT"`
	MaxReconnectAttempts int    `yaml:"max_reconnect_attempts" env:"MQTT_MAX_RECONNECT_ATTEMPTS"`
	CommandsEnabled      bool   `yaml:"commands_enabled" env:"MQTT_COMMANDS_ENABLED"`
}

type LoggingConfig struct {
	Level  string `yaml:"level" env:"LOG_LEVEL"`
	Format string `yaml:"format" env:"LOG_FORMAT"`
}
