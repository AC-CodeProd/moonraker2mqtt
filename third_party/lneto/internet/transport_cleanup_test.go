package internet

import (
	"github.com/soypat/lneto/tcp"
	"net"
	"testing"
	"time"
)

func TestTransportNodeCleanupActualAbortGeneration(t *testing.T) {
	old := tcp.TransportDiagnostics
	tcp.TransportDiagnostics = "true"
	defer func() { tcp.TransportDiagnostics = old }()
	var c tcp.Conn
	if e := c.Configure(tcp.ConnConfig{RxBuf: make([]byte, 512), TxBuf: make([]byte, 512), TxPacketQueueSize: 4, RWBackoff: func(uint) time.Duration { return 0 }}); e != nil {
		t.Fatal(e)
	}
	if e := c.OpenListen(8080, 100); e != nil {
		t.Fatal(e)
	}
	c.DiagnosticBind(123, 7)
	n := nodeFromStackNode(&c, 8080, c.Protocol(), nil)
	if n.diagnosticConn != &c {
		t.Fatal("local metadata pointer not bound")
	}
	c.Abort()
	var h handlers
	if !h.tryHandleError(&n, net.ErrClosed) || !n.callbacks.IsZeroed() || n.diagnosticConn != nil {
		t.Fatal("actual cleanup failed")
	}
	if e := c.OpenListen(8080, 200); e != nil {
		t.Fatal(e)
	}
	c.DiagnosticBind(123, 8)
	n = nodeFromStackNode(&c, 8080, c.Protocol(), nil)
	h.nodes = []node{n}
	c.Abort()
	h.compact()
	if len(h.nodes) != 0 {
		t.Fatal("generation invalidation not compacted")
	}
	if e := c.OpenListen(8080, 300); e != nil {
		t.Fatal(e)
	}
	c.DiagnosticBind(123, 9)
	h.nodes = []node{nodeFromStackNode(&c, 8080, c.Protocol(), nil)}
	h.reset("test", 1)
	if len(h.nodes) != 0 {
		t.Fatal("reset policy changed")
	}
	t.Log("REACHED actual Abort, tryHandleError destroy, generation invalidation compact, stack handlers reset; no cleanup repair")
}
