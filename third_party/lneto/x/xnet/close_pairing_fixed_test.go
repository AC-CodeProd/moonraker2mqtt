package xnet

import (
	"context"
	"github.com/soypat/lneto/tcp"
	"net"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"
)

// Test-only transport makes completion order deterministic. Not physical evidence.
type specCloseProbe struct {
	tcpconn
	mu               sync.Mutex
	calls            int
	entered, release chan struct{}
}

func (p *specCloseProbe) Close() error {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.mu.Unlock()
	if n == 1 {
		close(p.entered)
		<-p.release
		return net.ErrClosed
	}
	return os.ErrDeadlineExceeded
}
func TestSPECFirstBackendCloseReturnPairingFixed(t *testing.T) {
	old, oldTCP := TransportDiagnostics, tcp.TransportDiagnostics
	TransportDiagnostics, tcp.TransportDiagnostics = "true", "true"
	defer func() { TransportDiagnostics, tcp.TransportDiagnostics = old, oldTCP }()
	client, server, cc, sc := newTCPStacks(t, 5678, 1500)
	testerFrom(t, 1500).TestTCPSetupAndEstablish(server, client, sc, cc, 8080, 1337)
	raw := &specCloseProbe{tcpconn: tcpconn{Conn: cc}, entered: make(chan struct{}), release: make(chan struct{})}
	s := NewBerkeleyStack(func(context.Context, string, int, int, net.Addr, net.Addr) (any, error) { return raw, nil })
	fd, e := s.Socket(AF_INET, SOCK_STREAM, 6)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Connect(fd, "", netip.MustParseAddrPort("192.0.2.1:8080")); e != nil {
		t.Fatal(e)
	}
	first := make(chan error, 1)
	go func() { first <- s.Close(fd) }()
	select {
	case <-raw.entered:
	case <-time.After(time.Second):
		t.Fatal("first Close not reached")
	}
	if e = s.Close(fd); e != os.ErrDeadlineExceeded {
		t.Fatal("second result changed", e)
	}
	t.Log("REACHED second Close returned primitive timeout while first Close remains blocked; expected no first-owner return yet; second invocation deletes fd")
	close(raw.release)
	select {
	case e = <-first:
		if e != net.ErrClosed {
			t.Fatal("first result changed", e)
		}
	case <-time.After(time.Second):
		t.Fatal("first Close hung")
	}
	t.Log("REACHED first Close returned primitive net.ErrClosed; expected TTB3 first-owner site3 code4 after second-owner deletion")
}
