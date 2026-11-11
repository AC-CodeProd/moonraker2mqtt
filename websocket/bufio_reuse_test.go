//go:build lowmem && !tinygo

package websocket

import (
	"context"
	"io"
	wire "moonraker2mqtt/websocket/wire/lowmem"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func bufioBackingProfile() map[string]int64 {
	runtime.GC()
	runtime.GC()
	n, _ := runtime.MemProfile(nil, true)
	rec := make([]runtime.MemProfileRecord, n+64)
	n, ok := runtime.MemProfile(rec, true)
	if !ok {
		panic("HARNESS memprofile size changed")
	}
	counts := map[string]int64{"Reader": 0, "Writer": 0}
	for _, r := range rec[:n] {
		if r.AllocObjects == 0 || r.AllocBytes/r.AllocObjects != 4096 {
			continue
		}
		frames := runtime.CallersFrames(r.Stack())
		client := false
		kind := ""
		for {
			f, more := frames.Next()
			if strings.Contains(f.Function, "/wire/lowmem.NewClient") || strings.Contains(f.Function, "/wire/lowmem.newClientWithBuffers") || strings.Contains(f.Function, "/wire/lowmem.(*DialBuffers).Init") {
				client = true
			}
			if strings.HasPrefix(f.Function, "bufio.NewReader") {
				kind = "Reader"
			}
			if strings.HasPrefix(f.Function, "bufio.NewWriter") {
				kind = "Writer"
			}
			if !more {
				break
			}
		}
		if client && kind != "" {
			counts[kind] += r.AllocObjects
		}
	}
	return counts
}

// Same source on OLD and NEW. It counts BOTH real 4096 backing allocations,
// not constructor grep or a zero-allocation claim for the whole handshake.
func TestBufioActualLifecycleBackingOnceSuccessFailureGC(t *testing.T) {
	oldRate := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = oldRate }()
	var fail atomic.Bool
	peers := make(chan *wire.Conn, 1)
	h := wire.Handler(func(p *wire.Conn) { defer p.Close(); peers <- p; var m string; _ = wire.Message.Receive(p, &m) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(403)
			return
		}
		h.ServeHTTP(w, r)
	}))
	defer server.Close()
	before := bufioBackingProfile()
	c := recoveryClient(t, server.URL)
	defer c.Disconnect()
	var retained []any
	var first [5]uintptr
	stable := true
	for i := 0; i < 6; i++ {
		fail.Store(true)
		if e := c.Connect(context.Background()); e == nil {
			t.Fatal("403 accepted")
		}
		runtime.GC()
		fail.Store(false)
		if e := c.Connect(context.Background()); e != nil {
			t.Fatal(e)
		}
		peer := <-peers
		conn := c.conn
		retained = append(retained, conn)
		pair := reflect.ValueOf(conn).Elem().FieldByName("buf")
		reader := pair.Elem().FieldByName("Reader")
		writer := pair.Elem().FieldByName("Writer")
		rb := reader.Elem().FieldByName("buf")
		wb := writer.Elem().FieldByName("buf")
		got := [5]uintptr{pair.Pointer(), reader.Pointer(), writer.Pointer(), rb.Pointer(), wb.Pointer()}
		if rb.Cap() != 4096 || wb.Cap() != 4096 {
			t.Fatalf("capacities %d/%d", rb.Cap(), wb.Cap())
		}
		if i == 0 {
			first = got
		} else if first != got {
			stable = false
		}
		_ = peer.AbortTransport()
		waitRecoveryState(t, func() bool { return !c.IsConnected() })
		runtime.GC()
	}
	after := bufioBackingProfile()
	runtime.KeepAlive(retained)
	r, w := after["Reader"]-before["Reader"], after["Writer"]-before["Writer"]
	t.Logf("actual path: six403 + six101 + forcedGC: Reader backing allocations=%d Writer=%d stable pair/controls/backing=%v", r, w, stable)
	if r == 0 || w == 0 {
		t.Fatal("HARNESS allocation profiler did not reach both backing constructors")
	}
	if r != 1 || w != 1 || !stable {
		t.Fatalf("recurring bufio storage: Reader=%d Writer=%d stable=%v", r, w, stable)
	}
}

// Both old generation joins, not merely transport Close, gate the next dial.
func TestBufioLifecycleBothJoinsBeforeNextDial(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(403) }))
	defer server.Close()
	c := recoveryClient(t, server.URL)
	defer c.Disconnect()
	g := &connectionGeneration{stop: make(chan struct{}), readDone: make(chan struct{}), writeDone: make(chan struct{})}
	// Already stopped transport; emulate old I/O tasks not yet returned.
	g.once.Do(func() { close(g.stop) })
	c.lifecycle.generation = g
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() { close(entered); done <- c.Connect(context.Background()) }()
	<-entered
	// Use the held lifecycle lock itself as deterministic entry acknowledgement.
	for c.lifecycle.mu.TryLock() {
		c.lifecycle.mu.Unlock()
		runtime.Gosched()
	}
	if calls.Load() != 0 {
		t.Fatal("dial before read join")
	}
	close(g.readDone)
	runtime.Gosched()
	if calls.Load() != 0 {
		t.Fatal("dial before write join")
	}
	close(g.writeDone)
	if e := <-done; e == nil {
		t.Fatal("403 accepted")
	}
	if calls.Load() != 1 {
		t.Fatal("next dial not reached")
	}
}

var _ = io.EOF
