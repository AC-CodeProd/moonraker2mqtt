package tcp

import (
	"testing"
	"time"
)

func TestSPECMonotoneEffectiveDeadline(t *testing.T) {
	diagnosticEnable(t)
	c := newConfiguredConn(t)
	if err := c.OpenListen(8080, 100); err != nil {
		t.Fatal(err)
	}
	c.DiagnosticBind(123, 3)
	before := DiagnosticNow()
	for _, deadline := range []time.Time{{}, time.Now().Add(-time.Second), time.Now().Add(time.Second)} {
		if err := c.SetWriteDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		present, scalar := c.DiagnosticDeadline()
		if present != !deadline.IsZero() {
			t.Fatal("presence")
		}
		if !present && scalar != 0 {
			t.Fatal("absence must normalize zero")
		}
		if present && scalar != deadline.Sub(diagnosticEpoch).Nanoseconds() {
			t.Fatal("different epoch")
		}
		if !deadline.IsZero() && ((deadline.Before(time.Now()) && scalar >= DiagnosticNow()) || (deadline.After(time.Now()) && scalar <= DiagnosticNow())) {
			t.Fatal("expired/future relation")
		}
	}
	if DiagnosticNow() < before {
		t.Fatal("monotone clock")
	}
	t.Log("REACHED zero/expired/future effective existing wdead, immutable epoch, monotone delta")
}

func TestSPECFirstCloseOwnershipToken(t *testing.T) {
	diagnosticEnable(t)
	c := newConfiguredConn(t)
	c.DiagnosticBind(123, 4)
	g1, first1, stamp1 := c.DiagnosticCloseBegin()
	g2, first2, stamp2 := c.DiagnosticCloseBegin()
	if !first1 || first2 || g1 != g2 || stamp2 < stamp1 {
		t.Fatal("ownership token")
	}
	// Neither call performs/suppresses native Close; no extra per-connection fields.
	t.Log("REACHED one local first token, second neutral-to-first invocation")
}
