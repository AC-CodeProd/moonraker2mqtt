//go:build lowmem && !tinygo

package websocket

import (
	"context"
	"encoding/json"
	"errors"
	wire "moonraker2mqtt/websocket/wire/lowmem"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Both sides register a real request, borrow RX storage, dispatch the reply, and
// overwrite that storage before inspecting the returned state/error/metadata.
func collisionPipeline(t *testing.T, body string, typed bool) (string, error) {
	t.Helper()
	c := typedTestClient()
	c.state = WEB_SOCKET_STATE_CONNECTED
	c.lifecycle.generation = &connectionGeneration{stop: make(chan struct{})}
	replies := make(chan string, 1)
	server := httptest.NewServer(wire.Handler(func(conn *wire.Conn) {
		defer conn.Close()
		response := <-replies
		for _, frame := range []string{
			`{"method":"unhandled","params":{"text":"notification"}}`,
			`{"id":99999,"result":{"klippy_state":"unrelated"}}`,
			response,
			`{"method":"replacement","params":{"text":"overwritten"}}`,
		} {
			if err := wire.Message.Send(conn, frame); err != nil {
				return
			}
		}
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
		req := <-c.sendChan
		replies <- strings.ReplaceAll(body, "@ID@", strconv.Itoa(*req.ID))
		var retained WebSocketMessage
		for i := 0; i < 4; i++ {
			var response WebSocketMessage
			if err := c.receiveMessage(conn, &response); err != nil {
				t.Errorf("borrowed receive: %v", err)
				return
			}
			if i == 2 {
				retained = response
			}
			if i < 3 {
				c.handleMessage(&response)
			}
		}
		if retained.Params != nil && !reflect.DeepEqual(retained.Params, map[string]any{"held": []any{"prêt 🌡", nil}}) {
			t.Errorf("params aliased: %#v", retained.Params)
		}
		if retained.Error != nil && !reflect.DeepEqual(retained.Error.Data, map[string]any{"deep": []any{"saved", nil}}) {
			t.Errorf("error data aliased: %#v", retained.Error)
		}
	}()
	var state string
	if typed {
		state, err = c.RequestKlippyState(context.Background())
	} else {
		var message *WebSocketResponse
		message, err = c.Request(context.Background(), "server.info", nil)
		if err == nil {
			var data []byte
			data, err = json.Marshal(message.Result)
			if err == nil {
				var info ServerInfo
				err = json.Unmarshal(data, &info)
				state = info.KlippyState
			}
		}
	}
	<-done
	if len(c.requests) != 0 {
		t.Fatal("request retained after dispatch")
	}
	return state, err
}

func TestKlippyCollisionDifferentialPipeline(t *testing.T) {
	cases := []struct{ name, body string }{
		{"null-last", `"result":{"klippy_state":"ready","klippy_state":null}`},
		{"empty-result-last", `"result":{"klippy_state":"ready"},"result":{}`},
		{"null-result-last", `"result":{"klippy_state":"ready"},"result":null`},
		{"result-case-last", `"result":{"klippy_state":"ready"},"RESULT":{}`},
		{"result-escaped-last", `"result":{"klippy_state":"ready"},"\u0072esult":{}`},
		{"state-case", `"result":{"klippy_state":"shutdown","KLIPPY_STATE":"ready"}`},
		{"state-case-reversed", `"result":{"KLIPPY_STATE":"ready","klippy_state":"shutdown"}`},
		{"state-case-null", `"result":{"klippy_state":"shutdown","KLIPPY_STATE":null}`},
		{"case-only", `"result":{"KLIPPY_STATE":"ready"}`},
		{"escaped-null", `"result":{"klippy_state":"ready","\u006blippy_state":null}`},
		{"escaped-case", `"result":{"klippy_state":"shutdown","\u004bLIPPY_STATE":"ready"}`},
		{"kelvin", `"result":{"klippy_state":"shutdown","Klippy_state":"ready"}`},
		{"long-s", `"result":{"klippy_state":"shutdown","klippy_ſtate":"ready"}`},
		{"unicode-escaped", `"result":{"klippy_state":"shutdown","\u212alippy_state":"ready"}`},
		{"dupe-valid", `"result":{"klippy_state":"ready","klippy_state":"shutdown"}`},
		{"dupe-wrong-type-overwritten", `"result":{"klippy_state":12,"klippy_state":"ready"}`},
		{"result-wrong-overwritten", `"result":[],"result":{"klippy_state":"ready"}`},
		{"result-null-first", `"result":null,"result":{"klippy_state":"ready"}`},
		{"nested-decoy", `"result":{"klippy_state":"ready","unknown":{"klippy_state":null,"result":{},"KLIPPY_STATE":1},"array":[{"result":{},"klippy_state":null}]}`},
		{"strings-decoy", `"result":{"klippy_state":"prêt 🌡","ignored":"\"klippy_state\":null,\"result\":{}\\"}`},
		{"escaped-normal", `"\u0072esult":{"\u006blippy_state":"ready"}`},
		{"normal", `"result":{"klippy_state":"ready"}`},
		{"empty", `"result":{}`},
		{"null", `"result":null`},
		{"missing", `"unknown":{}`},
		{"wrong-type", `"result":{"klippy_state":12}`},
		{"wrong-array", `"result":[]`},
		{"repeated-id", `"id":123456,"id":@ID@,"result":{"klippy_state":"ready","klippy_state":null}`},
		{"maximum-collision", `"result":{"klippy_state":"ready","klippy_state":null,"ignored":"` + strings.Repeat("x", 15000) + `"}`},
		{"error-collision", `"result":{"klippy_state":"ready","klippy_state":null},"error":{"code":-321,"message":"failed","data":{"deep":["saved",null]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"id":@ID@,` + tc.body + `,"params":{"held":["prêt 🌡",null]}}`
			if tc.name == "maximum-collision" {
				body += strings.Repeat(" ", 16384-len(strings.ReplaceAll(body, "@ID@", "1")))
			}
			want, werr := collisionPipeline(t, body, false)
			got, gerr := collisionPipeline(t, body, true)
			var wt, gt *json.UnmarshalTypeError
			var wr, gr *RPCError
			sameErr := (werr == nil && gerr == nil) || (errors.As(werr, &wt) && errors.As(gerr, &gt)) || (errors.As(werr, &wr) && errors.As(gerr, &gr) && reflect.DeepEqual(wr, gr))
			if got != want || !sameErr {
				t.Fatalf("baseline state=%q err=%v; typed state=%q err=%v", want, werr, got, gerr)
			}
		})
	}
}
