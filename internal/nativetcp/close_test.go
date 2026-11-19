package nativetcp

import (
	"errors"
	"io"
	"net"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

func closeFixture(t *testing.T) (*publicFixture, *PublicOwner, net.Conn) {
	t.Helper()
	f := fixture(t)
	o, err := f.a.NewOwner()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := o.Retire(); err != nil {
			t.Error(err)
		}
	})
	return f, o, f.dial(t, o, 0)
}

func TestCloseAcknowledgesFinalBytes(t *testing.T) {
	f, o, c := closeFixture(t)
	f.paused.Store(true)
	// Join a pump callback to ensure the pause has taken effect.
	f.a.Pump(func() {})
	if n, e := c.Write([]byte{0xe0, 0}); n != 2 || e != nil {
		t.Fatal(n, e)
	}
	done := make(chan error, 1)
	go func() { done <- c.Close() }()
	t.Cleanup(func() { f.paused.Store(false); _ = o.Retire(); <-done })
	select {
	case e := <-done:
		done <- e
		t.Fatalf("close discarded queued final bytes: %v", e)
	case <-time.After(25 * time.Millisecond):
	}
	f.paused.Store(false)
	var got [2]byte
	if e := f.peers[0].SetReadDeadline(time.Now().Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	if _, e := io.ReadFull(&f.peers[0], got[:]); e != nil || got != [2]byte{0xe0, 0} {
		t.Fatal(got, e)
	}
	select {
	case e := <-done:
		done <- e
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("ACK drain failed")
	}
}

func TestCloseWithholdsACKAndHonorsWriteCap(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "withheldACK", true: "expired"}[expired], func(t *testing.T) {
			f, o, c := closeFixture(t)
			f.dropReturn.Store(true)
			cap := time.Now().Add(100 * time.Millisecond)
			if expired {
				cap = time.Now().Add(-time.Second)
			}
			if _, e := c.Write([]byte{0xe0, 0}); e != nil {
				t.Fatal(e)
			}
			if e := c.SetWriteDeadline(cap); e != nil {
				t.Fatal(e)
			}
			if !expired {
				until := time.Now().Add(50 * time.Millisecond)
				for o.conn.BufferedUnsent() != 0 && time.Now().Before(until) {
					runtime.Gosched()
				}
				if o.conn.BufferedUnsent() != 0 || o.conn.FreeOutput() == len(o.tx) {
					t.Fatal("not sent and unacknowledged")
				}
			}
			start := time.Now()
			e := c.Close()
			if !errors.Is(e, os.ErrDeadlineExceeded) {
				t.Fatalf("unacknowledged close: %v", e)
			}
			if time.Since(start) > 500*time.Millisecond {
				t.Fatal("earlier write cap ignored")
			}
			if e2 := c.Close(); e2 != e {
				t.Fatal("non-idempotent", e, e2)
			}
		})
	}
}

func TestCloseBlockedWriterAndExplicitInterrupt(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "bounded", true: "interrupt"}[cancel], func(t *testing.T) {
			f, o, c := closeFixture(t)
			interrupt, e := c.(interface{ GenerationInterrupt() (func() error, error) }).GenerationInterrupt()
			if e != nil {
				t.Fatal(e)
			}
			f.paused.Store(true)
			f.a.Pump(func() {})
			if !cancel {
				if e = c.SetWriteDeadline(time.Now().Add(100 * time.Millisecond)); e != nil {
					t.Fatal(e)
				}
			}
			written := make(chan error, 1)
			go func() { _, e := c.Write(make([]byte, 4097)); written <- e }()
			t.Cleanup(func() { _ = interrupt(); <-written })
			until := time.Now().Add(time.Second)
			for o.conn.FreeOutput() != 0 && time.Now().Before(until) {
				runtime.Gosched()
			}
			if o.conn.FreeOutput() != 0 {
				t.Fatal("writer not blocked")
			}
			closed := make(chan error, 1)
			go func() { closed <- c.Close() }()
			t.Cleanup(func() { _ = interrupt(); <-closed })
			select {
			case e := <-closed:
				closed <- e
				t.Fatalf("premature close: %v", e)
			case <-time.After(20 * time.Millisecond):
			}
			if cancel {
				if e = interrupt(); e != nil {
					t.Fatal(e)
				}
			}
			select {
			case e = <-closed:
				closed <- e
				if cancel && !errors.Is(e, net.ErrClosed) {
					t.Fatal(e)
				}
				if !cancel && !errors.Is(e, os.ErrDeadlineExceeded) {
					t.Fatal(e)
				}
			case <-time.After(500 * time.Millisecond):
				t.Fatal("close blocked")
			}
			select {
			case e = <-written:
				written <- e
				if e == nil {
					t.Fatal("writer succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("writer abandoned")
			}
		})
	}
}

func TestCloseConcurrentRetireReuseAndStaleInterrupt(t *testing.T) {
	f, o, c := closeFixture(t)
	interrupt, e := c.(interface{ GenerationInterrupt() (func() error, error) }).GenerationInterrupt()
	if e != nil {
		t.Fatal(e)
	}
	f.paused.Store(true)
	f.a.Pump(func() {})
	if _, e = c.Write([]byte("ok")); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := c.Close(); e != nil {
				t.Error(e)
			}
		}()
	}
	// Wait until close stops new admission, without touching native storage.
	until := time.Now().Add(time.Second)
	for c.SetWriteDeadline(time.Time{}) == nil && time.Now().Before(until) {
		runtime.Gosched()
	}
	retired := make(chan error, 1)
	go func() { retired <- o.Retire() }()
	t.Cleanup(func() { f.paused.Store(false); wg.Wait(); <-retired })
	select {
	case e := <-retired:
		retired <- e
		t.Fatalf("retire overtook closer: %v", e)
	case <-time.After(20 * time.Millisecond):
	}
	f.paused.Store(false)
	wg.Wait()
	if e := <-retired; e != nil {
		retired <- e
		t.Fatal(e)
	}
	retired <- nil
	fresh := f.dial(t, o, 0)
	if e = c.Close(); e != nil {
		t.Fatal(e)
	}
	if e = interrupt(); e != nil {
		t.Fatal(e)
	}
	if _, e = fresh.Write([]byte("new")); e != nil {
		t.Fatal(e)
	}
	var got [3]byte
	if e = f.peers[0].SetReadDeadline(time.Now().Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	if _, e = io.ReadFull(&f.peers[0], got[:]); e != nil || string(got[:]) != "new" {
		t.Fatal(string(got[:]), e)
	}
}
