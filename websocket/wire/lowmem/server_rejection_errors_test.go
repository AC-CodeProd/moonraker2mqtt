package websocket

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

var errServerRejectionPolicy = errors.New("fixture policy rejection")

type rejectionTestConn struct {
	input         *bytes.Reader
	output        *clientHandshakeBudgetWriter
	closes, reads int
}

func (c *rejectionTestConn) Read(p []byte) (int, error)     { c.reads++; return c.input.Read(p) }
func (c *rejectionTestConn) Write(p []byte) (int, error)    { return c.output.Write(p) }
func (c *rejectionTestConn) Close() error                   { c.closes++; return nil }
func (*rejectionTestConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (*rejectionTestConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (*rejectionTestConn) SetDeadline(time.Time) error      { return nil }
func (*rejectionTestConn) SetReadDeadline(time.Time) error  { return nil }
func (*rejectionTestConn) SetWriteDeadline(time.Time) error { return nil }

type rejectionHTTPWriter struct {
	conn    *rejectionTestConn
	buf     *bufio.ReadWriter
	hijacks int
}

func (*rejectionHTTPWriter) Header() http.Header { panic("unexpected HTTP header access") }
func (*rejectionHTTPWriter) WriteHeader(int)     { panic("unexpected HTTP response before hijack") }
func (*rejectionHTTPWriter) Write([]byte) (int, error) {
	panic("unexpected HTTP response before hijack")
}
func (w *rejectionHTTPWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacks++
	return w.conn, w.buf, nil
}

func serverRejectionFixture(t *testing.T, kind string, budget, bufferSize int, writeErr error) (*http.Request, *rejectionHTTPWriter, func(*Config, *http.Request) error, string, error) {
	t.Helper()
	method, version, protocol := "GET", "13", ""
	var hook func(*Config, *http.Request) error
	var cause error
	code, extra, body := 400, "", ""
	switch kind {
	case "version":
		version, cause = "9", ErrBadWebSocketVersion
		extra = "Sec-WebSocket-Version: " + SupportedProtocolVersion + "\r\n"
	case "method":
		method, code, cause = "POST", 405, ErrBadRequestMethod
	case "policy":
		code, cause = 403, errServerRejectionPolicy
		hook = func(*Config, *http.Request) error { return errServerRejectionPolicy }
	case "selection":
		protocol, cause = "Sec-WebSocket-Protocol: chat, superchat\r\n", ErrBadWebSocketProtocol
	case "success":
	default:
		t.Fatalf("unknown fixture %q", kind)
	}
	if kind == "version" || kind == "method" {
		body = cause.Error()
	}
	expected := fmt.Sprintf("HTTP/1.1 %03d %s\r\n", code, http.StatusText(code)) + extra + "\r\n" + body
	if kind == "success" {
		expected = "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n"
	}
	request := method + " /chat HTTP/1.1\r\nHost: server.example.invalid\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: " + version + "\r\n" + protocol + "\r\n"
	wire := append([]byte(request), []byte{0x81, 0x85, 0xcc, 0x55, 0x80, 0x20, 0xa4, 0x30, 0xec, 0x4c, 0xa3}...)
	conn := &rejectionTestConn{input: bytes.NewReader(wire), output: &clientHandshakeBudgetWriter{remaining: budget, err: writeErr}}
	buf := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriterSize(conn, bufferSize))
	req, err := http.ReadRequest(buf.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return req, &rejectionHTTPWriter{conn: conn, buf: buf}, hook, expected, cause
}

func TestServerRejectionWriteErrors(t *testing.T) {
	transportErr := errors.New("rejection transport failure")
	for _, kind := range []string{"version", "method", "policy", "selection"} {
		t.Run(kind, func(t *testing.T) {
			_, _, _, expected, _ := serverRejectionFixture(t, kind, 0, 16, transportErr)
			for _, size := range []int{16, 4096} {
				for _, short := range []bool{false, true} {
					for offset := 0; offset <= len(expected); offset++ {
						injected, want := transportErr, transportErr
						if short {
							injected, want = io.ErrShortWrite, io.ErrShortWrite
							// Exercise malformed short/nil writes at Flush, not the
							// direct-write path that relies on the io.Writer contract.
							if size == 4096 {
								injected = nil
							}
						}
						req, w, hook, _, cause := serverRejectionFixture(t, kind, offset, size, injected)
						conn, err := newServerConn(w.conn, w.buf, req, &Config{}, hook)
						if conn != nil || !errors.Is(err, cause) || w.conn.output.String() != expected[:offset] {
							t.Fatalf("size=%d short=%t offset=%d: conn=%v error=%v bytes=%q", size, short, offset, conn, err, w.conn.output.String())
						}
						if offset == len(expected) {
							if err != cause {
								t.Fatalf("healthy rejection changed cause identity: %v", err)
							}
						} else if !errors.Is(err, want) {
							t.Fatalf("size=%d short=%t offset=%d: lost transport error: %v", size, short, offset, err)
						}
					}
				}
			}
		})
	}
}

func TestServerRejectionServeHTTP(t *testing.T) {
	for _, kind := range []string{"version", "method", "policy", "selection", "success"} {
		t.Run(kind, func(t *testing.T) {
			_, _, _, expected, _ := serverRejectionFixture(t, kind, 0, 16, io.ErrClosedPipe)
			for _, budget := range []int{0, len(expected) / 2, len(expected)} {
				req, w, hook, _, _ := serverRejectionFixture(t, kind, budget, 16, io.ErrClosedPipe)
				calls := 0
				server := Server{Handshake: hook, Handler: func(conn *Conn) {
					calls++
					var message string
					if err := Message.Receive(conn, &message); err != nil || message != "hello" {
						t.Errorf("prefetched message=%q error=%v", message, err)
					}
				}}
				server.ServeHTTP(w, req)
				wantCalls := 0
				if kind == "success" && budget == len(expected) {
					wantCalls = 1
				}
				if calls != wantCalls || w.hijacks != 1 || w.conn.closes != 1 || w.conn.reads != 1 || w.conn.output.String() != expected[:budget] {
					t.Fatalf("budget=%d: handler=%d hijacks=%d closes=%d reads=%d response prefix match=%t", budget, calls, w.hijacks, w.conn.closes, w.conn.reads, w.conn.output.String() == expected[:budget])
				}
			}
		})
	}
}

func TestServerRejectionAfterAcceptWriteFailure(t *testing.T) {
	want := errors.New("HTTP101 transport failure")
	for _, budget := range []int{0, 4, 40} {
		req, w, hook, expected, _ := serverRejectionFixture(t, "success", budget, 16, want)
		conn, err := newServerConn(w.conn, w.buf, req, &Config{}, hook)
		if conn != nil || !errors.Is(err, want) || w.conn.output.String() != expected[:budget] || strings.Count(w.conn.output.String(), "HTTP/1.1") > 1 {
			t.Fatalf("budget=%d: conn=%v error=%v response=%q", budget, conn, err, w.conn.output.String())
		}
	}
}
