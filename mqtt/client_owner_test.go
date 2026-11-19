//go:build natiu && !tinygo

package mqtt

import (
	"bufio"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
)

// Hold a physical read after an actual first packet byte. RX owns an
// incomplete official decode, not an artificial mutex around HandleNext.
type ownerReadGate struct {
	net.Conn
	mu      sync.Mutex
	armed   bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	writes  chan byte
}

func (g *ownerReadGate) Read(p []byte) (int, error) {
	g.mu.Lock()
	armed := g.armed
	if armed {
		g.armed = false
	}
	g.mu.Unlock()
	if armed {
		close(g.entered)
		<-g.release
	}
	n, err := g.Conn.Read(p)
	if n == 1 && p[0] == 0x90 {
		g.mu.Lock()
		g.armed = true
		g.mu.Unlock()
	}
	return n, err
}
func (g *ownerReadGate) Write(p []byte) (int, error) {
	if len(p) > 0 {
		select {
		case g.writes <- p[0] >> 4:
		default:
		}
	}
	return g.Conn.Write(p)
}
func (g *ownerReadGate) unblock() { g.once.Do(func() { close(g.release) }) }

func TestClientOwnerRXErrorBeforeQueuedSubscribe(t *testing.T) {
	a, b := net.Pipe()
	raw := &ownerReadGate{Conn: a, entered: make(chan struct{}), release: make(chan struct{}), writes: make(chan byte, 16)}
	c := NewNatiuClient(config.DefaultConfig().MQTT, logger.NewSerial(io.Discard, logger.ERROR))
	c.dial = func(string, string) (net.Conn, error) { return raw, nil }
	ctx, cancel := context.WithCancel(context.Background())
	brokerDone := make(chan struct{})
	fragment := make(chan struct{})
	var fragmentOnce sync.Once
	startFragment := func() { fragmentOnce.Do(func() { close(fragment) }) }
	var subs sync.WaitGroup
	t.Cleanup(func() {
		startFragment()
		raw.unblock()
		cancel()
		_ = a.Close()
		_ = b.Close()
		subs.Wait()
		_ = c.Disconnect()
		<-brokerDone
	})
	go func() {
		defer close(brokerDone)
		r := bufio.NewReader(b)
		if _, err := readPacket(r); err != nil {
			return
		}
		if _, err := b.Write([]byte{0x20, 2, 0, 0}); err != nil {
			return
		}
		<-fragment
		// Invalid SUBACK length: the official RX error callback disconnects.
		if _, err := b.Write([]byte{0x90}); err != nil {
			return
		}
		if _, err := b.Write([]byte{4}); err != nil {
			return
		}
		for {
			if _, err := readPacket(r); err != nil {
				return
			}
		}
	}()
	if err := c.ConnectContext(ctx); err != nil {
		t.Fatal(err)
	}
	<-raw.writes // CONNECT
	startFragment()
	select {
	case <-raw.entered:
	case <-time.After(time.Second):
		t.Fatal("partial RX gate not reached")
	}
	result := make(chan error, 1)
	subs.Add(1)
	go func() { defer subs.Done(); result <- c.Subscribe("owner/topic", func(string, []byte) {}) }()
	select {
	case typ := <-raw.writes:
		t.Errorf("native TX %d overlapped incomplete RX", typ)
	case <-time.After(100 * time.Millisecond):
	}
	raw.unblock()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("RX error accepted queued subscribe")
		}
	case <-time.After(time.Second):
		t.Fatal("queued subscribe did not retire")
	}
	select {
	case <-c.done:
	case <-time.After(time.Second):
		t.Fatal("RX did not join")
	}
	if c.IsConnected() {
		t.Fatal("stale connected snapshot after RX error")
	}
}
