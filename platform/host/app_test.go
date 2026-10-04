//go:build !tinygo

package host

import (
	"moonraker2mqtt/config"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostAssembly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.GenerateDefaultConfig(path); err != nil {
		t.Fatal(err)
	}
	app, err := NewApp(path)
	if err != nil || app == nil {
		t.Fatalf("host assembly: %v", err)
	}
}
func TestHostAssemblyInvalidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.DefaultConfig()
	cfg.MQTT.Port = 0
	if err := config.SaveConfig(cfg, path); err != nil {
		t.Fatal(err)
	}
	app, err := NewApp(path)
	if app != nil || err == nil || !strings.Contains(err.Error(), "failed to load config") {
		t.Fatalf("invalid config: %v", err)
	}
}
