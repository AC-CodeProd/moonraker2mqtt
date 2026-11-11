package xnet

import (
	"context"
	"github.com/soypat/lneto/tcp"
	"net"
	"net/netip"
	"os"
	"runtime"
	"testing"
	"time"
)

type transportBackoff struct{ entered chan struct{} }

func (b transportBackoff) Do(uint) time.Duration {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	runtime.Gosched()
	return 0
}

func TestTransportBerkeleyConcurrentPartialTimeoutClose(t *testing.T) {
	old, oldTCP := TransportDiagnostics, tcp.TransportDiagnostics
	TransportDiagnostics = "true"
	tcp.TransportDiagnostics = "true"
	defer func() { TransportDiagnostics = old; tcp.TransportDiagnostics = oldTCP }()
	client, server, cc, sc := newTCPStacks(t, 5678, 1500)
	entered := make(chan struct{}, 1)
	if e := cc.Configure(tcp.ConnConfig{RxBuf: make([]byte, 512), TxBuf: make([]byte, 512), TxPacketQueueSize: 4, RWBackoff: transportBackoff{entered}.Do}); e != nil {
		t.Fatal(e)
	}
	testerFrom(t, 1500).TestTCPSetupAndEstablish(server, client, sc, cc, 8080, 1337)
	s := NewBerkeleyStack(func(context.Context, string, int, int, net.Addr, net.Addr) (any, error) {
		return tcpconn{Conn: cc}, nil
	})
	fd, e := s.Socket(AF_INET, SOCK_STREAM, 6)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Connect(fd, "", netip.MustParseAddrPort("192.0.2.1:8080")); e != nil {
		t.Fatal(e)
	}
	type result struct {
		n int
		e error
	}
	writeDone := make(chan result, 1)
	closeDone := make(chan error, 1)
	go func() {
		n, e := s.Send(fd, make([]byte, 515), 0, time.Now().Add(100*time.Millisecond))
		writeDone <- result{n, e}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("write did not reach real buffer full backoff")
	}
	go func() { closeDone <- s.Close(fd) }()
	select {
	case got := <-writeDone:
		if got.n != 512 || got.e != os.ErrDeadlineExceeded {
			t.Fatal("changed partial primitive result", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Write hung")
	}
	select {
	case e := <-closeDone:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("Close hung")
	}
	t.Log("REACHED real partial 512/515 timeout concurrent Berkeley first Close entry; original returned bytes and error preserved")
}
