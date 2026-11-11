package moonraker

import (
	"context"
	"io"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"moonraker2mqtt/websocket"
	"reflect"
	"testing"
)

type stateMock struct {
	websocket.Client
	typedCalls, genericCalls int
	objects                  map[string]any
}

func (m *stateMock) RequestKlippyState(context.Context) (string, error) {
	m.typedCalls++
	return "ready", nil
}
func (m *stateMock) Request(_ context.Context, method string, params any) (*websocket.WebSocketResponse, error) {
	m.genericCalls++
	if method == "printer.objects.query" {
		m.objects = params.(map[string]any)["objects"].(map[string]any)
		return &websocket.WebSocketResponse{Result: map[string]any{"status": map[string]any{}}}, nil
	}
	return &websocket.WebSocketResponse{Result: map[string]any{"klippy_state": "ready", "components": []any{"a", "b"}, "warnings": []any{"w"}, "moonraker_version": "offline", "websocket_count": float64(2), "klippy_connected": true}}, nil
}

type genericStateMock struct{ websocket.Client }

func (genericStateMock) Request(context.Context, string, any) (*websocket.WebSocketResponse, error) {
	return &websocket.WebSocketResponse{Result: map[string]any{"klippy_state": "fallback"}}, nil
}
func TestKlippyPlatformDispatchAndStartupInfo(t *testing.T) {
	m := &stateMock{}
	c := &Client{wsClient: m}
	state, err := c.GetKlippyState(context.Background())
	if err != nil || state != "ready" {
		t.Fatal(state, err)
	}
	real := websocket.NewWebSocketClient(&config.MoonrakerConfig{}, nil, logger.NewSerial(io.Discard, logger.ERROR))
	_, mcu := any(real).(interface {
		RequestKlippyState(context.Context) (string, error)
	})
	if mcu && (m.typedCalls != 1 || m.genericCalls != 0) || !mcu && (m.typedCalls != 0 || m.genericCalls != 1) {
		t.Fatal("wrong platform dispatch")
	}
	info, err := c.GetServerInfo(context.Background())
	if err != nil || !info.KlippyConnected || info.MoonrakerVersion != "offline" || info.WebsocketCount != 2 || !reflect.DeepEqual(info.Components, []string{"a", "b"}) || !reflect.DeepEqual(info.Warnings, []string{"w"}) {
		t.Fatalf("startup info truncated: %#v %v", info, err)
	}
	c.wsClient = genericStateMock{}
	state, err = c.GetKlippyState(context.Background())
	if state != "fallback" || err != nil {
		t.Fatal("generic adapter fallback changed")
	}
}
func TestQueryObjectsReadOnlySelection(t *testing.T) {
	m := &stateMock{}
	c := &Client{wsClient: m}
	selection := map[string]any{"all": nil, "toolhead": []any{"position", "é"}}
	_, err := c.QueryObjects(context.Background(), selection)
	if err != nil || !reflect.DeepEqual(selection, map[string]any{"all": nil, "toolhead": []any{"position", "é"}}) || !reflect.DeepEqual(m.objects, selection) {
		t.Fatal("read-only selection contract broken")
	}
}
