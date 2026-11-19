//go:build lowmem && natiu && !tinygo

package bridge

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	wire "moonraker2mqtt/websocket/wire/lowmem"
	"net"
	"os"
	"testing"
	"time"
)

type pipelinePacketWriter struct {
	n     int
	err   error
	calls int
	data  bytes.Buffer
}

func (w *pipelinePacketWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.n < 0 || w.n > len(p) {
		panic("invalid fixture count")
	}
	if _, err := w.data.Write(p[:w.n]); err != nil {
		return 0, err
	}
	return w.n, w.err
}

func TestPipelineBrokerPacketWrites(t *testing.T) {
	sentinel := errors.New("packet write failed")
	for _, packet := range [][]byte{{0x20, 2, 0, 0}, {0xd0, 0}} {
		for _, tc := range []struct {
			name      string
			n         int
			err, want error
		}{
			{"full", len(packet), nil, nil},
			{"short", len(packet) - 1, nil, io.ErrShortWrite},
			{"zero", 0, nil, io.ErrShortWrite},
			{"partial-error", 1, sentinel, sentinel},
			{"full-error", len(packet), sentinel, sentinel},
			{"closed-error", 0, net.ErrClosed, net.ErrClosed},
			{"timeout-error", 0, os.ErrDeadlineExceeded, os.ErrDeadlineExceeded},
		} {
			t.Run(fmt.Sprintf("%x/%s", packet[0], tc.name), func(t *testing.T) {
				w := &pipelinePacketWriter{n: tc.n, err: tc.err}
				err := writeOfflinePacket(w, packet)
				if !errors.Is(err, tc.want) || w.calls != 1 || !bytes.Equal(w.data.Bytes(), packet[:tc.n]) {
					t.Fatalf("write result lost: err=%v calls=%d bytes=%x", err, w.calls, w.data.Bytes())
				}
				if tc.err != nil && err != tc.err {
					t.Fatal("primary write error replaced")
				}
			})
		}
	}
}

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
