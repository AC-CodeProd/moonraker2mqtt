package websocket

import (
	"bufio"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func serverHandshakeFixture(protocol bool) (*hybiServerHandshaker, string, []int) {
	h := &hybiServerHandshaker{Config: &Config{}, accept: []byte("s3pPLMBiTxaQ9kYGzzhZRbK+xOo=")}
	lines := []string{"HTTP/1.1 101 Switching Protocols\r\n", "Upgrade: websocket\r\n", "Connection: Upgrade\r\n", "Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n"}
	if protocol {
		h.Protocol = []string{"chat"}
		lines = append(lines, "Sec-WebSocket-Protocol: chat\r\n")
	}
	response := ""
	var offsets []int
	for _, line := range lines {
		offsets = append(offsets, len(response))
		response += line
	}
	pad := strings.Repeat("p", (15-(len(response)+len("X-Pad: \r\n"))%16+16)%16)
	h.Header = http.Header{"Upgrade": {"must-not-duplicate"}, "X-Pad": {pad}}
	response += "X-Pad: " + pad + "\r\n"
	offsets = append(offsets, len(response))
	response += "\r\n"
	return h, response, offsets
}

func TestServerHandshakeResponseWriteFailures(t *testing.T) {
	want := errors.New("server handshake transport write failure")
	_, _, offsets := serverHandshakeFixture(true)
	for i, name := range []string{"status", "upgrade", "connection", "accept", "protocol", "terminator"} {
		t.Run(name, func(t *testing.T) {
			h, _, _ := serverHandshakeFixture(true)
			w := &clientHandshakeBudgetWriter{remaining: offsets[i], err: want}
			if err := h.AcceptHandshake(bufio.NewWriterSize(w, 16)); !errors.Is(err, want) || w.Len() != offsets[i] {
				t.Fatalf("error=%v response bytes=%d, want first transport error and %d bytes", err, w.Len(), offsets[i])
			}
		})
	}
}

func TestServerHandshakeFailedWriterStopsBeforeAllocatingHeaders(t *testing.T) {
	want := errors.New("already failed transport")
	h, _, _ := serverHandshakeFixture(true)
	w := &clientHandshakeBudgetWriter{err: want}
	bw := bufio.NewWriterSize(w, 16)
	if _, err := bw.WriteString(strings.Repeat("x", 32)); !errors.Is(err, want) {
		t.Fatalf("failed writer not reached: %v", err)
	}
	allocs := testing.AllocsPerRun(100, func() {
		if err := h.AcceptHandshake(bw); !errors.Is(err, want) {
			panic("transport error lost")
		}
	})
	if allocs != 0 {
		t.Fatalf("failed response still built headers: %g host allocations per call", allocs)
	}
	if w.Len() != 0 {
		t.Fatal("bytes emitted after transport failure")
	}
}

func TestServerHandshakeResponseWirePreserved(t *testing.T) {
	for _, protocol := range []bool{false, true} {
		h, expected, _ := serverHandshakeFixture(protocol)
		w := &clientHandshakeBudgetWriter{remaining: len(expected), err: errors.New("unexpected extra write")}
		if err := h.AcceptHandshake(bufio.NewWriterSize(w, 16)); err != nil {
			t.Fatal(err)
		}
		if w.String() != expected {
			t.Fatalf("protocol=%t: HTTP101 response or header filtering changed", protocol)
		}
	}
}

func TestServerHandshakeRejectsUnselectedProtocolsBeforeWriting(t *testing.T) {
	h, _, _ := serverHandshakeFixture(true)
	h.Protocol = []string{"chat", "superchat"}
	w := &clientHandshakeBudgetWriter{err: errors.New("must not write")}
	if err := h.AcceptHandshake(bufio.NewWriterSize(w, 16)); err != ErrBadWebSocketProtocol || w.Len() != 0 {
		t.Fatalf("error=%v bytes=%d, want protocol rejection before response", err, w.Len())
	}
}
