package xnet

import (
	"context"
	"errors"
	"github.com/soypat/lneto/tcp"
	"net"
	"sync"
	"time"
)

// BerkeleyStream owns a private Berkeley identity table for one fresh stream.
// Numeric fds in other streams (including a later fd=3) cannot alias it.
// The default radio/MQTT Berkeley instance is never replaced or reset.
type BerkeleyStream struct {
	stack     *StackBerkeley
	fd        int
	closeOnce sync.Once
	closeErr  error
}

// DiagnosticFD exposes only the immutable descriptor used by Send/Recv.
// The descriptor is local to this stream's private table, not a capability.
func (s *BerkeleyStream) DiagnosticFD() int {
	if s == nil {
		return -1
	}
	return s.fd
}

func (s *BerkeleyStream) Recv(b []byte, t time.Time) (int, error) { return s.stack.Recv(s.fd, b, 0, t) }
func (s *BerkeleyStream) Send(b []byte, t time.Time) (int, error) { return s.stack.Send(s.fd, b, 0, t) }
func (s *BerkeleyStream) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.stack.Close(s.fd) })
	return s.closeErr
}
func (s *BerkeleyStream) GenerationInterrupt() (func() error, error) {
	return s.stack.GenerationInterrupt(s.fd)
}

// Called only after the exact Conn has permanently closed native admissions.
// Do not call Close here: this may run while that Close is itself in flight.
func (s *BerkeleyStream) detachNative(expected *tcp.Conn) error {
	b := s.stack
	b.mu.Lock()
	defer b.mu.Unlock()
	entry := b.getConn(s.fd)
	if entry.isvalid() {
		active, ok := entry.sock.(tcpconn)
		if !ok || active.Conn != expected {
			return net.ErrClosed
		}
		b.conns = deleteFD(b.conns, s.fd)
	}
	b.pendingFDs = deleteFD(b.pendingFDs, s.fd)
	return nil
}

// NativeDialOwner strongly owns exactly one reserve. Construct once before app
// activity, not on retries. Retire waits for the dial itself and is called only
// after app reader/writer/cancellation joins. No worker is created by this type.
type NativeDialOwner struct {
	mu       sync.Mutex
	storage  tcp.DialStorage
	goStack  StackGo
	attempt  *OwnedDial
	stream   *BerkeleyStream
	dialDone chan struct{}
}

func NewNativeDialOwner(s StackGo) *NativeDialOwner {
	d := &NativeDialOwner{goStack: s}
	d.storage.Init()
	return d
}
func (d *NativeDialOwner) DialContext(ctx context.Context, network string, remote *net.TCPAddr) (stream *BerkeleyStream, err error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if network != "tcp" && network != "tcp4" {
		return nil, errors.New("owned native dial requires IPv4 TCP")
	}
	d.mu.Lock()
	if d.attempt != nil {
		d.mu.Unlock()
		return nil, tcp.ErrDialStorageBusy
	}
	a := NewOwnedDial(&d.storage)
	done := make(chan struct{})
	d.attempt = a
	d.dialDone = done
	d.mu.Unlock()
	defer close(done)
	b := NewBerkeleyStack(func(_ context.Context, n string, f, t int, l, r net.Addr) (any, error) {
		return d.goStack.Socket(a.Context(ctx), n, f, t, l, r)
	})
	fd, e := b.Socket(AF_INET, SOCK_STREAM, 6)
	if e != nil {
		return nil, e
	}
	stream = &BerkeleyStream{stack: b, fd: fd}
	a.detachFD = stream.detachNative
	if e = b.Connect(fd, "", remote.AddrPort()); e != nil {
		_ = b.Close(fd)
		return nil, e
	}
	d.mu.Lock()
	d.stream = stream
	d.mu.Unlock()
	return stream, nil
}
func (d *NativeDialOwner) Retire() error {
	d.mu.Lock()
	a, done := d.attempt, d.dialDone
	d.mu.Unlock()
	if a == nil {
		return nil
	}
	<-done
	// Close admissions and interrupt BEFORE waiting for any native Close.
	a.mu.Lock()
	already := a.retired
	a.mu.Unlock()
	var err error
	if !already {
		err = a.Retire()
	} else {
		<-a.retireDone
		a.mu.Lock()
		err = a.retireErr
		a.mu.Unlock()
	}
	// Automatic unwind from Socket failures is synchronous before dialDone.
	// Concurrent external Retire calls are not permitted by the lifecycle owner.
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.attempt != a {
		return net.ErrClosed
	}
	d.attempt = nil
	d.stream = nil
	d.dialDone = nil
	return nil
}
