package websocket

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type qualifiedGenerationConn struct {
	net.Conn
	claims, interrupts atomic.Int64
	rejection          error
}

func (c *qualifiedGenerationConn) GenerationInterrupt() (func() error, error) {
	if c.claims.Add(1) != 1 || c.rejection != nil {
		if c.rejection != nil {
			return nil, c.rejection
		}
		return nil, net.ErrClosed
	}
	return func() error {
		if c.interrupts.Add(1) != 1 {
			return net.ErrClosed
		}
		return c.Close()
	}, nil
}

func closeGenerationTestConn(t *testing.T, c net.Conn) {
	t.Helper()
	if err := c.Close(); err != nil {
		t.Errorf("close generation test connection: %v", err)
	}
}

func TestGenerationTransportOwnsCapabilityAndIdempotentConcurrentClose(t *testing.T) {
	a, b := net.Pipe()
	defer closeGenerationTestConn(t, a)
	defer closeGenerationTestConn(t, b)
	q := &qualifiedGenerationConn{Conn: a}
	owned, e := ownGenerationTransport(q)
	if e != nil {
		t.Fatal(e)
	}
	writeDone := make(chan error, 1)
	go func() { _, e := owned.Write([]byte("bounded")); writeDone <- e }()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := owned.Close(); err != nil {
				t.Errorf("close owned transport: %v", err)
			}
		}()
	}
	wg.Wait()
	select {
	case e := <-writeDone:
		if e == nil {
			t.Fatal("blocked write succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("writer did not join")
	}
	if q.claims.Load() != 1 || q.interrupts.Load() != 1 {
		t.Fatal("capability not single-owned", q.claims.Load(), q.interrupts.Load())
	}
	if _, e = q.GenerationInterrupt(); !errors.Is(e, net.ErrClosed) {
		t.Fatal("duplicate capability not refused")
	}
}
func TestGenerationTransportCapabilityRejectionDoesNotFallback(t *testing.T) {
	a, b := net.Pipe()
	defer closeGenerationTestConn(t, a)
	defer closeGenerationTestConn(t, b)
	sentinel := errors.New("stale generation")
	q := &qualifiedGenerationConn{Conn: a, rejection: sentinel}
	if got, e := ownGenerationTransport(q); got != q || !errors.Is(e, sentinel) {
		t.Fatal("rejected qualified socket silently fell back", e)
	}
}
func TestGenerationTransportHostedUnqualifiedFallbackUnchanged(t *testing.T) {
	if requireNativeGeneration {
		t.Fatal("host fallback fixture requires hosted compilation")
	}
	a, b := net.Pipe()
	defer closeGenerationTestConn(t, a)
	defer closeGenerationTestConn(t, b)
	got, e := ownGenerationTransport(a)
	if e != nil || got != a {
		t.Fatal("host unqualified fallback changed", e)
	}
}
