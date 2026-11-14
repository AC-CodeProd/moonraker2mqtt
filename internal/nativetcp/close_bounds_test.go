package nativetcp

import (
	"errors"
	"io"
	"net"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestCloseDefaultBoundAndSetterCannotClearIt(t *testing.T) {
	f, o, c := closeFixture(t)
	f.paused.Store(true)
	f.a.Pump(func() {})
	interrupt, e := c.(interface{ GenerationInterrupt() (func() error, error) }).GenerationInterrupt()
	if e != nil {
		t.Fatal(e)
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
	stop, settersDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(settersDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = c.SetWriteDeadline(time.Time{})
			runtime.Gosched()
		}
	}()
	t.Cleanup(func() { close(stop); <-settersDone })
	start := time.Now()
	e = c.Close()
	elapsed := time.Since(start)
	if !errors.Is(e, os.ErrDeadlineExceeded) {
		t.Fatal(e)
	}
	if elapsed < 1900*time.Millisecond || elapsed > 2500*time.Millisecond {
		t.Fatal("default 2s drain cap", elapsed)
	}
	if e = c.SetWriteDeadline(time.Time{}); !errors.Is(e, net.ErrClosed) {
		t.Fatal("post-close setter admitted", e)
	}
	t.Logf("default cap elapsed=%s; concurrent setters cannot clear close cap", elapsed)
}

func TestCloseDoesNotBlockOtherConnection(t *testing.T) {
	f, o, c := closeFixture(t)
	other, e := f.a.NewOwner()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := other.Retire(); e != nil {
			t.Error(e)
		}
	})
	keep := f.dial(t, other, 1)
	f.paused.Store(true)
	f.a.Pump(func() {})
	if _, e = c.Write(make([]byte, 4096)); e != nil {
		t.Fatal(e)
	}
	if e = c.SetWriteDeadline(time.Now().Add(200 * time.Millisecond)); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- c.Close() }()
	t.Cleanup(func() { f.paused.Store(false); _ = o.Retire(); <-done })
	until := time.Now().Add(time.Second)
	for c.SetReadDeadline(time.Time{}) == nil && time.Now().Before(until) {
		runtime.Gosched()
	}
	f.paused.Store(false)
	if _, e = keep.Write([]byte("on")); e != nil {
		t.Fatal(e)
	}
	var got [2]byte
	if e = f.peers[1].SetReadDeadline(time.Now().Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	if _, e = io.ReadFull(&f.peers[1], got[:]); e != nil || string(got[:]) != "on" {
		t.Fatal(got, e)
	}
	select {
	case e = <-done:
		done <- e
		if !errors.Is(e, os.ErrDeadlineExceeded) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("close not bounded")
	}
}

func TestCloseRejectsAlreadyAbortedOutput(t *testing.T) {
	f, o, c := closeFixture(t)
	f.paused.Store(true)
	f.a.Pump(func() {})
	if _, e := c.Write([]byte{0xe0, 0}); e != nil {
		t.Fatal(e)
	}
	// Simulate terminal native state with reset output, independently of wrapper
	// admission. Queue emptiness alone must not become a successful drain.
	f.a.Pump(func() { o.conn.Abort() })
	if e := c.Close(); !errors.Is(e, net.ErrClosed) {
		t.Fatalf("closed state mistaken for acknowledged output: %v", e)
	}
}
