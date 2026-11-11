//go:build tinygo || natiu

package mqtt

import (
	"encoding/binary"
	"errors"
	native "github.com/soypat/natiu-mqtt"
	"io"
	"time"
)

// writePublish streams a validated QoS0 packet. Payload ownership stays with the
// synchronous caller; no copy/retention and no bytes.Buffer payload assembly.
// Hold the same wire lock used by native ping/disconnect/subscribe writes for
// the ENTIRE packet, not individual chunks. This does not acquire native's
// txlock, so there is no wire -> txlock inversion with the receive goroutine.
func (c *packetConn) writePublish(topic []byte, payload []byte, retain bool) error {
	if len(topic) == 0 || len(topic) > 256 || len(payload) > MaxPayload {
		return errors.New("MQTT publication exceeds firmware limit or has empty topic")
	}
	flags, err := native.NewPublishFlags(native.QoS0, false, retain)
	if err != nil {
		return err
	}
	h, err := native.NewHeader(native.PacketPublish, flags, uint32(2+len(topic)+len(payload)))
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	deadline := time.Now().Add(10 * time.Second)
	if err = c.Conn.SetWriteDeadline(deadline); err != nil {
		if TransportDiagnostics == "true" {
			c.observeWrite(2, 0, 0, time.Until(deadline).Nanoseconds(), err, true)
		}
		c.closeCause(5)
		return err
	}
	n := h.Put(c.txPrefix[:5])
	binary.BigEndian.PutUint16(c.txPrefix[n:n+2], uint16(len(topic)))
	if err = c.writeChunks(c.txPrefix[:n+2], deadline); err == nil {
		err = c.writeChunks(topic, deadline)
	}
	if err == nil {
		err = c.writeChunks(payload, deadline)
	}
	if err != nil {
		// A partial MQTT packet is not resumable/retryable (QoS0). Close even for
		// zero-byte failures so a following ping cannot be appended to its suffix.
		c.closeCause(6)
	}
	return err
}

// writeChunks is only called with c.mu held. Bound transport calls too, rather
// than just eliminating the encoder's contiguous allocation.
func (c *packetConn) writeChunks(p []byte, deadline time.Time) error {
	for len(p) > 0 {
		size := len(p)
		if size > 256 {
			size = 256
		}
		n, err := c.Conn.Write(p[:size])
		if TransportDiagnostics == "true" {
			observed := err
			if observed == nil && n != size {
				observed = io.ErrShortWrite
			}
			if observed != nil {
				c.observeWrite(3, size, n, time.Until(deadline).Nanoseconds(), observed, false)
			}
		}
		if err != nil {
			return err
		}
		if n != size {
			return io.ErrShortWrite
		}
		p = p[size:]
	}
	return nil
}
