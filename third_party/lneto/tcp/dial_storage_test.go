package tcp

import (
	"errors"
	"github.com/soypat/lneto"
	"math"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestDialStorageFreshConnStableBackings(t *testing.T) {
	var s DialStorage
	s.Init()
	backoff := lneto.BackoffStrategy(func(uint) time.Duration { return 0 })
	var previous *Conn
	var addresses [4]any
	for i := 0; i < 32; i++ {
		c, token, e := s.Acquire(backoff)
		if e != nil {
			t.Fatal(e)
		}
		if c == previous || token != uint64(i+1) {
			t.Fatal("identity/token reused")
		}
		got := [4]any{&c.h.bufTx.rawbuf[0], &c.h.bufRx.Buf[0], &c.h.bufTx.slist.pkts[:cap(c.h.bufTx.slist.pkts)][0], &c.h.reasm.held[:cap(c.h.reasm.held)][0]}
		if i == 0 {
			addresses = got
		} else if got != addresses {
			t.Fatal("backing allocation renewed")
		}
		if cap(c.h.bufTx.rawbuf) != 4096 || cap(c.h.bufRx.Buf) != 1024 || cap(c.h.bufTx.slist.pkts) != 4 || cap(c.h.reasm.held) != 8 {
			t.Fatal("geometry changed")
		}
		if _, _, e = s.Acquire(backoff); !errors.Is(e, ErrDialStorageBusy) {
			t.Fatal("saturation not refused", e)
		}
		runtime.GC()
		if e = s.Retire(c, token, func() error { c.Abort(); return nil }); e != nil {
			t.Fatal(e)
		}
		if c.h.bufTx.rawbuf != nil || c.h.bufRx.Buf != nil || c.h.bufTx.slist.pkts != nil || c.h.reasm.held != nil {
			t.Fatal("old handler retains slices")
		}
		if e = s.Retire(c, token, func() error { return nil }); !errors.Is(e, net.ErrClosed) {
			t.Fatal("double release accepted")
		}
		if _, e = c.Write([]byte("old")); !errors.Is(e, net.ErrClosed) {
			t.Fatal("old capability accepted", e)
		}
		previous = c
	}
}

func TestDialStorageRetirementWaitsNativeSnapshot(t *testing.T) {
	var s DialStorage
	s.Init()
	c, token, e := s.Acquire(func(uint) time.Duration { return 0 })
	if e != nil {
		t.Fatal(e)
	}
	pinned, e := c.PinNativeCall()
	if e != nil || !pinned {
		t.Fatal("pin not acquired", e)
	}
	detached := make(chan struct{})
	retired := make(chan error, 1)
	go func() { retired <- s.Retire(c, token, func() error { c.Abort(); close(detached); return nil }) }()
	<-detached
	if _, _, e = s.Acquire(func(uint) time.Duration { return 0 }); !errors.Is(e, ErrDialStorageBusy) {
		t.Fatal("reused while native snapshot outstanding")
	}
	select {
	case e = <-retired:
		t.Fatal("retire returned before native snapshot", e)
	default:
	}
	if _, e = c.PinNativeCall(); !errors.Is(e, net.ErrClosed) {
		t.Fatal("new old-generation admission accepted")
	}
	c.UnpinNativeCall()
	if e = <-retired; e != nil {
		t.Fatal(e)
	}
	next, nt, e := s.Acquire(func(uint) time.Duration { return 0 })
	if e != nil {
		t.Fatal(e)
	}
	if next == c || nt == token {
		t.Fatal("new identity not fresh")
	}
	if _, e = c.Read(make([]byte, 1)); !errors.Is(e, net.ErrClosed) {
		t.Fatal("stale native read accepted")
	}
	if e = s.Retire(next, nt, func() error { next.Abort(); return nil }); e != nil {
		t.Fatal(e)
	}
}

func TestDialStorageOverflowAndIsolation(t *testing.T) {
	var a, b DialStorage
	a.Init()
	b.Init()
	ac, at, e := a.Acquire(func(uint) time.Duration { return 0 })
	if e != nil {
		t.Fatal(e)
	}
	bc, bt, e := b.Acquire(func(uint) time.Duration { return 0 })
	if e != nil {
		t.Fatal(e)
	}
	if &ac.h.bufTx.rawbuf[0] == &bc.h.bufTx.rawbuf[0] || &ac.h.bufRx.Buf[0] == &bc.h.bufRx.Buf[0] {
		t.Fatal("owners alias")
	}
	if e = a.Retire(bc, bt, func() error { return nil }); !errors.Is(e, net.ErrClosed) {
		t.Fatal("foreign owner accepted")
	}
	_ = a.Retire(ac, at, func() error { ac.Abort(); return nil })
	_ = b.Retire(bc, bt, func() error { bc.Abort(); return nil })
	a.token = math.MaxUint64
	if _, _, e = a.Acquire(func(uint) time.Duration { return 0 }); !errors.Is(e, ErrDialStorageExhausted) {
		t.Fatal("token wrapped", e)
	}
}

func TestDialStorageConcurrentAcquisitionBound(t *testing.T) {
	var s DialStorage
	s.Init()
	type result struct {
		c     *Conn
		token uint64
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 32)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			c, token, e := s.Acquire(func(uint) time.Duration { return 0 })
			results <- result{c, token, e}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var winner result
	wins := 0
	busy := 0
	for r := range results {
		if r.err == nil {
			wins++
			winner = r
		} else if errors.Is(r.err, ErrDialStorageBusy) {
			busy++
		} else {
			t.Fatal(r.err)
		}
	}
	if wins != 1 || busy != 31 {
		t.Fatal("acquisition not exclusive/bounded", wins, busy)
	}
	if e := s.Retire(winner.c, winner.token, func() error { winner.c.Abort(); return nil }); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.Acquire(nil); !errors.Is(e, lneto.ErrMissingHALConfig) {
		t.Fatal("Configure prerequisite failure not explicit", e)
	}
	c, token, e := s.Acquire(func(uint) time.Duration { return 0 })
	if e != nil {
		t.Fatal("failed configuration leaked lease", e)
	}
	_ = s.Retire(c, token, func() error { c.Abort(); return nil })
}
