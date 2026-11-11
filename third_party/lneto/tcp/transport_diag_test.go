package tcp

import (
	"net"
	"testing"
	"time"
)

func diagnosticEnable(t *testing.T) {
	old := TransportDiagnostics
	TransportDiagnostics = "true"
	t.Cleanup(func() { TransportDiagnostics = old })
}

func TestTransportDiagnosticGenerationReuse(t *testing.T) {
	diagnosticEnable(t)
	c := newConfiguredConn(t)
	if err := c.OpenListen(8080, 100); err != nil {
		t.Fatal(err)
	}
	c.DiagnosticBind(123, 3)
	old := c.DiagnosticBegin(false)
	c.Abort()
	if c.h.diag.seen&(1<<6) == 0 || c.h.diag.seen&(1<<7) == 0 {
		t.Fatal("Abort/reset not reached")
	}
	if err := c.OpenListen(8080, 200); err != nil {
		t.Fatal(err)
	}
	c.DiagnosticBind(123, 3)
	current := c.h.connid
	if old == current {
		t.Fatal("no generation change")
	}
	c.DiagnosticEnd(1, old, 2, 0, net.ErrClosed)
	if c.h.diag.seen&(1<<1) != 0 || c.h.diag.seen&(1<<31) == 0 {
		t.Fatal("stale failure consumed current first error")
	}
	c.DiagnosticEnd(1, current, 2, 0, net.ErrClosed)
	if c.h.diag.seen&(1<<1) == 0 {
		t.Fatal("new first error missing")
	}
	t.Log("REACHED same-fd same-object actual Abort/Open generationChanged stale error separated")
}

func TestTransportDiagnosticFirstEventsAndOFF(t *testing.T) {
	diagnosticEnable(t)
	c := newConfiguredConn(t)
	c.DiagnosticBind(123, 4)
	generation := c.DiagnosticBegin(true)
	n, err := c.Write([]byte{1, 2})
	if n != 0 || err != net.ErrClosed {
		t.Fatal("underlying failure changed", n, err)
	}
	c.DiagnosticEnd(1, generation, 2, n, err)
	c.DiagnosticEnd(3, generation, 0, 0, c.Close())
	first := c.h.diag.seq
	c.DiagnosticEnd(1, generation, 2, 0, net.ErrClosed)
	c.DiagnosticBegin(true)
	if first != 4 || c.h.diag.seq != first {
		t.Fatal("bounded first events", first, c.h.diag.seq)
	}
	TransportDiagnostics = "false"
	before := c.h.diag
	c.DiagnosticEnd(6, generation, 0, 0, nil)
	if c.h.diag != before {
		t.Fatal("OFF mutated diagnostic metadata")
	}
	t.Log("REACHED actual closed Write/Close, first ordering, OFF")
}

func TestTransportDiagnosticConcurrentCloseWrite(t *testing.T) {
	diagnosticEnable(t)
	c := newConfiguredConn(t)
	c.DiagnosticBind(123, 5)
	done := make(chan struct{}, 2)
	go func() {
		g := c.DiagnosticBegin(false)
		n, e := c.Write([]byte{1})
		c.DiagnosticEnd(1, g, 1, n, e)
		done <- struct{}{}
	}()
	go func() {
		g := c.DiagnosticBegin(true)
		e := c.Close()
		c.DiagnosticEnd(3, g, 0, 0, e)
		done <- struct{}{}
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("deadlock")
		}
	}
	if c.h.diag.seq != 4 {
		t.Fatal("events lost", c.h.diag.seq)
	}
	t.Log("REACHED actual concurrent primitive error/Close")
}
