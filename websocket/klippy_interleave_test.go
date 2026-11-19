//go:build lowmem && !tinygo

package websocket

import (
	"context"
	"strconv"
	"testing"
)

func TestKlippyOutOfOrderSimultaneousRequests(t *testing.T) {
	c := typedTestClient()
	c.state = WEB_SOCKET_STATE_CONNECTED
	c.lifecycle.generation = &connectionGeneration{stop: make(chan struct{})}
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		first, second := <-c.sendChan, <-c.sendChan
		for _, req := range []*WebSocketMessage{second, first} {
			var message WebSocketMessage
			body := []byte(`{"id":` + strconv.Itoa(*req.ID) + `,"result":{"klippy_state":"ready","components":["full"]}}`)
			if err := c.decodeMessage(body, 1, &message); err != nil {
				panic(err)
			}
			c.handleMessage(&message)
		}
	}()
	typedDone := make(chan struct{})
	go func() {
		defer close(typedDone)
		state, err := c.RequestKlippyState(context.Background())
		if err != nil || state != "ready" {
			t.Errorf("typed state=%q err=%v", state, err)
		}
	}()
	full, err := c.Request(context.Background(), "server.info", nil)
	if err != nil {
		t.Fatal(err)
	}
	if full.Result.(map[string]any)["components"].([]any)[0] != "full" {
		t.Fatal("generic RPC was truncated by typed request")
	}
	<-typedDone
	<-workerDone
	if len(c.requests) != 0 {
		t.Fatal("completed request IDs retained")
	}
}
