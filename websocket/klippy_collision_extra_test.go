//go:build lowmem && !tinygo

package websocket

import (
	"context"
	"encoding/json"
	"errors"
	wire "moonraker2mqtt/websocket/wire/lowmem"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestKlippyCollisionScannerBoundedAndValidated(t *testing.T) {
	for _, tc := range []struct {
		body     string
		fallback bool
	}{
		{`{"id":7,"result":{"klippy_state":"ready"}}`, false},
		{`{"id":7,"result":{"klippy_state":"ready","unknown":{"klippy_state":null,"result":{}},"list":[{"KLIPPY_STATE":null}]}}`, false},
		{`{"id":7,"result":{"klippy_state":"ready","klippy_state":null}}`, true},
		{`{"id":7,"result":{},"\u0072esult":{}}`, true},
		{`{"id":7,"result":{"\u006blippy_state":"ready"}}`, false},
		{`{"id":7,"result":{"Klippy_state":"ready"}}`, true},
		{`{"id":7,"result":{"klippy_ſtate":"ready"}}`, true},
		{`{"id":7,"result":{"klippy_state":"ready","ignored":"\\\"{[}]}"}}`, false},
		{`{"id":7,"result":{"klippy_state":"ready","ignored":` + strings.Repeat(`[`, 9000) + `null` + strings.Repeat(`]`, 9000) + `}}`, false},
	} {
		data := []byte(tc.body)
		if !json.Valid(data) {
			t.Fatalf("invalid test fixture: %q", tc.body)
		}
		if got := klippyNeedsRoundtrip(data); got != tc.fallback {
			t.Fatalf("scan got=%v want=%v for %q", got, tc.fallback, tc.body)
		}
	}
	normal := []byte(`{"id":7,"result":{"klippy_state":"ready","unknown":[{"x":1}]}}`)
	escaped := []byte(`{"id":7,"result":{"\u006blippy_state":"ready"}}`)
	a := testing.AllocsPerRun(100, func() {
		if klippyNeedsRoundtrip(normal) {
			panic("normal fallback")
		}
	})
	e := testing.AllocsPerRun(100, func() {
		if klippyNeedsRoundtrip(escaped) {
			panic("escaped exact fallback")
		}
	})
	t.Logf("Go-host detector normal=%g escaped-key=%g allocs/op; stack metadata is not MCU peak RAM", a, e)
	if a != 0 {
		t.Fatal("normal detector allocated")
	}
	c := typedTestClient()
	markState(c, 7)
	for _, body := range []string{
		`{"id":7,"result":{"klippy_state":"ready",}}`,
		`{"id":7,"result":{"\q":"ready"}}`,
		`{"id":7,"result":{"x":[1,]}}`,
		`{"id":7,"result":{"x":` + strings.Repeat(`[`, 10001) + `null` + strings.Repeat(`]`, 10001) + `}}`,
		`{"id":"7","result":{"klippy_state":"ready"}}`,
		`{"id":7.5,"result":{}}`,
	} {
		var message WebSocketMessage
		if err := c.decodeMessage([]byte(body), 1, &message); err == nil {
			t.Fatal("primary decoder accepted invalid envelope")
		}
	}
}

func TestKlippyCollisionCancelledRequestPreflight(t *testing.T) {
	c := typedTestClient()
	c.state = WEB_SOCKET_STATE_CONNECTED
 c.lifecycle.generation = &connectionGeneration{stop:make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.RequestKlippyState(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := c.Request(ctx, "server.info", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(c.requests) != 0 || len(c.sendChan) != 0 {
		t.Fatal("cancelled request reached RX registration/enqueue")
	}
}

func TestKlippyCollisionBorrowedOutOfOrder(t *testing.T) {
	c := typedTestClient()
	c.state = WEB_SOCKET_STATE_CONNECTED
 c.lifecycle.generation = &connectionGeneration{stop:make(chan struct{})}
	replies := make(chan []*WebSocketMessage, 1)
	server := httptest.NewServer(wire.Handler(func(conn *wire.Conn) {
		defer conn.Close()
		requests := <-replies
		_ = wire.Message.Send(conn, `{"method":"unhandled","params":[{"owned":"notify"}]}`)
		for i := len(requests) - 1; i >= 0; i-- {
			req := requests[i]
			_ = wire.Message.Send(conn, `{"id":`+strconv.Itoa(*req.ID)+`,"result":{"klippy_state":"ready","klippy_state":null,"components":["full"]}}`)
		}
		_ = wire.Message.Send(conn, `{"id":99999,"result":{"klippy_state":"late"}}`)
	}))
	defer server.Close()
	conn, err := wire.Dial("ws"+strings.TrimPrefix(server.URL, "http"), "", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	configureConnection(conn)
	done := make(chan struct{})
	go func() {
		defer close(done)
		first, second := <-c.sendChan, <-c.sendChan
		replies <- []*WebSocketMessage{first, second}
		for i := 0; i < 4; i++ {
			var msg WebSocketMessage
			if err := c.receiveMessage(conn, &msg); err != nil {
				t.Error(err)
				return
			}
			c.handleMessage(&msg)
		}
	}()
	typedDone := make(chan struct{})
	go func() {
		defer close(typedDone)
		state, err := c.RequestKlippyState(context.Background())
		if state != "" || err != nil {
			t.Errorf("typed=%q err=%v", state, err)
		}
	}()
	full, err := c.Request(context.Background(), "server.info", nil)
	if err != nil {
		t.Fatal(err)
	}
	<-typedDone
	<-done
	if full.Result.(map[string]any)["components"].([]any)[0] != "full" || full.Result.(map[string]any)["klippy_state"] != nil {
		t.Fatal("generic response altered/aliased")
	}
	if len(c.requests) != 0 {
		t.Fatal("request map not drained")
	}
}

// Measure the real request registration -> decode -> dispatch API for exceptional
// collisions too. This fake RX benchmark is Go-host, not MCU or network memory.
func BenchmarkKlippyCollisionRequestOffline(b *testing.B) {
	for _, body := range []struct{ name, result string }{
		{"normal", `{"klippy_state":"ready","components":["a","b","c"]}`},
		{"duplicate-null", `{"klippy_state":"ready","klippy_state":null,"components":["a","b","c"]}`},
		{"escaped-normal", `{"\u006blippy_state":"ready","components":["a","b","c"]}`},
	} {
		b.Run(body.name, func(b *testing.B) {
			c := typedTestClient()
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
						data := []byte(`{"id":` + strconv.Itoa(*req.ID) + `,"result":` + body.result + `}`)
						var msg WebSocketMessage
						if err := c.decodeMessage(data, 1, &msg); err != nil {
							panic(err)
						}
						c.handleMessage(&msg)
					}
				}
			}()
			defer func() { close(c.closeChan); <-done }()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := c.RequestKlippyState(context.Background()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
