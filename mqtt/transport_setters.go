//go:build natiu || tinygo

package mqtt

import "time"

// These promoted methods preserve every setter call/result. Read setters may
// be used by idle polling; only the first ERROR is recorded, never nominal polls.
func (c *packetConn) SetWriteDeadline(t time.Time) error {
	err := c.Conn.SetWriteDeadline(t)
	if TransportDiagnostics == "true" && err != nil {
		c.observeWrite(4, 0, 0, time.Until(t).Nanoseconds(), err, true)
	}
	return err
}
func (c *packetConn) SetReadDeadline(t time.Time) error {
	err := c.Conn.SetReadDeadline(t)
	if TransportDiagnostics == "true" && err != nil {
		c.observeWrite(5, 0, 0, time.Until(t).Nanoseconds(), err, true)
	}
	return err
}
func (c *packetConn) SetDeadline(t time.Time) error {
	err := c.Conn.SetDeadline(t)
	if TransportDiagnostics == "true" && err != nil {
		c.observeWrite(6, 0, 0, time.Until(t).Nanoseconds(), err, true)
	}
	return err
}
