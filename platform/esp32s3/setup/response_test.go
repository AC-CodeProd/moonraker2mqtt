package setup

import (
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/soypat/lneto"
	"github.com/soypat/lneto/tcp"
)

type bufferedConn struct{ *tcp.Conn }

func (c bufferedConn) LocalAddr() net.Addr  { return &net.TCPAddr{Port: 80} }
func (c bufferedConn) RemoteAddr() net.Addr { return &net.TCPAddr{Port: 1234} }
func (c bufferedConn) LnetoConn() *tcp.Conn { return c.Conn }

// Use the pinned real Conn, handler, Tx queue and ACK processing, not net.Pipe.
// The fake peer supplies actual TCP segments through Conn.Demux; outgoing
// packets pass through Conn.Encapsulate (the same locks as the radio pump).
func realConn(t *testing.T, raw string) bufferedConn {
	t.Helper()
	c := new(tcp.Conn)
	if err := c.Configure(tcp.ConnConfig{RxBuf: make([]byte, 4096), TxBuf: make([]byte, 4096), TxPacketQueueSize: 8, RWBackoff: func(uint) time.Duration { return lneto.BackoffFlagGosched }}); err != nil {
		t.Fatal(err)
	}
	if err := c.OpenActive(80, netip.MustParseAddrPort("192.168.4.2:1234"), 100); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 4096)
	b[0] = 0x45
	if _, err := c.Encapsulate(b, 0, 20); err != nil {
		t.Fatal(err)
	}
	peerSegment(t, c, 500, 101, tcp.FlagSYN|tcp.FlagACK, "")
	if _, err := c.Encapsulate(b, 0, 20); err != nil {
		t.Fatal(err)
	}
	peerSegment(t, c, 501, 101, tcp.FlagPSH|tcp.FlagACK, raw)
	return bufferedConn{c}
}
func peerSegment(t *testing.T, c *tcp.Conn, seq, ack tcp.Value, flags tcp.Flags, data string) {
	t.Helper()
	b := make([]byte, 40+len(data))
	b[0] = 0x45
	copy(b[12:16], []byte{192, 168, 4, 2})
	copy(b[16:20], []byte{192, 168, 4, 1})
	f, _ := tcp.NewFrame(b[20:])
	f.SetSourcePort(1234)
	f.SetDestinationPort(80)
	f.SetSeq(seq)
	f.SetAck(ack)
	f.SetOffsetAndFlags(5, flags)
	f.SetWindowSize(4096)
	copy(b[40:], data)
	if err := c.Demux(b, 20); err != nil && !(flags&tcp.FlagRST != 0 && err == net.ErrClosed) {
		t.Fatal(err)
	}
}
func waitQueued(t *testing.T, c *tcp.Conn, done <-chan bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for c.BufferedUnsent() == 0 {
		select {
		case accepted := <-done:
			t.Fatalf("Handle returned before transmit: accepted=%v unsent=%d", accepted, c.BufferedUnsent())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("no response queued")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case accepted := <-done:
		t.Fatalf("Handle returned with queued TCP bytes: accepted=%v unsent=%d", accepted, c.BufferedUnsent())
	case <-time.After(10 * time.Millisecond):
	}
}
func TestPortalRealTCPWaitsForResponseACK(t *testing.T) {
	for _, repair := range []bool{false, true} {
		t.Run(map[bool]string{false: "save", true: "repair"}[repair], func(t *testing.T) {
			raw := post(string(payload()))
			if repair {
				raw = strings.Replace(raw, "/settings", "/repair", 1)
				raw = strings.Replace(raw, "Content-Type:", "X-Setup-Repair: erase-two-settings-slots\r\nContent-Type:", 1)
			}
			c := realConn(t, raw)
			defer c.Abort()
			p := Portal{Token: "token", Recovery: repair, Save: func([]byte) error { return nil }, Repair: func([]byte) error { return nil }}
			done := make(chan bool, 1)
			go func() { done <- p.Handle(c) }()
			waitQueued(t, c.Conn, done)
			// ACK a first fragment but withhold the final ACK. Neither a partial
			// ACK nor Flush (which ignores in-flight bytes) may authorize reset.
			b := make([]byte, 148)
			b[0] = 0x45
			n, err := c.Encapsulate(b, 0, 20)
			if err != nil || n <= 20 {
				t.Fatalf("transmit %d %v", n, err)
			}
			f, _ := tcp.NewFrame(b[20 : 20+n])
			response := string(f.Payload())
			peerSegment(t, c.Conn, 501+tcp.Value(len(raw)), f.Seq()+tcp.Value(len(f.Payload())), tcp.FlagACK, "")
			select {
			case accepted := <-done:
				t.Fatalf("reset after partial ACK: %v", accepted)
			case <-time.After(10 * time.Millisecond):
			}
			b = make([]byte, 4096)
			b[0] = 0x45
			n, err = c.Encapsulate(b, 0, 20)
			if err != nil || n <= 20 {
				t.Fatalf("transmit remainder %d %v", n, err)
			}
			f, _ = tcp.NewFrame(b[20 : 20+n])
			response += string(f.Payload())
			if !strings.HasPrefix(response, "HTTP/1.1 202") || !strings.HasSuffix(response, "vérifiez ensuite le port série.") {
				t.Fatal("incomplete response")
			}
			if c.BufferedUnsent() != 0 {
				t.Fatal("unsent bytes remain")
			}
			if err := c.Flush(); err != nil {
				t.Fatal(err)
			} // Flush succeeds despite pending ACK.
			select {
			case accepted := <-done:
				t.Fatalf("reset before ACK: %v", accepted)
			case <-time.After(10 * time.Millisecond):
			}
			peerSegment(t, c.Conn, 501+tcp.Value(len(raw)), f.Seq()+tcp.Value(len(f.Payload())), tcp.FlagACK, "")
			select {
			case accepted := <-done:
				if !accepted {
					t.Fatal("ACKed response rejected")
				}
			case <-time.After(time.Second):
				t.Fatal("ACK did not unblock portal")
			}
		})
	}
}
