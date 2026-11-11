//go:build tinygo || natiu

package mqtt

import (
	"io"
	"net"
	"os"
	"sync"
	"time"

	"unsafe"
)

// TransportDiagnostics is opt-in. No endpoints, payloads or error strings.
var TransportDiagnostics = "false"

// Application-owned monotonic epoch, not lneto's former diagnostic clock.
var transportEpoch = time.Now()

func transportNow() int64 { return time.Since(transportEpoch).Nanoseconds() }

// Independent of wire.mu: caller may already own wire.mu, and RX/Close must
// not wait for a blocked Write. Fields are accessed only while mu is held.
type transportDiagnostic struct {
	mu         sync.Mutex
	seq        uint32
	writeSeen  bool
	setterSeen bool
	closeSeen  bool
}

// diagCode is a category, not a driver errno. Unknown remains explicitly 255.
// Compare only the identity of pinned singleton primitive errors. TinyGo's
// runtime/interface.go uses exactly typecode,value, two pointer words. This
// avoids generic reflection equality and its allocating panic branches.
// This is NOT general semantic error equality; unmatched values stay unknown.
func sameDiagnosticErrorIdentity(err, singleton error) bool {
	x := *(*[2]uintptr)(unsafe.Pointer(&err))
	y := *(*[2]uintptr)(unsafe.Pointer(&singleton))
	return x[0] == y[0] && x[1] == y[1]
}
func diagCode(err error) uint8 {
	if err == nil {
		return 0
	}
	if sameDiagnosticErrorIdentity(err, io.ErrShortWrite) {
		return 2
	}
	if sameDiagnosticErrorIdentity(err, io.EOF) {
		return 3
	}
	if sameDiagnosticErrorIdentity(err, net.ErrClosed) {
		return 4
	}
	if e, ok := err.(*net.OpError); ok && e != nil {
		err = e.Err
	}
	if sameDiagnosticErrorIdentity(err, os.ErrDeadlineExceeded) {
		return 1
	}
	return 255
}

//go:noinline
func (c *packetConn) observeWrite(site uint8, requested, written int, remaining int64, err error, setter bool) {
	if TransportDiagnostics != "true" || err == nil {
		return
	}
	c.diag.mu.Lock()
	seen := c.diag.writeSeen
	if setter {
		seen = c.diag.setterSeen
	}
	if !seen {
		if setter {
			c.diag.setterSeen = true
		} else {
			c.diag.writeSeen = true
		}
		c.diag.seq++
		println("TMW4:", uintptr(unsafe.Pointer(c)), c.diag.seq, site, requested, written, remaining, diagCode(err), setter, transportNow())
	}
	c.diag.mu.Unlock()
}

// Close entry and completion are distinct. Entry is not proof Close completed
// before a concurrent write failed. No wire.mu acquired; preserve close result.
func (c *packetConn) closeCause(site uint8) error {
	c.stopMu.Lock()
	c.closed = true
	c.stopMu.Unlock()
	// Owned-generation interruption does not acquire the wire write mutex.
	// Close may otherwise wait behind a native Send holding the write lock.
	if c.interrupt != nil {
		_ = c.interrupt()
	}

	if TransportDiagnostics != "true" {
		return c.Conn.Close()
	}
	c.diag.mu.Lock()
	first := !c.diag.closeSeen
	if first {
		c.diag.closeSeen = true
		c.diag.seq++
		println("TMC3:", uintptr(unsafe.Pointer(c)), c.diag.seq, site, 1, 0, transportNow())
	}
	c.diag.mu.Unlock()
	err := c.Conn.Close()
	returned := transportNow()
	if first {
		c.diag.mu.Lock()
		c.diag.seq++
		println("TMC3:", uintptr(unsafe.Pointer(c)), c.diag.seq, site, 2, diagCode(err), returned)
		c.diag.mu.Unlock()
	}
	return err
}

func (c *packetConn) Close() error { return c.closeCause(7) }
