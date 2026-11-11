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

func TestTransportBerkeleyActualSendCloseReuse(t *testing.T) {
	old, oldTCP := TransportDiagnostics, tcp.TransportDiagnostics
	TransportDiagnostics = "true"
	tcp.TransportDiagnostics = "true"
	defer func() { TransportDiagnostics = old; tcp.TransportDiagnostics = oldTCP }()
	client, server, cc, sc := newTCPStacks(t, 5678, 1500)
	testerFrom(t, 1500).TestTCPSetupAndEstablish(server, client, sc, cc, 8080, 1337)
	s := NewBerkeleyStack(func(context.Context, string, int, int, net.Addr, net.Addr) (any, error) {
		return tcpconn{Conn: cc}, nil
	})
	fd, e := s.Socket(AF_INET, SOCK_STREAM, 6)
	if e != nil {
		t.Fatal(e)
	}
	addr := netip.MustParseAddrPort("192.0.2.1:8080")
	if e = s.Connect(fd, "", addr); e != nil {
		t.Fatal(e)
	}
	n, e := s.Send(fd, []byte{1, 2}, 0, time.Time{})
	if n != 2 || e != nil {
		t.Fatal("nominal changed", n, e)
	}
	n, e = s.Send(fd, []byte{3, 4}, 0, time.Now().Add(-time.Second))
	if n != 0 || e != os.ErrDeadlineExceeded {
		t.Fatal("actual timeout not reached", n, e)
	}
	if e = s.Close(fd); e != nil {
		t.Fatal("close changed", e)
	}
	if _, e = s.Send(fd, []byte{1}, 0, time.Time{}); e == nil {
		t.Fatal("deleted fd accepted")
	}
	if e = s.Close(fd); e == nil {
		t.Fatal("double close silently repaired")
	}
	cc.Abort()
	if e = cc.OpenListen(1337, 300); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	s.nextFD = fd
	s.mu.Unlock() // test-only force reuse at the real allocator
	fd2, e := s.Socket(AF_INET, SOCK_STREAM, 6)
	if e != nil || fd2 != fd {
		t.Fatal("reuse harness", fd2, e)
	}
	if e = s.Connect(fd2, "", addr); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(fd2); e != nil {
		t.Fatal(e)
	}
	t.Log("REACHED real Berkeley Connect/Send, primitive deadline error before wrap, Close/delete/unknownfd/doubleclose, same fd and TCP object changed generation")
}
