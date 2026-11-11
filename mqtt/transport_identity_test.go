//go:build natiu && !tinygo

package mqtt

import (
	"io"
	"net"
	"os"
	"testing"
	"time"
)

type nonComparableError []byte

func (nonComparableError) Error() string { panic("diagnostic must not call Error") }

func TestTransportPrimitiveIdentityNoMethods(t *testing.T) {
	for _, p := range []struct {
		e    error
		code uint8
	}{{nil, 0}, {io.EOF, 3}, {io.ErrShortWrite, 2}, {net.ErrClosed, 4}, {os.ErrDeadlineExceeded, 1}, {&net.OpError{Err: os.ErrDeadlineExceeded}, 1}, {nonComparableError{1}, 255}, {(*net.OpError)(nil), 255}} {
		if got := diagCode(p.e); got != p.code {
			t.Fatal("primitive code", got, p.code)
		}
	}
	t.Log("REACHED singleton raw interface identity, non-comparable arbitrary error without Error/Timeout, typed nil safe")
}

type promotedSetterConn struct {
	*diagTestConn
	reads, all int
	sentinel   error
}

func (c *promotedSetterConn) SetReadDeadline(time.Time) error { c.reads++; return c.sentinel }
func (c *promotedSetterConn) SetDeadline(time.Time) error     { c.all++; return c.sentinel }
func TestTransportPromotedSettersPreserveResults(t *testing.T) {
	enableDiag(t)
	sentinel := nonComparableError{1}
	raw := &promotedSetterConn{diagTestConn: &diagTestConn{seterr: io.EOF}, sentinel: sentinel}
	c := &packetConn{Conn: raw}
	if c.SetWriteDeadline(time.Now()) != io.EOF {
		t.Fatal("write setter result changed")
	}
	if _, ok := c.SetReadDeadline(time.Now()).(nonComparableError); !ok {
		t.Fatal("read setter result changed")
	}
	if _, ok := c.SetDeadline(time.Now()).(nonComparableError); !ok {
		t.Fatal("all setter result changed")
	}
	if raw.reads != 1 || raw.all != 1 {
		t.Fatal("underlying calls changed")
	}
	if n, e := c.Write([]byte{1}); n != 1 || e != nil {
		t.Fatal("ignored write setter policy corrected silently")
	}
	seq, w, s, cl := state(c)
	if seq != 1 || w || !s || cl {
		t.Fatal("first setter cardinality", seq)
	}
	t.Log("REACHED promoted setters return exact errors; Write still ignores setter error")
}
