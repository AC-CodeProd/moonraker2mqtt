package bridge

import (
	"bytes"
	"context"
	"errors"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"moonraker2mqtt/platform/esp32s3/network"
	"moonraker2mqtt/websocket"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Offline adapters only. These errors are artificial injections, not capture data.
type startupMQTT struct {
	sessionMQTT
	connectErr, writeErr error
	cancel               context.CancelFunc
}

func (m startupMQTT) Connect() error {
	if m.connectErr != nil {
		return m.connectErr
	}
	return m.sessionMQTT.Connect()
}
func (m startupMQTT) Publish(topic string, b []byte, q byte, r bool, n int) error {
	if m.writeErr != nil {
		if m.cancel != nil {
			m.cancel()
		}
		return m.writeErr
	}
	return m.sessionMQTT.Publish(topic, b, q, r, n)
}

type startupMoon struct {
	sessionMoonraker
	connectErr    error
	asyncNotReady bool
}

func (m startupMoon) Connect(ctx context.Context) error {
	if m.connectErr != nil {
		return m.connectErr
	}
	if m.asyncNotReady {
		return nil
	}
	return m.sessionMoonraker.Connect(ctx)
}
func (m startupMoon) GetServerInfo(ctx context.Context) (*websocket.ServerInfo, error) {
	if m.asyncNotReady {
		m.p.cancel()
		return nil, errors.New("offline async connection not ready")
	}
	return m.sessionMoonraker.GetServerInfo(ctx)
}

func TestStartupAsyncNilIsNotReadiness(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &sessionProbe{cancel: cancel}
	cfg := config.DefaultConfig()
	cfg.MQTT.CommandsEnabled = false
	var out bytes.Buffer
	app := New(cfg, sessionMQTT{p}, logger.NewSerial(&out, logger.INFO))
	app.moonrakerClient = startupMoon{sessionMoonraker: sessionMoonraker{p}, asyncNotReady: true}
	if err := app.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("return %v", err)
	}
	if !strings.Contains(out.String(), "Successfully connected to both") || !strings.Contains(out.String(), "offline async connection not ready") {
		t.Fatalf("missing false-readiness evidence: %s", out.String())
	}
}

func TestStartupErrorsPropagateWithoutCauseLog(t *testing.T) {
	for _, protocol := range []string{"mqtt", "moonraker"} {
		t.Run(protocol, func(t *testing.T) {
			sentinel := errors.New("injected offline startup failure")
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			p := &sessionProbe{cancel: cancel}
			cfg := config.DefaultConfig()
			cfg.MQTT.CommandsEnabled = false
			broker := startupMQTT{sessionMQTT: sessionMQTT{p}}
			moon := startupMoon{sessionMoonraker: sessionMoonraker{p}}
			if protocol == "mqtt" {
				broker.connectErr = sentinel
			} else {
				moon.connectErr = sentinel
			}
			var out bytes.Buffer
			app := New(cfg, broker, logger.NewSerial(&out, logger.INFO))
			app.moonrakerClient = moon
			err := network.Session(ctx, func() bool { return true }, func(c context.Context) error { defer broker.Disconnect(); return app.Run(c) }, time.Millisecond, time.Second)
			expectedErr := sentinel
			if testMCUSessionPolicy {
				expectedErr = context.DeadlineExceeded
			}
			if !errors.Is(err, expectedErr) {
				t.Fatalf("cause lost in return: %v", err)
			}
			if strings.Contains(out.String(), sentinel.Error()) {
				t.Fatalf("unexpected cause log: %s", out.String())
			}
			expected := []string{"mqtt"}
			if testMCUSessionPolicy && protocol == "mqtt" {
				expected = []string{"mqtt", "mqtt"}
			}
			if protocol == "moonraker" {
				expected = []string{"mqtt", "mqtt"}
				if testMCUSessionPolicy {
					expected = []string{"moonraker", "mqtt", "mqtt"}
				}
			}
			if !reflect.DeepEqual(p.cleanup, expected) {
				t.Fatalf("cleanup %v", p.cleanup)
			}
		})
	}
}

func TestStartupInitialWriteErrorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &sessionProbe{cancel: cancel}
	cfg := config.DefaultConfig()
	cfg.MQTT.CommandsEnabled = false
	sentinel := errors.New("injected offline write failure")
	broker := startupMQTT{sessionMQTT: sessionMQTT{p}, writeErr: sentinel, cancel: cancel}
	var out bytes.Buffer
	app := New(cfg, broker, logger.NewSerial(&out, logger.INFO))
	app.moonrakerClient = sessionMoonraker{p}
	err := network.Session(ctx, func() bool { return true }, func(c context.Context) error { defer broker.Disconnect(); return app.Run(c) }, time.Millisecond, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want cancellation, got %v", err)
	}
	if !strings.Contains(out.String(), sentinel.Error()) {
		t.Fatal("initial write failure must already be logged")
	}
}

func TestDiagnosticStages(t *testing.T) {
	p := &sessionProbe{}
	cfg := config.DefaultConfig()
	cfg.MQTT.CommandsEnabled = false
	broker := startupMQTT{sessionMQTT: sessionMQTT{p}, connectErr: errors.New("offline")}
	app := New(cfg, broker, logger.NewSerial(&bytes.Buffer{}, logger.ERROR))
	mqttCtx, mqttCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer mqttCancel()
	if app.Run(mqttCtx) == nil || app.DiagnosticStage() != 1 {
		t.Fatal("MQTT startup stage missing")
	}
	broker.connectErr = nil
	app.mqttClient = broker
	app.moonrakerClient = startupMoon{sessionMoonraker: sessionMoonraker{p}, connectErr: errors.New("offline")}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := app.Run(ctx)
	if testMCUSessionPolicy && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Moonraker startup failure must wait original context, not return to reset: %v", err)
	}
	if err == nil || app.DiagnosticStage() != 2 {
		t.Fatal("Moonraker startup stage missing")
	}
}
