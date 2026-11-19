package websocket

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type gracefulGenerationConn struct {
	*qualifiedGenerationConn
	drains   atomic.Int64
	drainErr error
}

func (c *gracefulGenerationConn) CloseGracefully() error { c.drains.Add(1); return c.drainErr }

type heldGracefulGenerationConn struct {
	net.Conn
	started, release       chan struct{}
	startOnce, releaseOnce sync.Once
	aborts                 atomic.Int64
	interruptErr           error
}

func (c *heldGracefulGenerationConn) CloseGracefully() error {
	c.startOnce.Do(func() { close(c.started) })
	<-c.release
	return nil
}

func (c *heldGracefulGenerationConn) GenerationInterrupt() (func() error, error) {
	return func() error {
		c.aborts.Add(1)
		c.releaseOnce.Do(func() { close(c.release) })
		return errors.Join(c.interruptErr, c.Close())
	}, nil
}

func TestGenerationTransportAbortPreemptsGracefulClose(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { closeGenerationTestConn(t, a); closeGenerationTestConn(t, b) })
	want := errors.New("generation interrupt reported")
	q := &heldGracefulGenerationConn{Conn: a, started: make(chan struct{}), release: make(chan struct{}), interruptErr: want}
	owned, err := ownGenerationTransport(q)
	if err != nil {
		t.Fatal(err)
	}
	closed, aborted := make(chan struct{}), make(chan struct{})
	closeResult, abortResult := make(chan error, 1), make(chan error, 1)
	go func() { defer close(closed); closeResult <- owned.Close() }()
	t.Cleanup(func() {
		q.releaseOnce.Do(func() { close(q.release) })
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Error("graceful closer not joined")
		}
	})
	select {
	case <-q.started:
	case <-time.After(time.Second):
		t.Fatal("graceful close not reached")
	}
	ws := &Conn{rwc: owned}
	go func() { defer close(aborted); abortResult <- ws.AbortTransport() }()
	t.Cleanup(func() {
		q.releaseOnce.Do(func() { close(q.release) })
		select {
		case <-aborted:
		case <-time.After(time.Second):
			t.Error("abort caller not joined")
		}
	})
	select {
	case err = <-abortResult:
		if !errors.Is(err, want) {
			t.Errorf("interrupt error lost: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("AbortTransport waited behind graceful Close")
	}
	select {
	case err = <-closeResult:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Fatal("graceful closer did not finish after abort")
	}
	if q.aborts.Load() != 1 {
		t.Fatal("captured generation interrupt not invoked exactly once")
	}
}

func TestGenerationTransportAbortLegacyFallback(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { closeGenerationTestConn(t, a); closeGenerationTestConn(t, b) })
	q := &qualifiedGenerationConn{Conn: a}
	owned, err := ownGenerationTransport(q)
	if err != nil {
		t.Fatal(err)
	}
	ws := &Conn{rwc: owned}
	for range 2 {
		if err = ws.AbortTransport(); err != nil {
			t.Fatal(err)
		}
	}
	if q.interrupts.Load() != 1 {
		t.Fatal("legacy interrupt/Close once semantics changed")
	}
}

func TestGenerationTransportQualifiedGracefulClose(t *testing.T) {
	a, b := net.Pipe()
	defer closeGenerationTestConn(t, a)
	defer closeGenerationTestConn(t, b)
	want := errors.New("bounded drain failed")
	q := &gracefulGenerationConn{qualifiedGenerationConn: &qualifiedGenerationConn{Conn: a}, drainErr: want}
	owned, e := ownGenerationTransport(q)
	if e != nil {
		t.Fatal(e)
	}
	for range 2 {
		if e = owned.Close(); !errors.Is(e, want) {
			t.Errorf("graceful error lost: %v", e)
		}
	}
	if q.drains.Load() != 1 || q.interrupts.Load() != 0 {
		t.Fatal("graceful capability bypassed", q.drains.Load(), q.interrupts.Load())
	}
}
