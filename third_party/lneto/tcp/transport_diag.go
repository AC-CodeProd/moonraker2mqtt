package tcp

import (
	"net"
	"os"
	"unsafe"
)

// Explicit opt-in, no strings from errors or wire data.
var TransportDiagnostics = "false"

// Protected by the same owner lock as Handler, never a current-owner global.
// A reset keeps the old binding so a late Write can report generationChanged;
// a new bind starts a fresh record sequence. This is diagnostic metadata only.
type transportDiagnostic struct {
	stack      uintptr
	fd         int
	seq        uint32
	seen       uint32
	generation uint64
}

func sameDiagnosticErrorIdentity(err, singleton error) bool {
	x := *(*[2]uintptr)(unsafe.Pointer(&err))
	y := *(*[2]uintptr)(unsafe.Pointer(&singleton))
	return x[0] == y[0] && x[1] == y[1]
}
func primitiveCode(err error) uint8 {
	if err == nil {
		return 0
	}
	if sameDiagnosticErrorIdentity(err, errDeadlineExceeded) || sameDiagnosticErrorIdentity(err, os.ErrDeadlineExceeded) {
		return 1
	}
	if sameDiagnosticErrorIdentity(err, net.ErrClosed) {
		return 4
	}
	if sameDiagnosticErrorIdentity(err, errConnectionClosing) {
		return 5
	}
	return 255
}

//go:noinline
func (h *Handler) transportEvent(site uint8, generation uint64, requested, written int, code uint8) {
	if TransportDiagnostics != "true" {
		return
	}
	stamp := DiagnosticNow()
	owner := uint8(0)
	if site == 2 || site == 3 || site == 5 {
		owner = 1
	}
	h.transportTimedEvent(site, generation, requested, written, code, stamp, stamp, false, 0, owner, false)
}

//go:noinline
func (h *Handler) transportTimedEvent(site uint8, generation uint64, requested, written int, code uint8, start, end int64, present bool, deadline int64, owner uint8, ambiguous bool) {
	if TransportDiagnostics != "true" || h.diag.stack == 0 {
		return
	}
	if generation != h.diag.generation {
		if h.diag.seen&(uint32(1)<<31) == 0 {
			h.diag.seen |= uint32(1) << 31
			h.diag.seq++
			println("TTG2:", h.diag.stack, h.diag.fd, uintptr(unsafe.Pointer(h)), h.diag.generation, h.diag.seq, site, generation, h.connid)
		}
		return
	}
	bit := uint32(1) << site
	if h.diag.seen&bit != 0 {
		return
	}
	h.diag.seen |= bit
	h.diag.seq++
	println("TTB4:", h.diag.stack, h.diag.fd, uintptr(unsafe.Pointer(h)), h.diag.generation, h.diag.seq, site, generation, h.connid, requested, written, code, start, end, present, deadline, owner, ambiguous)
}

// DiagnosticBind is called only at connection promotion, not packet/idle polls.
func (conn *Conn) DiagnosticBind(stack uintptr, fd int) {
	if TransportDiagnostics != "true" {
		return
	}
	conn.mu.Lock()
	conn.h.diag = transportDiagnostic{stack: stack, fd: fd, generation: conn.h.connid}
	conn.h.transportEvent(0, conn.h.connid, 0, 0, 0)
	conn.mu.Unlock()
}

func (conn *Conn) DiagnosticBegin(close bool) uint64 {
	if TransportDiagnostics != "true" {
		return 0
	}
	conn.mu.Lock()
	generation := conn.h.connid
	if close {
		conn.h.transportEvent(2, generation, 0, 0, 0)
	}
	conn.mu.Unlock()
	return generation
}

func (conn *Conn) DiagnosticEnd(site uint8, generation uint64, requested, written int, err error) {
	if TransportDiagnostics != "true" {
		return
	}
	if (site == 1 || site == 4) && err == nil {
		return
	}
	conn.mu.Lock()
	conn.h.transportEvent(site, generation, requested, written, primitiveCode(err))
	conn.mu.Unlock()
}
