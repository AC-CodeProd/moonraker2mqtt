package setup

import (
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/soypat/lneto/tcp"
)

func TestPortalRealTCPFailureCancelsPending(t *testing.T) {
	for _, repair := range []bool{false, true} {
		for _, failure := range []string{"unsent-timeout", "unacked-timeout", "disconnect"} {
			name := map[bool]string{false: "save", true: "repair"}[repair] + "/" + failure
			t.Run(name, func(t *testing.T) {
				raw := post(string(payload()))
				op := SaveOperation
				if repair {
					op = RepairOperation
					raw = strings.Replace(raw, "/settings", "/repair", 1)
					raw = strings.Replace(raw, "Content-Type:", "X-Setup-Repair: erase-two-settings-slots\r\nContent-Type:", 1)
				}
				retained := make([]byte, PendingSize)
				stages, cancels := 0, 0
				stage := func(b []byte) error { stages++; return StageOperation(retained, b, op) }
				p := Portal{Token: "token", Recovery: repair, Save: stage, Repair: stage, Cancel: func() { cancels++; binary.LittleEndian.PutUint32(retained, 0) }}
				c := realConn(t, raw)
				defer c.Abort()
				done := make(chan bool, 1)
				start := time.Now()
				go func() { done <- p.Handle(c) }()
				waitQueued(t, c.Conn, done)
				if failure == "unacked-timeout" {
					b := make([]byte, 4096)
					b[0] = 0x45
					if n, err := c.Encapsulate(b, 0, 20); err != nil || n <= 20 {
						t.Fatalf("transmit %d %v", n, err)
					}
					if c.BufferedUnsent() != 0 {
						t.Fatal("test failed to empty unsent queue")
					}
				}
				if failure == "disconnect" {
					peerSegment(t, c.Conn, 501+tcp.Value(len(raw)), 101, tcp.FlagRST, "")
				}
				select {
				case accepted := <-done:
					if accepted {
						t.Fatal("failed response authorized reset")
					}
				case <-time.After(3 * time.Second):
					t.Fatal("unbounded drain")
				}
				if time.Since(start) > 3*time.Second || stages != 1 || cancels != 1 {
					t.Fatalf("stage/cancel=%d/%d", stages, cancels)
				}
				pending, err := TakeOperation(retained, true)
				if err != nil || pending != nil {
					t.Fatal("failed request would replay on unrelated software reset")
				}
				// Explicit user retry, not an automatic reset; only the new confirmed
				// request may publish pending state. No destructive backend is called live.
				response, accepted := exchange(t, p, raw)
				if !accepted || !strings.HasPrefix(response, "HTTP/1.1 202") || stages != 2 || cancels != 1 {
					t.Fatal("explicit retry failed")
				}
				pending, err = TakeOperation(retained, true)
				if err != nil || pending == nil || pending.Operation != op {
					t.Fatal("retry handoff")
				}
				pending, err = TakeOperation(retained, true)
				if err != nil || pending != nil {
					t.Fatal("double repair/save replay")
				}
			})
		}
	}
}

type failedResponseConn struct {
	bufferedConn
	failure string
}

func (c failedResponseConn) Write(b []byte) (int, error) {
	if c.failure == "write-error" {
		return 0, io.ErrClosedPipe
	}
	if c.failure == "short-write" {
		return len(b) - 1, nil
	}
	return c.bufferedConn.Write(b)
}
func (c failedResponseConn) Close() error {
	if c.failure == "close-error" {
		c.Abort()
		return net.ErrClosed
	}
	return c.bufferedConn.Close()
}
func TestPortalResponseWriteAndCloseErrors(t *testing.T) {
	for _, failure := range []string{"write-error", "short-write", "close-error"} {
		t.Run(failure, func(t *testing.T) {
			raw := post(string(payload()))
			c := realConn(t, raw)
			defer c.Abort()
			cancel := false
			p := Portal{Token: "token", Save: func([]byte) error { return nil }, Cancel: func() { cancel = true }}
			done := make(chan bool, 1)
			go func() { done <- p.Handle(failedResponseConn{c, failure}) }()
			if failure == "close-error" {
				waitQueued(t, c.Conn, done)
				b := make([]byte, 4096)
				b[0] = 0x45
				n, err := c.Encapsulate(b, 0, 20)
				if err != nil || n <= 20 {
					t.Fatal("transmit")
				}
				f, _ := tcp.NewFrame(b[20 : 20+n])
				peerSegment(t, c.Conn, 501+tcp.Value(len(raw)), f.Seq()+tcp.Value(len(f.Payload())), tcp.FlagACK, "")
			}
			select {
			case accepted := <-done:
				if accepted || !cancel {
					t.Fatal("response error accepted or pending not cancelled")
				}
			case <-time.After(time.Second):
				t.Fatal("response error blocked")
			}
		})
	}
}
