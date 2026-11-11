package tcp

import "time"

// One immutable epoch shared with application observations. time.Since uses
// runtime monotonic ticks, never Unix wall time. No per-connection time.Time.
var diagnosticEpoch = time.Now()

// DiagnosticNow returns nanoseconds since that epoch (not serial receipt time).
func DiagnosticNow() int64 { return time.Since(diagnosticEpoch).Nanoseconds() }

// DiagnosticDeadline is a scalar snapshot of the existing effective deadline,
// under its existing lock. It does not set/retain a new deadline or change policy.
// Subsequent concurrent SetWriteDeadline can still change the native deadline.
func (conn *Conn) DiagnosticDeadline() (bool, int64) {
	conn.mu.Lock()
	present := !conn.wdead.IsZero()
	var value int64
	if present {
		value = conn.wdead.Sub(diagnosticEpoch).Nanoseconds()
	}
	conn.mu.Unlock()
	return present, value
}

// diagnosticDeadlineAssigned is called under mu for every wdead assignment,
// even an identical value. Never reset or wrap: saturation remains ambiguous.
func (conn *Conn) diagnosticDeadlineAssigned() {
	if conn.wdeadlineVersion != ^uint64(0) {
		conn.wdeadlineVersion++
	}
}

// DiagnosticWriteSnapshot is NOT an observation of a primitive deadline read.
// Its version brackets the entire real Write, including wait/ABA/reset paths.
func (conn *Conn) DiagnosticWriteSnapshot() (bool, int64, uint64) {
	conn.mu.Lock()
	present := !conn.wdead.IsZero()
	var value int64
	if present {
		value = conn.wdead.Sub(diagnosticEpoch).Nanoseconds()
	}
	version := conn.wdeadlineVersion
	conn.mu.Unlock()
	return present, value, version
}

func (conn *Conn) DiagnosticWriteEnd(generation uint64, requested, written int, err error, start, end int64, present bool, deadline int64, version uint64) {
	if TransportDiagnostics != "true" || err == nil {
		return
	}
	conn.mu.Lock()
	ambiguous := version == ^uint64(0) || version != conn.wdeadlineVersion || generation != conn.h.connid
	conn.h.transportTimedEvent(1, generation, requested, written, primitiveCode(err), start, end, present, deadline, 0, ambiguous)
	conn.mu.Unlock()
}

// DiagnosticCloseBegin returns an invocation-local ownership token. The first
// entry is selected atomically under the same lock as diagnostic event state.
func (conn *Conn) DiagnosticCloseBegin() (uint64, bool, int64) {
	if TransportDiagnostics != "true" {
		return 0, false, 0
	}
	conn.mu.Lock()
	generation := conn.h.connid
	first := conn.h.diag.seen&(1<<2) == 0
	stamp := DiagnosticNow()
	if first {
		conn.h.transportTimedEvent(2, generation, 0, 0, 0, stamp, stamp, false, 0, 1, false)
	}
	conn.mu.Unlock()
	return generation, first, stamp
}

func (conn *Conn) DiagnosticTimedEnd(site uint8, generation uint64, requested, written int, err error, start, end int64, present bool, deadline int64, owner uint8) {
	if TransportDiagnostics != "true" || ((site == 1 || site == 4) && err == nil) {
		return
	}
	conn.mu.Lock()
	conn.h.transportTimedEvent(site, generation, requested, written, primitiveCode(err), start, end, present, deadline, owner, false)
	conn.mu.Unlock()
}
