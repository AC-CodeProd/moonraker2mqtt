package tcp

import (
	"net"
	"testing"
)

func TestTransportActualReceivedRST(t *testing.T) {
	diagnosticEnable(t)
	c := newConfiguredConn(t)
	server := newHandler(t, 512, 4)
	if e := server.OpenListen(8080, 100); e != nil {
		t.Fatal(e)
	}
	if e := c.h.OpenActive(1337, 8080, 200); e != nil {
		t.Fatal(e)
	}
	establish(t, &c.h, server, make([]byte, 512))
	c.DiagnosticBind(123, 6)
	var packet [20]byte
	frame, e := NewFrame(packet[:])
	if e != nil {
		t.Fatal(e)
	}
	frame.SetSourcePort(c.h.RemotePort())
	frame.SetDestinationPort(c.h.LocalPort())
	frame.SetSegment(Segment{SEQ: c.h.scb.RecvNext(), Flags: FlagRST, WND: 64}, 5)
	if e = c.h.Recv(packet[:]); e != net.ErrClosed {
		t.Fatal("real RST teardown not reached", e)
	}
	if c.h.State() != StateClosed || c.h.diag.seen&(1<<8) == 0 {
		t.Fatal("primitive closed observation missing")
	}
	t.Log("REACHED actual received valid RST -> primitive closed state before wrapper")
}
