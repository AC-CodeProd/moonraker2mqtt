//go:build lowmem && !tinygo

package bridge

import (
	"context"
	"io"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"reflect"
	"testing"
)

type statusMoon struct {
	sessionMoonraker
	objects map[string]any
}

func (m *statusMoon) QueryObjects(_ context.Context, objects map[string]any) (map[string]any, error) {
	m.objects = objects
	return map[string]any{"status": map[string]any{"print_stats": map[string]any{"state": "printing"}}, "eventtime": 1.5}, nil
}

type statusMQTT struct {
	sessionMQTT
	topics map[string]string
}

func (m *statusMQTT) Publish(topic string, payload []byte, _ byte, _ bool, _ int) error {
	if m.topics != nil {
		m.topics[topic] = string(payload)
	}
	return nil
}
func statusApp() (*App, *statusMoon, *statusMQTT) {
	cfg := config.DefaultConfig()
	cfg.MQTT.TopicPrefix = "offline"
	cfg.Moonraker.MonitoredObjects = `{"print_stats":null,"toolhead":["position"]}`
	m := &statusMoon{}
	q := &statusMQTT{topics: map[string]string{}}
	a := New(cfg, q, logger.NewSerial(io.Discard, logger.ERROR))
	a.moonrakerClient = m
	return a, m, q
}
func TestStatusCacheSnapshot(t *testing.T) {
	a, m, q := statusApp()
	if err := a.publishStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := m.objects
	a.config.Moonraker.MonitoredObjects = `{"replacement":null}`
	a.config.MQTT.TopicPrefix = "changed"
	if err := a.publishStatus(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, m.objects) {
		t.Fatal("selection reparsed after stable MCU configuration boundary")
	}
	if q.topics["changed/klipper/state"] != "" || q.topics["offline/objects/status"] != `{"print_stats":{"state":"printing"}}` || len(q.topics) != 2 {
		t.Fatalf("publication topics/payload changed: %v", q.topics)
	}
}
func TestStatusCacheAllocations(t *testing.T) {
	a, _, q := statusApp()
	q.topics = nil
	_ = a.publishStatus(context.Background())
	n := testing.AllocsPerRun(100, func() {
		if err := a.publishStatus(context.Background()); err != nil {
			panic(err)
		}
	})
	t.Logf("offline Go-host lowmem real publishStatus allocs/op=%g", n)
	if n > 25 {
		t.Fatalf("repeated selection/topic allocations remain: %g", n)
	}
}
func BenchmarkStatusPublishOffline(b *testing.B) {
	a, _, q := statusApp()
	q.topics = nil
	_ = a.publishStatus(context.Background())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = a.publishStatus(context.Background())
	}
}
