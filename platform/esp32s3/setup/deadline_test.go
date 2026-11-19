package setup

import (
	"bytes"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

type memConn struct {
	reader      *strings.Reader
	output      bytes.Buffer
	deadline    time.Time
	calls       int
	unsupported bool
	closed      bool
}

func (c *memConn) Read(p []byte) (int, error)  { return c.reader.Read(p) }
func (c *memConn) Write(p []byte) (int, error) { return c.output.Write(p) }
func (c *memConn) Close() error                { c.closed = true; return nil }
func (c *memConn) LocalAddr() net.Addr         { return nil }
func (c *memConn) RemoteAddr() net.Addr        { return nil }
func (c *memConn) SetDeadline(t time.Time) error {
	c.calls++
	c.deadline = t
	if c.unsupported {
		return io.ErrClosedPipe
	}
	return nil
}
func (c *memConn) SetReadDeadline(t time.Time) error  { return c.SetDeadline(t) }
func (c *memConn) SetWriteDeadline(t time.Time) error { return c.SetDeadline(t) }
func TestDeadlineAndIncompleteBody(t *testing.T) {
	p := Portal{Token: "token", Save: func([]byte) error { t.Fatal("unexpected save"); return nil }}
	c := &memConn{reader: strings.NewReader(post("{}")), unsupported: true}
	if p.Handle(c) || c.calls != 1 || !c.closed || c.output.Len() != 0 {
		t.Fatal("deadline failure was not fail-closed")
	}
	raw := post("SECRET")
	raw = raw[:len(raw)-2] // EOF before Content-Length
	c = &memConn{reader: strings.NewReader(raw)}
	before := time.Now()
	if p.Handle(c) || c.calls != 1 || !c.closed || !strings.HasPrefix(c.output.String(), "HTTP/1.1 400") || strings.Contains(c.output.String(), "SECRET") {
		t.Fatal("incomplete body")
	}
	delta := c.deadline.Sub(before)
	if delta < 7*time.Second || delta > 9*time.Second {
		t.Fatal("not an absolute eight-second cap")
	}
}
