//go:build tinygo || natiu

package mqtt

import native "github.com/soypat/natiu-mqtt"

// receiveFrame tracks wire framing without retaining or rewriting a packet.
// The official SUBACK decoder allocates ReturnCodes before our post-decode
// checks. Every adapter SUBSCRIBE requests one topic, so reject any SUBACK
// remaining length other than three before admitting its body, even at CONNECT.
// Only the decoder goroutine owns this state; beginReceive must not reset it
// because CONNECT and idle polling also traverse this same byte stream.
type receiveFrame struct {
	remaining uint32
	state     uint8 // 0: first byte; 1..4: remaining length; 5: body
	first     byte
	rejected  bool
}

func (f *receiveFrame) limit(p []byte) []byte {
	if f.state != 5 {
		return p[:1]
	}
	if uint64(len(p)) > uint64(f.remaining) {
		return p[:int(f.remaining)]
	}
	return p
}

func (f *receiveFrame) accept(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	if f.state == 5 {
		f.remaining -= uint32(len(p))
		if f.remaining == 0 {
			f.state = 0
		}
		return nil
	}
	b := p[0] // limit admits exactly one header byte per underlying read.
	if f.state == 0 {
		f.first, f.state, f.remaining = b, 1, 0
		return nil
	}
	f.remaining |= uint32(b&127) << (7 * (f.state - 1))
	if b&128 != 0 {
		if f.state == 4 {
			f.rejected = true
			return native.ErrBadRemainingLen
		}
		f.state++
		return nil
	}
	if f.first>>4 == byte(native.PacketSuback) && f.remaining != 3 {
		f.rejected = true
		return native.ErrBadRemainingLen
	}
	f.state = 5
	if f.remaining == 0 {
		f.state = 0
	}
	return nil
}

func (c *packetConn) readFrame(p []byte) (int, error) {
	if len(p) == 0 {
		return c.Conn.Read(p)
	}
	if c.frame.rejected {
		return 0, native.ErrBadRemainingLen
	}
	n, err := c.Conn.Read(c.frame.limit(p))
	if frameErr := c.frame.accept(p[:n]); frameErr != nil {
		return n, frameErr
	}
	return n, err
}
