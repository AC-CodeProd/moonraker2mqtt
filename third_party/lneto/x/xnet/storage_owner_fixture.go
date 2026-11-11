//go:build native_storage_fixture && !tinygo

package xnet

import "github.com/soypat/lneto/tcp"

// FixtureOwnerSnapshot is excluded from MCU and exists only for source-route
// gates; the fresh native object itself is returned for identity comparison.
// FixtureAbortTCP performs test-server teardown under the real stack lock.
func (s *StackAsync) FixtureAbortTCP(c *tcp.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.Abort()
	s.tcps.DetachTCP(c)
}

func (d *NativeDialOwner) FixtureOwnerSnapshot() (*tcp.Conn, uint64, [4]any, [4]int) {
	d.mu.Lock()
	a := d.attempt
	d.mu.Unlock()
	a.mu.Lock()
	c, token := a.conn, a.token
	a.mu.Unlock()
	p, caps := c.FixtureBackings()
	return c, token, p, caps
}
