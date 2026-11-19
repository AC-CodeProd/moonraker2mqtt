package websocket

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

type clientHandshakeBudgetWriter struct {
	output    bytes.Buffer
	remaining int
	err       error
}

func (w *clientHandshakeBudgetWriter) Write(p []byte) (int, error) {
	n := len(p)
	if n > w.remaining {
		n = w.remaining
	}
	if _, err := w.output.Write(p[:n]); err != nil {
		return 0, err
	}
	w.remaining -= n
	if n < len(p) {
		return n, w.err
	}
	return n, nil
}

func (w *clientHandshakeBudgetWriter) Len() int       { return w.output.Len() }
func (w *clientHandshakeBudgetWriter) String() string { return w.output.String() }

func TestClientHandshakeBudgetWriterFaultInjection(t *testing.T) {
	want := errors.New("budget exhausted")
	w := &clientHandshakeBudgetWriter{remaining: 1, err: want}
	if n, err := io.WriteString(w, "test"); n != 1 || !errors.Is(err, want) || w.String() != "t" {
		t.Fatalf("WriteString bypassed injection: n=%d error=%v bytes=%q", n, err, w.String())
	}
}

type clientHandshakeResponseReader struct {
	*bytes.Reader
	calls int
}

func (r *clientHandshakeResponseReader) Read(p []byte) (int, error) {
	r.calls++
	return r.Reader.Read(p)
}

func clientHandshakeFixture(t *testing.T) (*Config, string, []int) {
	t.Helper()
	cfg, err := NewConfig("ws://server.example.invalid/chat?x=1", "http://example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	cfg.handshakeData = map[string]string{"key": "dGhlIHNhbXBsZSBub25jZQ=="}
	cfg.Protocol = []string{"chat", "superchat"}
	lines := []string{
		"GET /chat?x=1 HTTP/1.1\r\n",
		"Host: server.example.invalid\r\n",
		"Upgrade: websocket\r\n",
		"Connection: Upgrade\r\n",
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n",
		"Origin: http://example.invalid\r\n",
		"Sec-WebSocket-Version: 13\r\n",
		"Sec-WebSocket-Protocol: chat, superchat\r\n",
	}
	var offsets []int
	request := ""
	for _, line := range lines {
		offsets = append(offsets, len(request))
		request += line
	}
	// Align the terminating CRLF to force a buffer flush in its WriteString.
	pad := strings.Repeat("p", (15-(len(request)+len("X-Pad: \r\n"))%16+16)%16)
	cfg.Header.Set("X-Pad", pad)
	request += "X-Pad: " + pad + "\r\n"
	offsets = append(offsets, len(request))
	request += "\r\n"
	return cfg, request, offsets
}

func TestClientHandshakeRequestWriteFailures(t *testing.T) {
	want := errors.New("client handshake transport write failure")
	_, _, offsets := clientHandshakeFixture(t)
	names := []string{"request", "host", "upgrade", "connection", "key", "origin", "version", "protocol", "terminator"}
	for i, offset := range offsets {
		t.Run(names[i], func(t *testing.T) {
			for _, invalidVersion := range []bool{false, true} {
				cfg, _, _ := clientHandshakeFixture(t)
				if invalidVersion {
					cfg.Version = 99
				}
				// Version rejection intentionally precedes later headers.
				if invalidVersion && i >= 6 {
					continue
				}
				writer := &clientHandshakeBudgetWriter{remaining: offset, err: want}
				response := &clientHandshakeResponseReader{Reader: bytes.NewReader(nil)}
				err := hybiClientHandshake(cfg, bufio.NewReader(response), bufio.NewWriterSize(writer, 16))
				if !errors.Is(err, want) || response.calls != 0 || writer.Len() != offset {
					t.Fatalf("invalid version=%t: error=%v response reads=%d request bytes=%d; want first write error, no response read, %d bytes", invalidVersion, err, response.calls, writer.Len(), offset)
				}
			}
		})
	}
}

func TestClientHandshakeWireAndPrefetchPreserved(t *testing.T) {
	cfg, expected, _ := clientHandshakeFixture(t)
	response := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\nSec-WebSocket-Protocol: chat\r\n\r\n"
	input := &clientHandshakeResponseReader{Reader: bytes.NewReader(append([]byte(response), []byte{0x81, 5, 'h', 'e', 'l', 'l', 'o'}...))}
	output := &clientHandshakeBudgetWriter{remaining: len(expected), err: io.ErrClosedPipe}
	br, bw := bufio.NewReader(input), bufio.NewWriterSize(output, 16)
	if err := hybiClientHandshake(cfg, br, bw); err != nil {
		t.Fatal(err)
	}
	if output.String() != expected || strings.Join(cfg.Protocol, ",") != "chat" || input.calls != 1 || br.Buffered() == 0 {
		t.Fatalf("handshake changed: request match=%t protocol=%v reads=%d buffered=%d", output.String() == expected, cfg.Protocol, input.calls, br.Buffered())
	}
	ws := newHybiClientConn(cfg, bufio.NewReadWriter(br, bw), nil)
	var got string
	if err := Message.Receive(ws, &got); err != nil || got != "hello" || input.calls != 1 {
		t.Fatalf("prefetched frame=%q error=%v transport reads=%d", got, err, input.calls)
	}
}

func TestClientHandshakeBadVersionWithoutWriteFailure(t *testing.T) {
	cfg, _, _ := clientHandshakeFixture(t)
	cfg.Version = 99
	var output bytes.Buffer
	if err := hybiClientHandshake(cfg, bufio.NewReader(bytes.NewReader(nil)), bufio.NewWriter(&output)); err != ErrBadProtocolVersion {
		t.Fatalf("bad version error=%v, want %v", err, ErrBadProtocolVersion)
	}
}
