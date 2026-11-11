package tcp

import "net"

// Generation returns the native connection epoch while holding its mutex.
func (conn *Conn) Generation() uint64 {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	return conn.h.connid
}

// ClaimGenerationInterrupt is explicitly opt-in and single-issuance per epoch.
// Its owner MUST join every operation before releasing or reusing the instance.
// The caller must serialize invocation against its owning stack pump.
// Only the non-pooled, freshly allocated active StackGo client exposes it above
// this layer. Close, default traffic and the shared network device are unchanged.
func (conn *Conn) ClaimGenerationInterrupt(expected uint64) (func() error, error) {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if expected != conn.h.connid || conn.h.State().IsClosed() || conn.interruptClaimed {
		return nil, net.ErrClosed
	}
	conn.interruptClaimed = true
	return func() error {
		conn.mu.Lock()
		defer conn.mu.Unlock()
		if expected != conn.h.connid || !conn.interruptClaimed {
			return net.ErrClosed
		}
		// Under the same mutex as checkPipe/Read/Write; stale capabilities cannot
		// interrupt a subsequently opened generation, including on the same object.
		conn.h.Abort()
		conn.reset(conn.h)
		return nil
	}, nil
}
