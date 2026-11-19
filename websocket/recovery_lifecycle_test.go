//go:build lowmem && !tinygo

package websocket

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	wire "moonraker2mqtt/websocket/wire/lowmem"
)

func recoveryClient(t *testing.T, address string) *WebSocketClient {
	t.Helper()
	u, e := url.Parse(address)
	if e != nil {
		t.Fatal(e)
	}
	p, e := strconv.Atoi(u.Port())
	if e != nil {
		t.Fatal(e)
	}
	return NewWebSocketClient(&config.MoonrakerConfig{Host: u.Hostname(), Port: p}, nil, logger.NewSerial(io.Discard, logger.ERROR))
}
func waitRecoveryState(t *testing.T, condition func() bool) {
	t.Helper()
	until := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(until) {
			t.Fatal("state transition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}
func actualWorkerIDs() []string {
	b := make([]byte, 1<<18)
	n := runtime.Stack(b, true)
	var ids []string
	for _, g := range strings.Split(string(b[:n]), "\n\n") {
		if strings.Contains(g, "moonraker2mqtt/websocket.(*WebSocketClient).reusableReadWorker") || strings.Contains(g, "moonraker2mqtt/websocket.(*WebSocketClient).reusableWriteWorker") || strings.Contains(g, "moonraker2mqtt/websocket.(*WebSocketClient).reusableCancelWorker") {
			ids = append(ids, strings.Fields(strings.Split(g, "\n")[0])[1])
		}
	}
	sort.Strings(ids)
	return ids
}
func TestRecoveryFortyActualCyclesStableWorkersAndRetainedHeap(t *testing.T) {
	peers := make(chan *wire.Conn, 1)
	server := bufioFixtureServer(t, wire.Handler(func(p *wire.Conn) {
		defer closeBufioPeer(t, p)
		peers <- p
		for {
			var m WebSocketMessage
			if wire.JSON.Receive(p, &m) != nil {
				return
			}
			if wire.JSON.Send(p, map[string]any{"id": m.ID, "jsonrpc": "2.0", "result": map[string]any{"klippy_state": "ready"}}) != nil {
				return
			}
		}
	}))
	defer server.Close()
	before := actualWorkerIDs()
	c := recoveryClient(t, server.URL)
	defer disconnectBufioFixture(t, c)
	var starts []string
	var readStart, writeStart, cancelStart any
	var retained10, retained40 uint64
	for cycle := 1; cycle <= 40; cycle++ {
		if e := c.Connect(context.Background()); e != nil {
			t.Fatalf("connect cycle%d: %v", cycle, e)
		}
		peer := <-peers
		if state, e := c.RequestKlippyState(context.Background()); e != nil || state != "ready" {
			t.Fatalf("request cycle%d: %q %v", cycle, state, e)
		}
		if cycle == 1 {
			starts = actualWorkerIDs()
			if len(starts)-len(before) != 3 {
				t.Fatalf("actual workers delta=%d want3", len(starts)-len(before))
			}
			readStart, writeStart, cancelStart = c.lifecycle.readStart, c.lifecycle.writeStart, c.lifecycle.cancelStart
		} else if fmt.Sprint(starts) != fmt.Sprint(actualWorkerIDs()) || readStart != c.lifecycle.readStart || writeStart != c.lifecycle.writeStart || cancelStart != c.lifecycle.cancelStart {
			t.Fatal("workers or worker dispatch channels replaced")
		}
		if e := peer.Close(); e != nil {
			t.Fatal(e)
		}
		waitRecoveryState(t, func() bool { return !c.IsConnected() })
		c.requestsMux.Lock()
		pending := len(c.requests)
		c.requestsMux.Unlock()
		if pending != 0 {
			t.Fatalf("pending retained=%d", pending)
		}
		if cycle == 10 || cycle == 40 {
			runtime.GC()
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			if cycle == 10 {
				retained10 = m.HeapAlloc
			} else {
				retained40 = m.HeapAlloc
			}
		}
	}
	if retained40 > retained10+256*1024 {
		t.Fatalf("retained heap growth above host regression bound: %d -> %d", retained10, retained40)
	}
	if e := c.Disconnect(); e != nil {
		t.Fatal(e)
	}
	waitRecoveryState(t, func() bool { return fmt.Sprint(actualWorkerIDs()) == fmt.Sprint(before) })
	t.Logf("HOST_ONLY actual worker starts=3 stable IDs across40 cycles, final joined3; heapAlloc after GC cycle10=%d cycle40=%d; not MCU headroom", retained10, retained40)
}

func TestRecoveryHandshakeCancellationAndFailedHTTPDrainSockets(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	var accepted, closed atomic.Int64
	var wg sync.WaitGroup
	acceptDone := make(chan struct{})
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			if err := listener.Close(); err != nil {
				t.Errorf("close recovery listener: %v", err)
			}
			// Stop Accept before waiting: it owns all handler registrations.
			<-acceptDone
			wg.Wait()
		})
	}
	t.Cleanup(stop)
	go func() {
		defer close(acceptDone)
		for {
			p, e := listener.Accept()
			if e != nil {
				if !errors.Is(e, net.ErrClosed) {
					t.Errorf("accept recovery peer: %v", e)
				}
				return
			}
			accepted.Add(1)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer closed.Add(1)
				defer func() {
					if err := p.Close(); err != nil {
						t.Errorf("close recovery handshake peer: %v", err)
					}
				}()
				if err := p.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
					t.Errorf("set recovery peer deadline: %v", err)
					return
				}
				if _, err := io.Copy(io.Discard, p); err != nil {
					t.Errorf("drain recovery handshake peer: %v", err)
				}
			}()
		}
	}()
	c := recoveryClient(t, "http://"+listener.Addr().String())
	defer disconnectBufioFixture(t, c)
	for i := 0; i < 12; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		start := time.Now()
		e = c.Connect(ctx)
		cancel()
		if e == nil {
			t.Fatal("incomplete HTTP handshake accepted")
		}
		if time.Since(start) > time.Second {
			t.Fatal("cancelled handshake cleanup not bounded")
		}
		if c.IsConnected() {
			t.Fatal("cancelled connection remained active")
		}
	}
	if e = c.Disconnect(); e != nil {
		t.Fatal(e)
	}
	stop()
	if accepted.Load() != 12 || accepted.Load() != closed.Load() {
		t.Fatalf("sockets accepted=%d closed=%d", accepted.Load(), closed.Load())
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer bad.Close()
	d := recoveryClient(t, bad.URL)
	defer disconnectBufioFixture(t, d)
	for i := 0; i < 8; i++ {
		if e = d.Connect(context.Background()); e == nil {
			t.Fatal("failed HTTP status accepted")
		}
	}
	t.Logf("cancelled handshakes=%d closed=%d; rejected HTTP403=8", accepted.Load(), closed.Load())
}

func TestRecoveryConcurrentCancellationPendingQueueDrain(t *testing.T) {
	peers := make(chan *wire.Conn, 1)
	server := bufioFixtureServer(t, wire.Handler(func(p *wire.Conn) {
		defer closeBufioPeer(t, p)
		peers <- p
		for {
			var m WebSocketMessage
			if wire.JSON.Receive(p, &m) != nil {
				return
			}
		}
	}))
	defer server.Close()
	c := recoveryClient(t, server.URL)
	defer disconnectBufioFixture(t, c)
	if e := c.Connect(context.Background()); e != nil {
		t.Fatal(e)
	}
	peer := <-peers
	var wg sync.WaitGroup
	var succeeded atomic.Int64
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			if _, e := c.Request(ctx, "server.info", nil); e == nil {
				succeeded.Add(1)
			}
		}()
	}
	wg.Wait()
	if succeeded.Load() != 0 {
		t.Fatal("request without response succeeded")
	}
	c.requestsMux.Lock()
	pending := len(c.requests)
	c.requestsMux.Unlock()
	if pending != 0 {
		t.Fatalf("cancelled pending=%d", pending)
	}
	closeBufioPeer(t, peer)
	waitRecoveryState(t, func() bool { return !c.IsConnected() })
	if e := c.Connect(context.Background()); e != nil {
		t.Fatal(e)
	}
	peer = <-peers
	if len(c.sendChan) != 0 {
		t.Fatal("old send queue retained")
	}
	closeBufioPeer(t, peer)
}
