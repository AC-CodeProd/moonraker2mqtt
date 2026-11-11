//go:build lowmem && !tinygo

package websocket

import (
	"encoding/json"
	websocket "moonraker2mqtt/websocket/wire/lowmem"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestMoonrakerReservedReceiveRetainedAndInterleaved(t *testing.T) {
	if _, ok := any(new(WebSocketMessage)).(json.Unmarshaler); ok {
		t.Fatal("reserved decoding requires ownership audit for custom UnmarshalJSON")
	}
	if _, ok := any(new(RPCError)).(json.Unmarshaler); ok {
		t.Fatal("RPC error custom decoding needs ownership audit")
	}
	server := httptest.NewServer(websocket.Handler(func(conn *websocket.Conn) {
		defer conn.Close()
		body := `{"method":"notify","params":{"text":"saved"},"result":["retained"],"error":{"message":"error"}}`
		body += strings.Repeat(" ", 13456-len(body))
		for _, body := range []string{body, `{"method":"next","params":{"text":"replacement"}}`} {
			if err := websocket.Message.Send(conn, body); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := websocket.Dial("ws"+strings.TrimPrefix(server.URL, "http"), "", server.URL)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			configureConnection(conn)
			var first, second WebSocketMessage
			if err = receiveMessage(conn, &first); err != nil {
				t.Error(err)
				return
			}
			if err = receiveMessage(conn, &second); err != nil {
				t.Error(err)
				return
			}
			if first.Method != "notify" || first.Params.(map[string]any)["text"] != "saved" || first.Result.([]any)[0] != "retained" || first.Error.Message != "error" || second.Method != "next" {
				t.Errorf("ownership lost: %#v %#v", first, second)
			}
		}()
	}
	wg.Wait()
}
