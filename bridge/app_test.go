package bridge

import (
	"bytes"
	"encoding/json"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"moonraker2mqtt/mqtt"
	"testing"
)

type publication struct {
	topic   string
	payload []byte
	qos     byte
	retain  bool
	retries int
}
type recordingMQTT struct {
	connected bool
	messages  []publication
}

func (m *recordingMQTT) Connect() error    { m.connected = true; return nil }
func (m *recordingMQTT) Disconnect() error { m.connected = false; return nil }
func (m *recordingMQTT) IsConnected() bool { return m.connected }
func (m *recordingMQTT) Publish(topic string, payload []byte, qos byte, retain bool, retries int) error {
	m.messages = append(m.messages, publication{topic, append([]byte(nil), payload...), qos, retain, retries})
	return nil
}
func (*recordingMQTT) Subscribe(string, mqtt.MessageHandler) error { return nil }
func (*recordingMQTT) Unsubscribe(string) error                    { return nil }
func TestStatePublicationPlatformIndependent(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MQTT.Retain = true
	cfg.MQTT.QoS = 1
	broker := &recordingMQTT{connected: true}
	var out bytes.Buffer
	app := New(cfg, broker, logger.NewSerial(&out, logger.INFO))
	app.OnStateChanged("ws_connected")
	if len(broker.messages) != 1 {
		t.Fatal("state not published")
	}
	p := broker.messages[0]
	if p.topic != "moonraker/state" || string(p.payload) != "ws_connected" || p.qos != 1 || !p.retain || p.retries != 3 {
		t.Fatalf("publication changed: %#v", p)
	}
	broker.connected = false
	app.OnStateChanged("ws_stopped")
	if len(broker.messages) != 1 {
		t.Fatal("published while disconnected")
	}
}
func TestNotificationPublicationPlatformIndependent(t *testing.T) {
	cfg := config.DefaultConfig()
	broker := &recordingMQTT{connected: true}
	var out bytes.Buffer
	app := New(cfg, broker, logger.NewSerial(&out, logger.INFO))
	app.OnNotification("notify_status_update", map[string]any{"state": "ready"})
	if len(broker.messages) != 1 || broker.messages[0].topic != "moonraker/notifications/notify_status_update" {
		t.Fatal("notification topic changed")
	}
	var data map[string]any
	if err := json.Unmarshal(broker.messages[0].payload, &data); err != nil || data["state"] != "ready" {
		t.Fatal("notification payload changed")
	}
	app.OnNotification("invalid", make(chan int))
	if len(broker.messages) != 1 {
		t.Fatal("invalid JSON published")
	}
}
func TestConfigurationCopy(t *testing.T) {
	var out bytes.Buffer
	cfg := config.DefaultConfig()
	app := New(cfg, &recordingMQTT{}, logger.NewSerial(&out, logger.INFO))
	snapshot := app.Configuration()
	snapshot.MQTT.Host = "changed"
	if app.Configuration().MQTT.Host != "localhost" {
		t.Fatal("configuration accessor leaked mutable state")
	}
}
