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

// Host-only barriers, never injected byte counts or errors.
type r4DeadlineSeam struct {
	tcpconn
	entered, proceed chan struct{}
}

func (p *r4DeadlineSeam) Write(b []byte) (int, error) {
	close(p.entered)
	<-p.proceed
	return p.Conn.Write(b)
}

func TestR4RealDeadlineSchedules(t *testing.T) {
	old, oldTCP := TransportDiagnostics, tcp.TransportDiagnostics
	TransportDiagnostics, tcp.TransportDiagnostics = "true", "true"
	defer func() { TransportDiagnostics, tcp.TransportDiagnostics = old, oldTCP }()
	for _, name := range []string{"stable_absent", "stable_future", "stable_expired", "same_before_snapshot", "concurrent_same", "concurrent_ABA", "during_wait_expiry", "during_wait_ABA"} {
		t.Run(name, func(t *testing.T) {
			client, server, cc, sc := newTCPStacks(t, 5678, 1500)
			entered, proceed := make(chan struct{}, 1), make(chan struct{})
			wait := name == "during_wait_expiry" || name == "during_wait_ABA"
			if wait {
				backoff := func(uint) time.Duration {
					select {
					case entered <- struct{}{}:
					default:
					}
					<-proceed
					return 0
				}
				if e := cc.Configure(tcp.ConnConfig{RxBuf: make([]byte, 512), TxBuf: make([]byte, 512), TxPacketQueueSize: 4, RWBackoff: backoff}); e != nil {
					t.Fatal(e)
				}
			}
			testerFrom(t, 1500).TestTCPSetupAndEstablish(server, client, sc, cc, 8080, 1337)
			var native net.Conn = tcpconn{Conn: cc}
			if !wait {
				native = &r4DeadlineSeam{tcpconn: tcpconn{Conn: cc}, entered: entered, proceed: proceed}
			}
			s := NewBerkeleyStack(func(context.Context, string, int, int, net.Addr, net.Addr) (any, error) { return native, nil })
			fd, e := s.Socket(AF_INET, SOCK_STREAM, 6)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Connect(fd, "", netip.MustParseAddrPort("192.0.2.1:8080")); e != nil {
				t.Fatal(e)
			}
			var deadline time.Time
			if name == "stable_future" || name == "during_wait_expiry" {
				deadline = time.Now().Add(time.Hour)
			}
			if name == "stable_expired" || name == "same_before_snapshot" || name == "concurrent_same" || name == "concurrent_ABA" || name == "during_wait_ABA" {
				deadline = time.Now().Add(-time.Second)
			}
			// during_wait_ABA must reach the real wait with a future initial deadline.
			if name == "during_wait_ABA" {
				deadline = time.Now().Add(100 * time.Millisecond)
			}
			if e = cc.SetWriteDeadline(deadline); e != nil {
				t.Fatal(e)
			}
			if name == "same_before_snapshot" {
				if e = cc.SetDeadline(deadline); e != nil {
					t.Fatal(e)
				}
			}
			present, value, version := cc.DiagnosticWriteSnapshot()
			if present != !deadline.IsZero() {
				t.Fatal("snapshot presence")
			}
			count := 8
			if wait {
				count = 515
			}
			type result struct {
				n int
				e error
			}
			done := make(chan result, 1)
			go func() { n, e := s.Send(fd, make([]byte, count), 0, deadline); done <- result{n, e} }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("real seam/wait not reached")
			}
			switch name {
			case "concurrent_same":
				e = cc.SetWriteDeadline(deadline)
			case "concurrent_ABA", "during_wait_ABA":
				e = cc.SetWriteDeadline(time.Now().Add(-time.Hour))
				if e == nil {
					e = cc.SetDeadline(deadline)
				}
			case "during_wait_expiry":
				e = cc.SetWriteDeadline(time.Now().Add(-time.Second))
			}
			if e != nil {
				t.Fatal(e)
			}
			_, after, afterVersion := cc.DiagnosticWriteSnapshot()
			changed := name == "concurrent_same" || name == "concurrent_ABA" || wait
			// Send now always assigns its explicit deadline, including zero.
			// The diagnostic snapshot within Send is after that assignment.
			wantVersion := version + 1
			switch name {
			case "concurrent_same", "during_wait_expiry":
				wantVersion++
			case "concurrent_ABA", "during_wait_ABA":
				wantVersion += 2
			}
			if afterVersion != wantVersion {
				t.Fatal("version mismatch", wantVersion, afterVersion)
			}
			if name != "during_wait_expiry" && value != after {
				t.Fatal("same-value/ABA snapshot mismatch")
			}
			if name == "during_wait_ABA" {
				time.Sleep(110 * time.Millisecond)
			} // held backoff, real deadline expiry
			close(proceed)
			select {
			case got := <-done:
				wantN := 0
				var wantErr error = os.ErrDeadlineExceeded
				if name == "stable_absent" || name == "stable_future" {
					wantN = 8
					wantErr = nil
				}
				if wait {
					wantN = 512
				}
				if got.n != wantN || got.e != wantErr {
					t.Fatal("real Write result changed", got, wantN, wantErr)
				}
			case <-time.After(time.Second):
				t.Fatal("real Write hung")
			}
			t.Log("REACHED real Write, version interval", name, "changed", changed)
		})
	}
}
