package tcp

import (
	"testing"
	"time"
)

func TestR4DeadlineAssignmentVersion(t *testing.T) {
	c := newConfiguredConn(t)
	if e := c.OpenListen(8080, 100); e != nil {
		t.Fatal(e)
	}
	_, _, v := c.DiagnosticWriteSnapshot()
	for _, setter := range []func(time.Time) error{c.SetWriteDeadline, c.SetDeadline, c.SetWriteDeadline} {
		if e := setter(time.Time{}); e != nil {
			t.Fatal(e)
		}
		_, _, n := c.DiagnosticWriteSnapshot()
		if n != v+1 {
			t.Fatal("identical assignment not counted")
		}
		v = n
	}
	if e := c.Configure(ConnConfig{RxBuf: make([]byte, 512), TxBuf: make([]byte, 512), TxPacketQueueSize: 4, RWBackoff: func(uint) time.Duration { return 0 }}); e == nil {
		t.Fatal("reconfigure open connection must retain original rejection")
	}
	_, _, n := c.DiagnosticWriteSnapshot()
	if n != v {
		t.Fatal("Configure does not assign wdead")
	}
	c.Abort()
	_, _, n = c.DiagnosticWriteSnapshot()
	if n != v+1 {
		t.Fatal("reset version erased")
	}
	c.mu.Lock()
	c.wdeadlineVersion = ^uint64(0) - 1
	c.mu.Unlock()
	if e := c.OpenListen(8081, 101); e != nil {
		t.Fatal(e)
	}
	for range 3 {
		if e := c.SetDeadline(time.Time{}); e != nil {
			t.Fatal(e)
		}
	}
	_, _, n = c.DiagnosticWriteSnapshot()
	if n != ^uint64(0) {
		t.Fatal("version wrapped")
	}
	c.Abort()
	_, _, n = c.DiagnosticWriteSnapshot()
	if n != ^uint64(0) {
		t.Fatal("saturation reset")
	}
	t.Log("REACHED every assignment, SetDeadline, reset, identical value, persistent saturation")
}
