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

// Host-only injection at primitive net.Conn, on an established real TCP identity.
type specSendClockProbe struct{ tcpconn }

func (p *specSendClockProbe) Write(b []byte) (int, error) { return len(b) - 3, os.ErrDeadlineExceeded }

func TestSPECSendClockBoundary(t *testing.T) {
	old, oldTCP := TransportDiagnostics, tcp.TransportDiagnostics
	TransportDiagnostics, tcp.TransportDiagnostics = "true", "true"
	defer func() { TransportDiagnostics, tcp.TransportDiagnostics = old, oldTCP }()
	for _, name := range []string{"zero", "expired", "future"} {
		t.Run(name, func(t *testing.T) {
			client, server, cc, sc := newTCPStacks(t, 5678, 1500)
			testerFrom(t, 1500).TestTCPSetupAndEstablish(server, client, sc, cc, 8080, 1337)
			raw := &specSendClockProbe{tcpconn: tcpconn{Conn: cc}}
			s := NewBerkeleyStack(func(context.Context, string, int, int, net.Addr, net.Addr) (any, error) { return raw, nil })
			fd, err := s.Socket(AF_INET, SOCK_STREAM, 6)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Connect(fd, "", netip.MustParseAddrPort("192.0.2.1:8080")); err != nil {
				t.Fatal(err)
			}
			var deadline time.Time
			if name == "expired" {
				deadline = time.Now().Add(-time.Second)
			}
			if name == "future" {
				deadline = time.Now().Add(time.Second)
			}
			before := tcp.DiagnosticNow()
			n, err := s.Send(fd, make([]byte, 515), 0, deadline)
			if n != 512 || err != os.ErrDeadlineExceeded {
				t.Fatal("primitive bytes/error changed", n, err)
			}
			present, value := cc.DiagnosticDeadline()
			if present != (name != "zero") || (!present && value != 0) {
				t.Fatal("effective deadline absence")
			}
			if name == "expired" && value >= before {
				t.Fatal("expired")
			}
			if name == "future" && value <= tcp.DiagnosticNow() {
				t.Fatal("future")
			}
			t.Log("REACHED measured Send boundaries; 512/515 and original timeout preserved", name)
		})
	}
}
