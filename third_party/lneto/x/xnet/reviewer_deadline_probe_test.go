package xnet

import (
	"context"
	"github.com/soypat/lneto/tcp"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"
)

// External host seam: preserve real tcp.Conn.Write, only schedule deadline setter
// between the Berkeley snapshot and the native primitive's first deadline read.
type reviewerDeadlineProbe struct {
	tcpconn
	entered, proceed chan struct{}
}

func (p *reviewerDeadlineProbe) Write(b []byte) (int, error) {
	close(p.entered)
	<-p.proceed
	present, value := p.Conn.DiagnosticDeadline()
	println("REVIEWER_ACTUAL_PRIMITIVE_DEADLINE:", present, value)
	return p.Conn.Write(b)
}
func TestReviewerDeadlineSnapshotNotActual(t *testing.T) {
	old, oldTCP := TransportDiagnostics, tcp.TransportDiagnostics
	TransportDiagnostics, tcp.TransportDiagnostics = "true", "true"
	defer func() { TransportDiagnostics, tcp.TransportDiagnostics = old, oldTCP }()
	for _, name := range []string{"absent_to_expired", "future_to_expired"} {
		t.Run(name, func(t *testing.T) {
			client, server, cc, sc := newTCPStacks(t, 5678, 1500)
			testerFrom(t, 1500).TestTCPSetupAndEstablish(server, client, sc, cc, 8080, 1337)
			raw := &reviewerDeadlineProbe{tcpconn: tcpconn{Conn: cc}, entered: make(chan struct{}), proceed: make(chan struct{})}
			s := NewBerkeleyStack(func(context.Context, string, int, int, net.Addr, net.Addr) (any, error) { return raw, nil })
			fd, e := s.Socket(AF_INET, SOCK_STREAM, 6)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Connect(fd, "", netip.MustParseAddrPort("192.0.2.1:8080")); e != nil {
				t.Fatal(e)
			}
			var initial time.Time
			if name == "future_to_expired" {
				initial = time.Now().Add(time.Hour)
			}
			type result struct {
				n   int
				err error
			}
			done := make(chan result, 1)
			go func() { n, e := s.Send(fd, make([]byte, 515), 0, initial); done <- result{n, e} }()
			select {
			case <-raw.entered:
			case <-time.After(time.Second):
				t.Fatal("Write seam not reached")
			}
			if e = cc.SetWriteDeadline(time.Now().Add(-time.Second)); e != nil {
				t.Fatal(e)
			}
			present, value := cc.DiagnosticDeadline()
			if !present || value >= tcp.DiagnosticNow() {
				t.Fatal("actual expiry not set")
			}
			close(raw.proceed)
			select {
			case got := <-done:
				if got.n != 0 || got.err != os.ErrDeadlineExceeded {
					t.Fatal("real primitive changed", got)
				}
			case <-time.After(time.Second):
				t.Fatal("native Write hung")
			}
			t.Log("REACHED real tcp.Conn.Write 0/515 native timeout with changed expired deadline; compare emitted TTB3 to ACTUAL_PRIMITIVE_DEADLINE")
		})
	}
}
