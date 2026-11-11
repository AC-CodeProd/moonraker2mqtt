//go:build natiu && !tinygo

package mqtt

import (
	"bufio"
	"io"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"net"
	"sync"
	"testing"
	"time"
)

type gracefulGateConn struct {
	net.Conn
	entered, release, closed chan struct{}
	once                     sync.Once
}

func (g *gracefulGateConn) Write(p []byte) (int, error) {
	if len(p) == 2 && p[0] == 0xe0 {
		close(g.entered)
		<-g.release
	}
	return g.Conn.Write(p)
}
func (g *gracefulGateConn) Close() error {
	g.once.Do(func() { close(g.closed) })
	return g.Conn.Close()
}
func TestSameBootGracefulDisconnectDoesNotCloseBeforeFinalWrite(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	wire := &gracefulGateConn{Conn: a, entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	c := NewNatiuClient(config.DefaultConfig().MQTT, logger.NewSerial(io.Discard, logger.ERROR))
	c.dial = func(string, string) (net.Conn, error) { return wire, nil }
	var release sync.Once
	unblock := func() { release.Do(func() { close(wire.release) }) }
	defer func() { unblock(); _ = c.Disconnect() }()
	peer := make(chan struct{})
	go func() {
		defer close(peer)
		defer b.Close()
		r := bufio.NewReader(b)
		if _, e := readPacket(r); e != nil {
			return
		}
		if _, e := b.Write([]byte{0x20, 2, 0, 0}); e != nil {
			return
		}
		_, _ = readPacket(r)
	}()
	if e := c.Connect(); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- c.Disconnect() }()
	select {
	case <-wire.entered:
	case <-time.After(time.Second):
		t.Fatal("final write not reached")
	}
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		t.Fatal("idle RX shutdown not reached")
	}
	// Let the durable RX worker run after readLoop's done closure.
	select {
	case <-wire.closed:
		t.Error("RX worker cancellation closed transport before DISCONNECT Write returned")
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	select {
	case e := <-done:
		if e != nil {
			t.Error(e)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal join did not return")
	}
	<-peer
}
