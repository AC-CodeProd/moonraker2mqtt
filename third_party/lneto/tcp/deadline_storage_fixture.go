//go:build native_storage_fixture

package tcp

import "time"

// FixtureDeadlines is a test-only observer, not part of MCU firmware.
func (c *Conn) FixtureDeadlines() (time.Time, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rdead, c.wdead
}

func (c *Conn) FixtureBackings() ([4]any, [4]int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return [4]any{&c.h.bufTx.rawbuf[0], &c.h.bufRx.Buf[0], &c.h.bufTx.slist.pkts[:cap(c.h.bufTx.slist.pkts)][0], &c.h.reasm.held[:cap(c.h.reasm.held)][0]}, [4]int{cap(c.h.bufTx.rawbuf), cap(c.h.bufRx.Buf), cap(c.h.bufTx.slist.pkts), cap(c.h.reasm.held)}
}
