//go:build natiu && !tinygo

package mqtt

import (
	"errors"
	native "github.com/soypat/natiu-mqtt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
	"unsafe"
)

type diagTestConn struct {
	net.Conn
	mu     sync.Mutex
	seterr error
	err    error
	writes int
	closes int
	closed bool
}

func (c *diagTestConn) SetWriteDeadline(time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seterr
}
func (c *diagTestConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes++
	if c.closed {
		return 0, net.ErrClosed
	}
	if c.err != nil {
		return 0, c.err
	}
	return len(p), nil
}
func (c *diagTestConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closes++
	c.closed = true
	return nil
}
func enableDiag(t *testing.T) {
	t.Helper()
	old := TransportDiagnostics
	TransportDiagnostics = "true"
	t.Cleanup(func() { TransportDiagnostics = old })
}
func state(c *packetConn) (uint32, bool, bool, bool) {
	c.diag.mu.Lock()
	defer c.diag.mu.Unlock()
	return c.diag.seq, c.diag.writeSeen, c.diag.setterSeen, c.diag.closeSeen
}

func TestTransportFailureBeforeClose(t *testing.T) {
	enableDiag(t)
	sentinel := errors.New("injected")
	raw := &diagTestConn{err: sentinel}
	c := &packetConn{Conn: raw}
	if err := c.writePublish([]byte("test"), []byte("synthetic"), false); err != sentinel {
		t.Fatal("changed error")
	}
	seq, w, s, cl := state(c)
	if seq != 3 || !w || s || !cl || raw.closes != 1 {
		t.Fatal("events or close changed", seq)
	}
	t.Log("REACHED failure-before-close")
}
func TestTransportCloseBeforeError(t *testing.T) {
	enableDiag(t)
	raw := &diagTestConn{}
	c := &packetConn{Conn: raw}
	if c.closeCause(4) != nil {
		t.Fatal("close")
	}
	n, err := c.Write([]byte{0xc0, 0})
	if n != 0 || err != net.ErrClosed {
		t.Fatal("write")
	}
	seq, w, _, cl := state(c)
	if seq != 3 || !w || !cl {
		t.Fatal(seq)
	}
	t.Log("REACHED close-before-error")
}
func TestTransportSetterFailurePreventsWrite(t *testing.T) {
	enableDiag(t)
	sentinel := errors.New("setter")
	raw := &diagTestConn{seterr: sentinel}
	c := &packetConn{Conn: raw}
	n, err := c.Write([]byte{0xc0, 0})
	if n != 0 || err != sentinel || raw.writes != 0 || raw.closes != 0 {
		t.Fatal("write admitted after setter failure")
	}
	seq, w, s, cl := state(c)
	if seq != 1 || w || !s || cl {
		t.Fatal(seq)
	}
	t.Log("REACHED failed-setter-prevents-write")
}
func TestTransportPublicationSetterClose(t *testing.T) {
	enableDiag(t)
	sentinel := errors.New("setter")
	raw := &diagTestConn{seterr: sentinel}
	c := &packetConn{Conn: raw}
	if c.writePublish([]byte("test"), nil, false) != sentinel || raw.writes != 0 || raw.closes != 1 {
		t.Fatal("publication setter policy")
	}
	seq, w, s, cl := state(c)
	if seq != 3 || w || !s || !cl {
		t.Fatal(seq)
	}
	t.Log("REACHED publication-setter-close")
}
func TestTransportFirstErrorNotOverwritten(t *testing.T) {
	enableDiag(t)
	raw := &diagTestConn{err: io.ErrShortWrite}
	c := &packetConn{Conn: raw}
	if n, err := c.Write([]byte{1}); n != 0 || err != io.ErrShortWrite {
		t.Errorf("first diagnostic write: n=%d error=%v, want 0 and io.ErrShortWrite", n, err)
	}
	raw.mu.Lock()
	raw.err = net.ErrClosed
	raw.mu.Unlock()
	if n, err := c.Write([]byte{2}); n != 0 || err != net.ErrClosed {
		t.Errorf("second diagnostic write: n=%d error=%v, want 0 and net.ErrClosed", n, err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("first diagnostic Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("second diagnostic Close: %v", err)
	}
	seq, w, _, cl := state(c)
	if seq != 3 || !w || !cl || raw.writes != 2 || raw.closes != 2 {
		t.Fatal("bounded records changed original calls", seq)
	}
	t.Log("REACHED first-error-bounded underlying-close-twice")
}
func TestTransportNominalNoEvents(t *testing.T) {
	enableDiag(t)
	raw := &diagTestConn{}
	c := &packetConn{Conn: raw}
	if c.writePublish([]byte("test"), []byte("synthetic"), false) != nil {
		t.Fatal("publish")
	}
	if _, err := c.Write([]byte{0xc0, 0}); err != nil {
		t.Fatal("ping")
	}
	seq, w, s, cl := state(c)
	if seq != 0 || w || s || cl {
		t.Fatal("nominal log")
	}
	t.Log("REACHED nominal-no-events")
}
func checkConcurrentTransportResult(t *testing.T, operation string, err error, afterClose bool) {
	t.Helper()
	if afterClose && err == nil {
		t.Errorf("%s unexpectedly succeeded after Close", operation)
	}
	if err != nil && !errors.Is(err, net.ErrClosed) {
		t.Errorf("unexpected %s result (afterClose=%t): %v", operation, afterClose, err)
	}
}

func TestTransportConcurrentNativePingWriteClose(t *testing.T) {
	enableDiag(t)
	raw := &diagTestConn{}
	c := &packetConn{Conn: raw}
	var tx native.Tx
	tx.SetTxTransport(c)
	// The original harness could finish both writers before the Close goroutine
	// was scheduled, so its mandatory first-error assertion was not reached.
	// Keep concurrent calls, but guarantee half the writes occur after real Close.
	closed := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if i == 50 {
				<-closed
			}
			checkConcurrentTransportResult(t, "publish", c.writePublish([]byte("test"), []byte("synthetic"), false), i >= 50)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if i == 50 {
				<-closed
			}
			checkConcurrentTransportResult(t, "native PING", tx.WriteSimple(native.PacketPingreq), i >= 50)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if err := c.closeCause(4); err != nil {
				t.Errorf("concurrent diagnostic Close: %v", err)
			}
			if i == 0 {
				close(closed)
			}
		}
	}()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock")
	}
	seq, w, _, cl := state(c)
	if seq != 3 || !w || !cl {
		t.Fatal("not reached", seq)
	}
	t.Log("REACHED concurrent-native-ping-publish-close")
}
func TestTransportMetadataLayout(t *testing.T) {
	t.Logf("REACHED host-layout packetConn=%d diagnostic=%d deadline=%d; NOT MCU sizes", unsafe.Sizeof(packetConn{}), unsafe.Sizeof(transportDiagnostic{}), unsafe.Sizeof(time.Time{}))
}
