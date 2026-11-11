//go:build lowmem && !tinygo

package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
)

func TestKlippyTypedNullShapeAndNilContext(t *testing.T) {
	for _, tc := range []struct {
		result string
		bad    bool
	}{{`null`, false}, {`{}`, false}, {`{"klippy_state":null}`, false}, {`{"klippy_state":12}`, true}, {`[]`, true}, {`"bad"`, true}} {
		t.Run(tc.result, func(t *testing.T) {
			c := typedTestClient()
			c.state = WEB_SOCKET_STATE_CONNECTED
			c.lifecycle.generation = &connectionGeneration{stop: make(chan struct{})}
			done := make(chan struct{})
			go func() {
				defer close(done)
				req := <-c.sendChan
				var response WebSocketMessage
				data := []byte(`{"id":` + strconv.Itoa(*req.ID) + `,"result":` + tc.result + `}`)
				if err := c.decodeMessage(data, 1, &response); err != nil {
					panic(err)
				}
				c.handleMessage(&response)
			}()
			state, err := c.RequestKlippyState(context.Background())
			<-done
			if tc.bad {
				var typeErr *json.UnmarshalTypeError
				if !errors.As(err, &typeErr) {
					t.Fatalf("wrong result must return type error, got %v", err)
				}
			} else if state != "" || err != nil {
				t.Fatalf("zero state=%q err=%v", state, err)
			}
		})
	}
	c := typedTestClient()
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("nil context semantics changed")
			}
		}()
		_, _ = c.RequestKlippyState(nil)
	}()
}
