//go:build lowmem && !tinygo

package websocket

import (
	"bufio"
	"context"
	"io"
	wire "moonraker2mqtt/websocket/wire/lowmem"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type joinedIO struct {
	net.Conn
	armed                                                        atomic.Bool
	readEntered, writeEntered, readRelease, writeRelease, closed chan struct{}
	readOnce, writeOnce, closeOnce                               sync.Once
}

// Host seam qualifies lifecycle ordering only; native draining is separately
// exercised through real Berkeley/Conn barriers in the fork fixtures.
type joinedNativeOwner struct {
	readDone, writeDone <-chan struct{}
	calls               atomic.Int32
	premature           atomic.Bool
}

func (p *joinedNativeOwner) DialContext(ctx context.Context, n, a string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, n, a)
}
func (p *joinedNativeOwner) Retire() error {
	select {
	case <-p.readDone:
	default:
		p.premature.Store(true)
	}
	select {
	case <-p.writeDone:
	default:
		p.premature.Store(true)
	}
	p.calls.Add(1)
	return nil
}

func (p *joinedIO) Read(b []byte) (int, error) {
	if !p.armed.Load() {
		return p.Conn.Read(b)
	}
	p.readOnce.Do(func() { close(p.readEntered) })
	<-p.closed
	<-p.readRelease
	return 0, net.ErrClosed
}
func (p *joinedIO) Write(b []byte) (int, error) {
	if !p.armed.Load() {
		return p.Conn.Write(b)
	}
	p.writeOnce.Do(func() { close(p.writeEntered) })
	<-p.closed
	<-p.writeRelease
	return 0, net.ErrClosed
}
func (p *joinedIO) Close() error {
	p.closeOnce.Do(func() { close(p.closed); _ = p.Conn.Close() })
	return nil
}
func awaitBarrier(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("HARNESS barrier not reached")
	}
}
func waitLifecycleHeld(c *WebSocketClient) {
	for c.lifecycle.mu.TryLock() {
		c.lifecycle.mu.Unlock()
		runtime.Gosched()
	}
}

func TestBufioRealBlockedReadWriteJoinedBeforeResetDial(t *testing.T) {
	var nextDial atomic.Int32
	next := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { nextDial.Add(1); w.WriteHeader(403) }))
	defer next.Close()
	c := recoveryClient(t, next.URL)
	defer c.Disconnect()
	a, b := net.Pipe()
	defer b.Close()
	raw := &joinedIO{Conn: a, readEntered: make(chan struct{}), writeEntered: make(chan struct{}), readRelease: make(chan struct{}), writeRelease: make(chan struct{}), closed: make(chan struct{})}
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		req, e := http.ReadRequest(bufio.NewReader(b))
		if e != nil {
			return
		}
		// Let the authentic server handler produce the cryptographic response.
		h := wire.Handler(func(ws *wire.Conn) { _, _ = io.Copy(io.Discard, ws) })
		h.ServeHTTP(&pipeHijacker{conn: b, rw: bufio.NewReadWriter(bufio.NewReader(b), bufio.NewWriter(b))}, req)
	}()
	cfg, _ := wire.NewConfig("ws://fixture.invalid/", "http://fixture.invalid")
	conn, e := wire.NewClient(cfg, raw)
	if e != nil {
		t.Fatal(e)
	}
	raw.armed.Store(true)
	g := &connectionGeneration{conn: conn, stop: make(chan struct{}), readDone: make(chan struct{}), writeDone: make(chan struct{})}
	owned := &joinedNativeOwner{readDone: g.readDone, writeDone: g.writeDone}
	c.lifecycle.native = owned
	c.lifecycle.generation = g
	c.conn = conn
	c.state = WEB_SOCKET_STATE_CONNECTED
	go func() { c.readGeneration(g); close(g.readDone) }()
	go func() { c.writeGeneration(g); close(g.writeDone) }()
	c.sendChan <- &WebSocketMessage{Method: "old", JSONRPC: "2.0"}
	awaitBarrier(t, raw.readEntered)
	awaitBarrier(t, raw.writeEntered)
	c.stopGeneration(g)
	done := make(chan error, 1)
	go func() { done <- c.Connect(context.Background()) }()
	waitLifecycleHeld(c)
	if nextDial.Load() != 0 {
		t.Fatal("dial while both old operations live")
	}
	if owned.calls.Load() != 0 {
		t.Fatal("native retirement before old I/O joins")
	}
	close(raw.readRelease)
	awaitBarrier(t, g.readDone)
	if nextDial.Load() != 0 {
		t.Fatal("dial before old writer join")
	}
	if owned.calls.Load() != 0 {
		t.Fatal("native retirement before old writer join")
	}
	close(raw.writeRelease)
	awaitBarrier(t, g.writeDone)
	if e = <-done; e == nil {
		t.Fatal("403 accepted")
	}
	if nextDial.Load() != 1 {
		t.Fatal("next dial not reached after BOTH joins")
	}
	if owned.premature.Load() || owned.calls.Load() != 2 {
		t.Fatal("native retire ordering/count", owned.premature.Load(), owned.calls.Load())
	}
	awaitBarrier(t, serverDone)
}

type pipeHijacker struct {
	conn net.Conn
	rw   *bufio.ReadWriter
}

func (p *pipeHijacker) Header() http.Header                          { return http.Header{} }
func (p *pipeHijacker) WriteHeader(int)                              {}
func (p *pipeHijacker) Write(b []byte) (int, error)                  { return p.conn.Write(b) }
func (p *pipeHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) { return p.conn, p.rw, nil }

func TestBufioCanceledContextAndConcurrentConnectIsolation(t *testing.T) {
	peers := make(chan *wire.Conn, 2)
	server := httptest.NewServer(wire.Handler(func(p *wire.Conn) {
		defer p.Close()
		peers <- p
		for {
			var m WebSocketMessage
			if wire.JSON.Receive(p, &m) != nil {
				return
			}
			_ = wire.JSON.Send(p, map[string]any{"id": m.ID, "result": map[string]any{"klippy_state": "ready"}, "jsonrpc": "2.0"})
		}
	}))
	defer server.Close()
	c := recoveryClient(t, server.URL)
	defer c.Disconnect()
	before, cancelBefore := context.WithCancel(context.Background())
	cancelBefore()
	if e := c.Connect(before); e != context.Canceled {
		t.Fatalf("before TCP: %v", e)
	}
	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	results := make(chan error, 2)
	go func() { results <- c.Connect(first) }()
	go func() { results <- c.Connect(first) }()
	successes := 0
	for i := 0; i < 2; i++ {
		if <-results == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent Connect successes=%d", successes)
	}
	p := <-peers
	cancelFirst() // successful attempt is already joined: no late raw Close.
	if s, e := c.RequestKlippyState(context.Background()); e != nil || s != "ready" {
		t.Fatalf("cancel after success touched active stream: %q %v", s, e)
	}
	_ = p.AbortTransport()
	waitRecoveryState(t, func() bool { return !c.IsConnected() })
	if e := c.Connect(context.Background()); e != nil {
		t.Fatal(e)
	}
	p = <-peers
	cancelFirst()
	if s, e := c.RequestKlippyState(context.Background()); e != nil || s != "ready" {
		t.Fatalf("old cancel touched next stream: %q %v", s, e)
	}
	_ = p.AbortTransport()
}
