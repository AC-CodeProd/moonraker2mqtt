//go:build lowmem && !tinygo

package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	wire "moonraker2mqtt/websocket/wire/lowmem"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func typedTestClient() *WebSocketClient {
	return NewWebSocketClient(&config.MoonrakerConfig{}, nil, logger.NewSerial(io.Discard, logger.ERROR))
}
func markState(c *WebSocketClient, id int) {
	r := NewWebSocketRequest(id, "server.info", nil, 30)
	r.setKlippyState(true)
	c.requests[id] = r
}
func TestKlippyDecodeMixedOwnership(t *testing.T) {
	c := typedTestClient()
	markState(c, 7)
	c.requests[8] = NewWebSocketRequest(8, "server.info", nil, 30)
	for _, tc := range []struct {
		body  string
		typed bool
	}{
		{`{"id":8,"result":{"klippy_state":"generic","components":["owned"]}}`, false},
		{`{"method":"notify","params":[{"text":"prêt 🌡","nested":[null,1]}],"result":{"klippy_state":"notification"}}`, false},
		{`{"id":99,"result":{"klippy_state":"late"}}`, false},
		{`{"id":null,"result":{"klippy_state":"null id"}}`, false},
		{`{"id":7,"result":{"klippy_state":"prêt 🌡","unknown":{"maps":[1,2]}},"error":{"code":-1,"message":"échoué","data":{"deep":[null,"owned"]}},"params":{"held":"saved"}}`, true},
		{`{"id":8,"id":7,"result":{"klippy_state":"last id"}}`, true},
		{`{"ID":7,"result":null}`, true},
	} {
		data := []byte(tc.body)
		var got WebSocketMessage
		if err := c.decodeMessage(data, 1, &got); err != nil {
			t.Fatal(err)
		}
		_, typed := got.Result.(*klippyStateResult)
		if typed != tc.typed {
			t.Fatalf("wrong schema %s: %T", tc.body, got.Result)
		}
		before, _ := json.Marshal(got)
		for i := range data {
			data[i] = 'x'
		}
		after, _ := json.Marshal(got)
		if string(before) != string(after) {
			t.Fatal("borrowed alias retained")
		}
		if got.Error != nil && (got.Error.Code != -1 || got.Error.Message != "échoué" || !reflect.DeepEqual(got.Error.Data, map[string]any{"deep": []any{nil, "owned"}})) {
			t.Fatal("RPC error fields lost")
		}
	}
	delete(c.requests, 7)
	var late WebSocketMessage
	if err := c.decodeMessage([]byte(`{"id":7,"result":{"klippy_state":"late"}}`), 1, &late); err != nil {
		t.Fatal(err)
	}
	if _, ok := late.Result.(map[string]any); !ok {
		t.Fatal("late response still decoded typed")
	}
	for _, body := range []string{`{"id":"7","result":null}`, `{"id":7.5}`, `{"id":7`, `[]`} {
		var got WebSocketMessage
		if c.decodeMessage([]byte(body), 1, &got) == nil {
			t.Fatalf("invalid envelope accepted %s", body)
		}
	}
	for _, schema := range []any{new(klippyStateEnvelope), new(klippyStateResult), new(WebSocketMessage), new(RPCError)} {
		if _, ok := schema.(json.Unmarshaler); ok {
			t.Fatal("custom decoding needs new ownership audit")
		}
	}
}
func TestKlippyTypedBorrowedMaximum(t *testing.T) {
	c := typedTestClient()
	markState(c, 7)
	body := `{"id":7,"result":{"klippy_state":"prêt 🌡","ignored":"` + strings.Repeat("x", 15000) + `"},"params":{"owned":["saved"]},"error":{"code":-9,"message":"owned","data":["saved"]}}`
	body += strings.Repeat(" ", 16384-len(body))
	server := httptest.NewServer(wire.Handler(func(conn *wire.Conn) {
		defer conn.Close()
		_ = wire.Message.Send(conn, body)
		_ = wire.Message.Send(conn, `{"method":"replacement","params":{"owned":["changed"]}}`)
	}))
	defer server.Close()
	conn, err := wire.Dial("ws"+strings.TrimPrefix(server.URL, "http"), "", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	configureConnection(conn)
	var first, second WebSocketMessage
	if err := c.receiveMessage(conn, &first); err != nil {
		t.Fatal(err)
	}
	if err := c.receiveMessage(conn, &second); err != nil {
		t.Fatal(err)
	}
	if first.Result.(*klippyStateResult).KlippyState != "prêt 🌡" || first.Params.(map[string]any)["owned"].([]any)[0] != "saved" || first.Error.Data.([]any)[0] != "saved" {
		t.Fatal("typed borrowed ownership lost across maximum frame reuse")
	}
}

// Fake response transport, no production network; exercise actual registration,
// schema lookup, decode, response dispatch and Request APIs, not a map conversion.
func offlineResponder(c *WebSocketClient) func() {
	c.state = WEB_SOCKET_STATE_CONNECTED
 c.lifecycle.generation = &connectionGeneration{stop:make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-c.closeChan:
				return
			case req := <-c.sendChan:
				body := []byte(`{"id":` + strconv.Itoa(*req.ID) + `,"result":{"klippy_state":"ready","components":["a","b","c"],"warnings":["d"],"websocket_count":2,"unknown":{"items":[1,2,3]}}}`)
				var response WebSocketMessage
				if err := c.decodeMessage(body, 1, &response); err != nil {
					panic(err)
				}
				c.handleMessage(&response)
			}
		}
	}()
	return func() { close(c.closeChan); <-done }
}
func TestKlippyTypedRequestSemantics(t *testing.T) {
	c := typedTestClient()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.RequestKlippyState(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	_, err = c.RequestKlippyState(context.Background())
	if err == nil {
		t.Fatal("disconnected accepted")
	}
	c.state = WEB_SOCKET_STATE_CONNECTED
 c.lifecycle.generation = &connectionGeneration{stop:make(chan struct{})}
	for i := 0; i < maxPendingRequests; i++ {
		c.requests[i] = NewWebSocketRequest(i, "pending", nil, 30)
	}
	_, err = c.RequestKlippyState(context.Background())
	if err == nil || !strings.Contains(err.Error(), "pending request limit") {
		t.Fatal(err)
	}
	c.requests = map[int]*WebSocketRequest{}
	_, err = c.sendRequestWithTimeout("server.info", nil, time.Millisecond, true)
	if err == nil || len(c.requests) != 0 {
		t.Fatalf("timeout cleanup err=%v requests=%d", err, len(c.requests))
	}
	// Late response after timeout must stay generic, not acquire stale request kind.
	var late WebSocketMessage
	if err := c.decodeMessage([]byte(`{"id":1,"result":{"klippy_state":"late"}}`), 1, &late); err != nil {
		t.Fatal(err)
	}
	if _, ok := late.Result.(map[string]any); !ok {
		t.Fatal("stale kind")
	}
}
func TestKlippyTypedRequestRPCError(t *testing.T) {
	c := typedTestClient()
	c.state = WEB_SOCKET_STATE_CONNECTED
 c.lifecycle.generation = &connectionGeneration{stop:make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		req := <-c.sendChan
		var message WebSocketMessage
		body := []byte(`{"id":` + strconv.Itoa(*req.ID) + `,"error":{"code":-321,"message":"failed","data":{"owned":["saved",null]}}}`)
		if err := c.decodeMessage(body, 1, &message); err != nil {
			panic(err)
		}
		c.handleMessage(&message)
	}()
	_, err := c.RequestKlippyState(context.Background())
	<-done
	rpc, ok := err.(*RPCError)
	if !ok || rpc.Code != -321 || rpc.Message != "failed" || !reflect.DeepEqual(rpc.Data, map[string]any{"owned": []any{"saved", nil}}) {
		t.Fatalf("RPC error altered: %#v", err)
	}
}
func TestKlippyTypedAllocationComparison(t *testing.T) {
	c := typedTestClient()
	stop := offlineResponder(c)
	defer stop()
	typed := testing.AllocsPerRun(100, func() {
		state, err := c.RequestKlippyState(context.Background())
		if err != nil || state != "ready" {
			panic("typed state")
		}
	})
	generic := testing.AllocsPerRun(100, func() {
		msg, err := c.Request(context.Background(), "server.info", nil)
		if err != nil {
			panic(err)
		}
		data, err := json.Marshal(msg.Result)
		if err != nil {
			panic(err)
		}
		var info ServerInfo
		if err := json.Unmarshal(data, &info); err != nil {
			panic(err)
		}
	})
	t.Logf("offline Go-host lowmem actual request+fake RX typed=%g generic+roundtrip=%g allocs/op (not MCU)", typed, generic)
	if typed >= generic {
		t.Fatal("typed API did not reduce allocations")
	}
}
func BenchmarkKlippyRequestOffline(b *testing.B) {
	for _, typed := range []bool{false, true} {
		name := "generic-roundtrip"
		if typed {
			name = "typed"
		}
		b.Run(name, func(b *testing.B) {
			c := typedTestClient()
			stop := offlineResponder(c)
			defer stop()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if typed {
					_, _ = c.RequestKlippyState(context.Background())
				} else {
					msg, _ := c.Request(context.Background(), "server.info", nil)
					data, _ := json.Marshal(msg.Result)
					var info ServerInfo
					_ = json.Unmarshal(data, &info)
				}
			}
		})
	}
}
