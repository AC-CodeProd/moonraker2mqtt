// Package setup is the portable, bounded provisioning boundary; Linux never imports it.
package setup

import (
	"encoding/json"
	"errors"
	"io"
	"moonraker2mqtt/config"
	"strings"
)

const MaxSettings = 3072

type Settings struct {
	SSID          string `json:"ssid"`
	WiFiPassword  string `json:"wifi_password"`
	MoonrakerHost string `json:"moonraker_host"`
	MoonrakerPort int    `json:"moonraker_port"`
	APIKey        string `json:"api_key"`
	MQTTHost      string `json:"mqtt_host"`
	MQTTPort      int    `json:"mqtt_port"`
	MQTTUsername  string `json:"mqtt_username"`
	MQTTPassword  string `json:"mqtt_password"`
	ClientID      string `json:"client_id"`
	TopicPrefix   string `json:"topic_prefix"`
	Commands      bool   `json:"commands"`
	PollSeconds   int    `json:"poll_seconds"`
}

func host(s string) bool {
	if len(s) == 0 || len(s) > 253 || strings.ContainsAny(s, " /\\@?#:\r\n\t") {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '-') {
			return false
		}
	}
	return true
}
func clean(s string, max int) bool {
	if len(s) > max {
		return false
	}
	for _, c := range s {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}
func (s Settings) Validate() error {
	if len(s.SSID) < 1 || !clean(s.SSID, 32) || !clean(s.WiFiPassword, 63) || (s.WiFiPassword != "" && len(s.WiFiPassword) < 8) {
		return errors.New("invalid Wi-Fi settings")
	}
	if !host(s.MoonrakerHost) || !host(s.MQTTHost) || s.MoonrakerPort < 1 || s.MoonrakerPort > 65535 || s.MQTTPort < 1 || s.MQTTPort > 65535 {
		return errors.New("invalid host or port")
	}
	if !clean(s.APIKey, 256) || !clean(s.MQTTUsername, 128) || !clean(s.MQTTPassword, 256) || (s.MQTTPassword != "" && s.MQTTUsername == "") {
		return errors.New("invalid authentication settings")
	}
	if !clean(s.ClientID, 64) || s.ClientID == "" || !clean(s.TopicPrefix, 128) || s.TopicPrefix == "" || strings.ContainsAny(s.TopicPrefix, "+#") || s.PollSeconds < 1 || s.PollSeconds > 3600 {
		return errors.New("invalid client, topic or polling interval")
	}
	return nil
}
func Decode(b []byte) (Settings, error) {
	var s Settings
	if len(b) == 0 || len(b) > MaxSettings {
		return s, errors.New("settings size limit")
	}
	// Reject unknown keys and trailing data; no unbounded reader allocation.
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		return s, errors.New("invalid settings JSON")
	}
	var extra interface{}
	if d.Decode(&extra) != io.EOF {
		return s, errors.New("trailing settings JSON")
	}
	return s, s.Validate()
}
func (s Settings) Config() *config.Config {
	c := config.DefaultConfig()
	c.Environment = "production"
	c.Moonraker.Host = s.MoonrakerHost
	c.Moonraker.Port = s.MoonrakerPort
	c.Moonraker.APIKey = s.APIKey
	c.Moonraker.CallInterval = s.PollSeconds
	c.MQTT.Host = s.MQTTHost
	c.MQTT.Port = s.MQTTPort
	c.MQTT.Username = s.MQTTUsername
	c.MQTT.Password = s.MQTTPassword
	c.MQTT.ClientID = s.ClientID
	c.MQTT.TopicPrefix = s.TopicPrefix
	c.MQTT.CommandsEnabled = s.Commands
	return c
}
