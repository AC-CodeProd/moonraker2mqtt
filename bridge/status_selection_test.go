package bridge

import (
	"io"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"reflect"
	"testing"
)

func TestStatusSelectionFallbackAndReadOnly(t *testing.T) {
	for _, selection := range []string{"", `{}`, `{"unicode 🌡":["position","é"]}`, "invalid", `{"toolhead":42}`, `{"x":[1]}`} {
		cfg := config.DefaultConfig()
		cfg.Moonraker.MonitoredObjects = selection
		a := &App{config: cfg, logger: logger.NewSerial(io.Discard, logger.ERROR)}
		expected, err := cfg.Moonraker.GetMonitoredObjects()
		if err != nil {
			defaults := config.DefaultConfig()
			defaults.Moonraker.MonitoredObjects = ""
			expected, _ = defaults.Moonraker.GetMonitoredObjects()
		}
		one := a.statusObjects()
		if !reflect.DeepEqual(one, expected) {
			t.Fatalf("selection changed %q", selection)
		}
		two := a.statusObjects()
		if !reflect.DeepEqual(one, two) {
			t.Fatal("read-only reuse altered selection")
		}
		if a.objectStatusTopic("unexpected") != cfg.MQTT.TopicPrefix+"/objects/unexpected" {
			t.Fatal("unexpected outer key topic changed")
		}
	}
}
func TestStatusHostMutableConfiguration(t *testing.T) {
	if testMCUStatusCache {
		t.Skip("MCU intentionally snapshots stable configuration")
	}
	cfg := config.DefaultConfig()
	a := New(cfg, sessionMQTT{}, logger.NewSerial(io.Discard, logger.ERROR))
	cfg.Moonraker.MonitoredObjects = `{"first":null}`
	_ = a.statusObjects()
	cfg.Moonraker.MonitoredObjects = `{"second":null}`
	cfg.MQTT.TopicPrefix = "changed"
	if !reflect.DeepEqual(a.statusObjects(), map[string]any{"second": nil}) || a.klippyStatusTopic() != "changed/klipper/state" {
		t.Fatal("host live configuration semantics changed")
	}
}
