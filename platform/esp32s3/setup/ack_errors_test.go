package setup

import (
	"encoding/binary"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/soypat/lneto/tcp"
)

func TestPortalInternalACKFailureError(t *testing.T) {
	for _, reset := range []bool{false, true} {
		name := "ACK timeout"
		if reset {
			name = "peer reset"
		}
		t.Run(name, func(t *testing.T) {
			raw := post(string(payload()))
			c := realConn(t, raw)
			defer c.Abort()
			rtc := make([]byte, PendingSize)
			stages, cancels := 0, 0
			p := &Portal{Token: "token", Save: func(b []byte) error { stages++; return Stage(rtc, b) }, Cancel: func() { cancels++; binary.LittleEndian.PutUint32(rtc, 0) }}
			h := portalResponsePipeline(t, p)
			done, failures := make(chan bool, 1), make(chan error, 1)
			go func() {
				accepted, err := h.handle(c)
				failures <- err
				done <- accepted
			}()
			waitQueued(t, c.Conn, done)
			want := os.ErrDeadlineExceeded
			if reset {
				peerSegment(t, c.Conn, 501+tcp.Value(len(raw)), 101, tcp.FlagRST, "")
				want = net.ErrClosed
			}
			select {
			case accepted := <-done:
				err := <-failures
				if accepted || !errors.Is(err, want) || stages != 1 || cancels != 1 {
					t.Fatalf("accepted=%t error=%v stages=%d cancels=%d", accepted, err, stages, cancels)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("ACK failure did not return within the existing bound")
			}
			if pending, err := TakeOperation(rtc, true); err != nil || pending != nil {
				t.Fatal("failed ACK left replayable pending settings")
			}
		})
	}
}
