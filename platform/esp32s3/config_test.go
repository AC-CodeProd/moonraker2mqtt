package esp32s3

import "testing"

func TestFirmwareConfig(t *testing.T) {
	oldSSID, oldMoon, oldMQTT := WiFiSSID, MoonrakerHost, MQTTHost
	oldPort, oldInterval, oldCommands := MQTTPort, CallInterval, CommandsEnabled
	defer func() {
		WiFiSSID = oldSSID
		MoonrakerHost = oldMoon
		MQTTHost = oldMQTT
		MQTTPort = oldPort
		CallInterval = oldInterval
		CommandsEnabled = oldCommands
	}()
	WiFiSSID = "test-network"
	MoonrakerHost = "192.0.2.10"
	MQTTHost = "192.0.2.20"
	cfg, err := Config()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MQTT.CommandsEnabled || cfg.MQTT.UseTLS || cfg.Moonraker.SSL || cfg.MQTT.QoS != 0 {
		t.Fatal("unsafe firmware defaults")
	}
	if cfg.MQTT.Host != MQTTHost || cfg.Moonraker.Host != MoonrakerHost {
		t.Fatal("link-time hosts not applied")
	}
	for _, tt := range []struct {
		name    string
		set     func()
		restore func()
	}{
		{"missing SSID", func() { WiFiSSID = "" }, func() { WiFiSSID = "test-network" }},
		{"missing Moonraker host", func() { MoonrakerHost = "" }, func() { MoonrakerHost = "192.0.2.10" }},
		{"invalid broker port", func() { MQTTPort = "65536" }, func() { MQTTPort = oldPort }},
		{"invalid interval", func() { CallInterval = "0" }, func() { CallInterval = oldInterval }},
		{"invalid command flag", func() { CommandsEnabled = "maybe" }, func() { CommandsEnabled = oldCommands }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.set()
			defer tt.restore()
			if _, err := Config(); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
