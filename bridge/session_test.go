package bridge

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"moonraker2mqtt/mqtt"
	"moonraker2mqtt/platform/esp32s3/network"
	"moonraker2mqtt/websocket"
)

// Drop a protocol only after both connects and the final initial publication.
// No production endpoints or printer commands are used by these adapters.
type sessionProbe struct {
	sync.Mutex
	mqttUp, moonUp             bool
	mqttConnects, moonConnects int
	drop                       string
	cleanup                    []string
	cancel                     context.CancelFunc
}
type sessionMQTT struct{ p *sessionProbe }
type sessionMoonraker struct{ p *sessionProbe }

func (m sessionMQTT) Connect() error {
	m.p.Lock()
	defer m.p.Unlock()
	m.p.mqttConnects++
	m.p.mqttUp = true
	if m.p.mqttConnects > 1 {
		m.p.cancel()
	}
	return nil
}
func (m sessionMQTT) Disconnect() error {
	m.p.Lock()
	defer m.p.Unlock()
	m.p.mqttUp = false
	m.p.cleanup = append(m.p.cleanup, "mqtt")
	return nil
}
func (m sessionMQTT) IsConnected() bool { m.p.Lock(); defer m.p.Unlock(); return m.p.mqttUp }
func (m sessionMQTT) Publish(topic string, _ []byte, _ byte, _ bool, _ int) error {
	if strings.HasSuffix(topic, "/printer/info") {
		m.p.Lock()
		defer m.p.Unlock()
		switch m.p.drop {
		case "mqtt":
			m.p.mqttUp = false
		case "moonraker":
			m.p.moonUp = false
		case "both":
			m.p.mqttUp = false
			m.p.moonUp = false
		case "cancel":
			m.p.cancel()
		}
	}
	return nil
}
func (sessionMQTT) Subscribe(string, mqtt.MessageHandler) error { return nil }
func (sessionMQTT) Unsubscribe(string) error                    { return nil }
func (m sessionMoonraker) Connect(context.Context) error {
	m.p.Lock()
	defer m.p.Unlock()
	m.p.moonConnects++
	m.p.moonUp = true
	if m.p.moonConnects > 1 {
		m.p.cancel()
	}
	return nil
}
func (m sessionMoonraker) Disconnect() error {
	m.p.Lock()
	defer m.p.Unlock()
	m.p.moonUp = false
	m.p.cleanup = append(m.p.cleanup, "moonraker")
	return nil
}
func (m sessionMoonraker) IsConnected() bool          { m.p.Lock(); defer m.p.Unlock(); return m.p.moonUp }
func (sessionMoonraker) HandleCommand(string, []byte) { panic("commands must stay disabled") }
func (sessionMoonraker) GetServerInfo(context.Context) (*websocket.ServerInfo, error) {
	return &websocket.ServerInfo{}, nil
}
func (sessionMoonraker) GetHostInfo(context.Context) (*websocket.PrinterInfo, error) {
	return &websocket.PrinterInfo{}, nil
}
func (sessionMoonraker) GetKlippyState(context.Context) (string, error) { return "ready", nil }
func (sessionMoonraker) QueryObjects(context.Context, map[string]any) (map[string]any, error) {
	return map[string]any{}, nil
}

func TestSessionDisconnectPolicyAndCleanup(t *testing.T) {
	for _, drop := range []string{"mqtt", "moonraker", "both", "cancel"} {
		t.Run(drop, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
			defer cancel()
			p := &sessionProbe{drop: drop, cancel: cancel}
			cfg := config.DefaultConfig()
			cfg.Moonraker.CallInterval = 1
			cfg.MQTT.CommandsEnabled = false
			broker := sessionMQTT{p}
			app := New(cfg, broker, logger.NewSerial(io.Discard, logger.ERROR))
			app.moonrakerClient = sessionMoonraker{p}
			err := network.Session(ctx, func() bool { return true }, func(ctx context.Context) error {
				defer func() { p.Lock(); p.cleanup = append(p.cleanup, "callback"); p.Unlock() }()
				defer broker.Disconnect() // Match the ESP32 runner's partial-connect safeguard.
				return app.Run(ctx)
			}, 5*time.Millisecond, time.Second)
			// This is the point where ESP32 supervision may back off and reset.
			p.Lock()
			defer p.Unlock()
			if !reflect.DeepEqual(p.cleanup, []string{"moonraker", "mqtt", "mqtt", "callback"}) {
				t.Fatalf("session returned before cleanup: %v", p.cleanup)
			}
			if drop == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel returned %v", err)
				}
			} else if testMCUSessionPolicy {
				if p.mqttConnects != 1 || p.moonConnects != 1 {
					t.Fatalf("MCU reconnected: MQTT=%d Moonraker=%d", p.mqttConnects, p.moonConnects)
				}
				if err == nil || ctx.Err() != nil || err == network.ErrShutdown {
					t.Fatalf("MCU must return protocol session error without waiting for ctx cancellation: %v", err)
				}
				protocol := "Moonraker"
				if drop != "moonraker" {
					protocol = "MQTT"
				}
				if !strings.Contains(err.Error(), protocol+" disconnected") {
					t.Fatalf("wrong session error: %v", err)
				}
			} else {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("host failed to reconnect before cancellation: %v", err)
				}
				if drop == "mqtt" && p.mqttConnects != 2 || drop != "mqtt" && p.moonConnects != 2 {
					t.Fatalf("host reconnection changed: MQTT=%d Moonraker=%d", p.mqttConnects, p.moonConnects)
				}
			}
		})
	}
}
