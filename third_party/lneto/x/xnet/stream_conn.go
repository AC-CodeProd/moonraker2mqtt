// This adapter uses only the public Go net API and lneto capabilities.
package xnet

import (
	"io"
	"net"
	"sync"
	"time"
)

// Stream is the explicit capability supplied by the pinned network dependency.
// Interruption is generation-bound, and is separate from graceful Close.
type Stream interface {
	Recv([]byte, time.Time) (int, error)
	Send([]byte, time.Time) (int, error)
	Close() error
	GenerationInterrupt() (func() error, error)
	DiagnosticFD() int
}

type StreamConn struct {
	stream                      Stream
	remote                      net.Addr
	mu                          sync.Mutex
	readDeadline, writeDeadline time.Time
}

var _ net.Conn = (*StreamConn)(nil)

func BindStream(stream Stream, remote net.Addr) *StreamConn {
	return &StreamConn{stream: stream, remote: remote}
}
func (c *StreamConn) Read(b []byte) (int, error) {
	c.mu.Lock()
	t := c.readDeadline
	c.mu.Unlock()
	n, err := c.stream.Recv(b, t)
	if n < 0 {
		n = 0
	}
	if err != nil && err != io.EOF {
		err = &net.OpError{Op: "read", Net: "tcp", Addr: c.remote, Err: err}
	}
	return n, err
}
func (c *StreamConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	t := c.writeDeadline
	c.mu.Unlock()
	n, err := c.stream.Send(b, t)
	if n < 0 {
		n = 0
	}
	if err != nil {
		err = &net.OpError{Op: "write", Net: "tcp", Addr: c.remote, Err: err}
	}
	return n, err
}
func (c *StreamConn) Close() error { return c.stream.Close() }

// The Berkeley capability does not export the assigned local endpoint.
// Return unknown rather than inventing an address or reading private layouts.
func (c *StreamConn) LocalAddr() net.Addr  { return nil }
func (c *StreamConn) RemoteAddr() net.Addr { return c.remote }
func (c *StreamConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline, c.writeDeadline = t, t
	c.mu.Unlock()
	return nil
}
func (c *StreamConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	c.mu.Unlock()
	return nil
}
func (c *StreamConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.writeDeadline = t
	c.mu.Unlock()
	return nil
}
func (c *StreamConn) GenerationInterrupt() (func() error, error) {
	return c.stream.GenerationInterrupt()
}
func (c *StreamConn) DiagnosticFD() int { return c.stream.DiagnosticFD() }
