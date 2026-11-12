//go:build lowmem && natiu && !tinygo

package bridge

import (
	"errors"
	"fmt"
	"io"
	wire "moonraker2mqtt/websocket/wire/lowmem"
	"net"
	"os"
	"testing"
	"time"
)

func TestPipelinePeerShutdownClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{nil, false}, {io.EOF, true}, {net.ErrClosed, true}, {fmt.Errorf("wrapped: %w", net.ErrClosed), true},
		{os.ErrDeadlineExceeded, false}, {io.ErrUnexpectedEOF, false}, {io.ErrShortWrite, false}, {errors.New("arbitrary failure"), false},
	} {
		if got := pipelinePeerShutdown(tc.err); got != tc.want {
			t.Fatalf("shutdown(%v)=%t want %t", tc.err, got, tc.want)
		}
	}
}

func TestPipelineHTTPFixtureJoinsOwnedConnections(t *testing.T) {
	s := &fakeMoonService{address: "127.0.0.1:0"}
	defer s.stop(t)
	for range 3 {
		s.start(t)
		c, err := wire.Dial("ws://"+s.address+"/websocket", "", "http://"+s.address)
		if err != nil {
			t.Fatal(err)
		}
		if err := wire.JSON.Send(c, map[string]any{"id": 1, "method": "server.info"}); err != nil {
			pipelineClose(t, c)
			t.Fatal(err)
		}
		var reply map[string]any
		if err := wire.JSON.Receive(c, &reply); err != nil {
			pipelineClose(t, c)
			t.Fatal(err)
		}
		idle, err := net.Dial("tcp", s.address)
		if err != nil {
			pipelineClose(t, c)
			t.Fatal(err)
		}
		deadline := time.Now().Add(time.Second)
		for {
			s.mu.Lock()
			count := len(s.connections)
			s.mu.Unlock()
			if count == 2 {
				break
			}
			if time.Now().After(deadline) {
				pipelineClose(t, idle)
				pipelineClose(t, c)
				t.Fatal("HTTP fixture did not register both raw and hijacked connections")
			}
			time.Sleep(time.Millisecond)
		}
		s.stop(t)
		pipelineClose(t, idle)
		pipelineClose(t, c)
		s.mu.Lock()
		remaining := len(s.connections)
		s.mu.Unlock()
		if remaining != 0 {
			t.Fatal("HTTP fixture retained owned connections", remaining)
		}
	}
}

func TestPipelineWriterPropagatesCaptureFailure(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "capture-")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	w := &pipelineWriter{f: f, lines: make(chan string, 1)}
	n, err := w.Write([]byte("fixture record\n"))
	if n != 0 || !errors.Is(err, os.ErrClosed) {
		t.Fatalf("capture failure hidden: n=%d error=%v", n, err)
	}
	select {
	case <-w.lines:
		t.Fatal("failed capture emitted a successful observer record")
	default:
	}
}
