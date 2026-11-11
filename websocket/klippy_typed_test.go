//go:build lowmem && !tinygo

package websocket

import (
	"context"
	"io"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	wire "moonraker2mqtt/websocket/wire/lowmem"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

type stateRequester interface {
	RequestKlippyState(context.Context) (string, error)
}

func TestKlippyTypedRequestOffline(t *testing.T) {
	cfg := &config.MoonrakerConfig{}
	c := NewWebSocketClient(cfg, nil, logger.NewSerial(io.Discard, logger.ERROR))
	typed, ok := any(c).(stateRequester)
	if !ok {
		t.Fatal("MCU concrete client lacks actual typed state request")
	}
	server := httptest.NewServer(wire.Handler(func(conn *wire.Conn) {
		defer conn.Close()
		var req WebSocketMessage
		if wire.JSON.Receive(conn, &req) != nil {
			return
		}
		_ = wire.Message.Send(conn, `{"jsonrpc":"2.0","id":1,"result":{"klippy_state":"prêt 🌡","components":["large ignored"],"unknown":{"nested":[1,2,3]}}}`)
	}))
	defer server.Close()
 // Use the public constructor and Connect so the real generation owns conn,
 // worker start channels and completion joins. Keep the original typed value
 // and ignored-field assertions; the malformed legacy fixture is preserved r4.
 u,err:=url.Parse(server.URL);if err!=nil{t.Fatal(err)}
 cfg.Host=u.Hostname();cfg.Port,err=strconv.Atoi(u.Port());if err!=nil{t.Fatal(err)}
 if err=c.Connect(context.Background());err!=nil{t.Fatal(err)}
	defer c.Disconnect()
	state, err := typed.RequestKlippyState(context.Background())
	if err != nil || state != "prêt 🌡" {
		t.Fatalf("state=%q err=%v", state, err)
	}
}
