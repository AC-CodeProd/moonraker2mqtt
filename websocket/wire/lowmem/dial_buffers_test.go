package websocket

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Synchronous transport fixture: response and all frames arrive in ONE read.
// Only the real cryptographic nonce in the real emitted request supplies Accept.
type reuseHTTP struct {
	mode          string
	input         *bytes.Reader
	writes        bytes.Buffer
	req           *http.Request
	reads, closes int
}

func (p *reuseHTTP) Read(b []byte) (int, error) {
	p.reads++
	if p.input == nil {
		return 0, io.EOF
	}
	return p.input.Read(b)
}
func (p *reuseHTTP) Write(b []byte) (int, error) {
	if p.mode == "shortwrite" {
		return len(b) - 1, nil
	}
	if p.req != nil {
		return p.writes.Write(b)
	}
	p.writes.Write(b)
	r, e := http.ReadRequest(bufio.NewReader(bytes.NewReader(p.writes.Bytes())))
	if e != nil {
		return 0, e
	}
	p.req = r
	accept, _ := getNonceAccept([]byte(r.Header.Get("Sec-WebSocket-Key")))
	status, upgrade, extra := "101 Switching Protocols", "websocket", ""
	switch p.mode {
	case "403":
		status = "403 Forbidden"
	case "badAccept":
		accept = []byte("bad")
	case "Upgrade":
		upgrade = "wrong"
	case "extensions":
		extra = "Sec-WebSocket-Extensions: unsupported\r\n"
	case "subprotocol":
		extra = "Sec-WebSocket-Protocol: unoffered\r\n"
	case "EOF":
		p.input = bytes.NewReader(nil)
		return len(b), nil
	}
	response := fmt.Sprintf("HTTP/1.1 %s\r\nUpgrade: %s\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n%s\r\n", status, upgrade, accept, extra)
	var frames bytes.Buffer
	appendServerFrame(&frames, TextFrame, []byte("first"))
	appendServerFrame(&frames, PingFrame, []byte("ping"))
	appendServerFrame(&frames, TextFrame, []byte("second"))
	p.input = bytes.NewReader(append([]byte(response), frames.Bytes()...))
	return len(b), nil
}
func (p *reuseHTTP) Close() error { p.closes++; return nil }
func backingIDs(b *DialBuffers) [5]uintptr {
	return [5]uintptr{reflect.ValueOf(b.pair).Pointer(), reflect.ValueOf(b.pair.Reader).Pointer(), reflect.ValueOf(b.pair.Writer).Pointer(), reflect.ValueOf(b.pair.Reader).Elem().FieldByName("buf").Pointer(), reflect.ValueOf(b.pair.Writer).Elem().FieldByName("buf").Pointer()}
}
func TestDialBuffersFailuresPrefetchNonceIsolationGC(t *testing.T) {
	old := FrameProofDiagnostics
	defer func() { FrameProofDiagnostics = old }()
	for _, diag := range []string{"false", "true"} {
		t.Run(diag, func(t *testing.T) {
			FrameProofDiagnostics = diag
			b := &DialBuffers{}
			b.Init()
			first := backingIDs(b)
			other := &DialBuffers{}
			other.Init()
			if first[3] == backingIDs(other)[3] || first[4] == backingIDs(other)[4] {
				t.Fatal("shared clients")
			}
			keys := map[string]bool{}
			ids := map[uint64]bool{}
			for cycle := 0; cycle < 3; cycle++ {
				for _, mode := range []string{"403", "badAccept", "Upgrade", "extensions", "subprotocol", "EOF", "shortwrite", "success"} {
					t.Run(fmt.Sprintf("%d/%s", cycle, mode), func(t *testing.T) {
						b.Release()
						runtime.GC()
						cfg, _ := NewConfig("ws://fixture.invalid/ws", "http://fixture.invalid")
						cfg.Header.Set("X-Api-Key", "secret-fixture")
						p := &reuseHTTP{mode: mode}
						ws, e := newClientWithBuffers(cfg, p, b)
						if mode != "shortwrite" {
							if p.req == nil {
								t.Fatal("request not reached")
							}
							k := p.req.Header.Get("Sec-WebSocket-Key")
							if k == "" || keys[k] {
								t.Fatal("nonce missing/reused")
							}
							keys[k] = true
							if p.req.Header.Get("X-Api-Key") != "secret-fixture" {
								t.Fatal("auth dropped")
							}
						}
						if mode != "success" {
							if e == nil {
								t.Fatal("invalid handshake accepted")
							}
							b.Release()
							if b.pair.Reader.Buffered() != 0 || b.pair.Writer.Buffered() != 0 {
								t.Fatal("failure bytes retained")
							}
						} else {
							if e != nil {
								t.Fatal(e)
							}
							if b.pair.Reader.Buffered() == 0 || p.reads != 1 {
								t.Fatalf("prefetch not reached buffered=%d reads=%d", b.pair.Reader.Buffered(), p.reads)
							}
							for _, want := range []string{"first", "second"} {
								var got string
								if e = Message.Receive(ws, &got); e != nil || got != want {
									t.Fatalf("prefetched frame %q: %q %v", want, got, e)
								}
							}
							if p.reads != 1 {
								t.Fatal("prefetched bytes lost; extra transport read")
							}
							if !bytes.Contains(p.writes.Bytes()[strings.Index(p.writes.String(), "\r\n\r\n")+4:], []byte{0x8a}) {
								t.Fatal("pong absent")
							}
							if diag == "true" {
								pr := ws.frameReaderFactory.(hybiFrameReaderFactory).proof
								if pr == nil {
									t.Fatal("proof wrapper missing")
								}
								id := reflect.ValueOf(pr).Elem().FieldByName("id").Uint()
								if id == 0 || ids[id] {
									t.Fatal("proof stream identity reused")
								}
								ids[id] = true
							}
						}
						b.Release()
						if p.closes != 0 {
							t.Fatal("buffer release closed transport")
						}
						if backingIDs(b) != first || b.pair.Reader.Size() != 4096 || b.pair.Writer.Size() != 4096 {
							t.Fatal("storage replaced")
						}
					})
				}
			}
		})
	}
}

func TestDialBuffersReleaseDiscardsErrorPendingWithoutFlush(t *testing.T) {
	b := &DialBuffers{}
	b.Init()
	ids := backingIDs(b)
	bad := &reuseHTTP{mode: "shortwrite"}
	b.pair.Writer.Reset(bad)
	_, _ = b.pair.Writer.WriteString("old bytes")
	if b.pair.Writer.Flush() != io.ErrShortWrite {
		t.Fatal("shortwrite not reached")
	}
	b.Release()
	good := &bytes.Buffer{}
	b.pair.Writer.Reset(good)
	if _, e := b.pair.Writer.WriteString("new"); e != nil {
		t.Fatal(e)
	}
	if e := b.pair.Writer.Flush(); e != nil {
		t.Fatal(e)
	}
	if good.String() != "new" || backingIDs(b) != ids {
		t.Fatal("stale error/data or new buffers")
	}
	b.pair.Writer.WriteString("unflushed old")
	b.Release()
	if good.String() != "new" {
		t.Fatal("Release flushed old bytes")
	}
}
