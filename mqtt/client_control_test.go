//go:build natiu && !tinygo

package mqtt

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

func waitOwnerIdle(t *testing.T, wire *packetConn) {
	t.Helper()
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		wire.stopMu.Lock()
		idle := wire.idleRead
		wire.stopMu.Unlock()
		if idle {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("owner did not publish idle admission")
}

func TestClientOwnerQueuedBeforeIdleAdmission(t *testing.T) {
	raw := &deadlineStub{}
	wire := &packetConn{Conn: raw, receive: true, controls: make(chan clientControl, 1)}
	if err := wire.enqueueControl(clientControl{}); err != nil {
		t.Fatal(err)
	}
	var p [1]byte
	n, err := wire.Read(p[:])
	if n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) || raw.readCalls != 0 {
		t.Fatalf("missed queued control: %d %v reads=%d", n, err, raw.readCalls)
	}
}

// Simulate a byte winning concurrently with the wake deadline. The next
// physical read must restore the original progress/absolute packet bounds.
type ownerFirstByteRace struct {
	net.Conn
	mu               sync.Mutex
	deadlines        []time.Time
	entered, release chan struct{}
	reads            int
}

func (r *ownerFirstByteRace) SetReadDeadline(d time.Time) error {
	r.mu.Lock()
	r.deadlines = append(r.deadlines, d)
	r.mu.Unlock()
	return nil
}
func (r *ownerFirstByteRace) Read(p []byte) (int, error) {
	r.reads++
	if r.reads == 1 {
		close(r.entered)
		<-r.release
		p[0] = 0xd0
	} else {
		p[0] = 0
	}
	return 1, nil
}
func TestClientOwnerWakeRacesFirstByteKeepsProgressDeadline(t *testing.T) {
	raw := &ownerFirstByteRace{entered: make(chan struct{}), release: make(chan struct{})}
	wire := &packetConn{Conn: raw, controls: make(chan clientControl, 1)}
	if err := wire.beginReceive(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(raw.release) }) }
	var joined sync.WaitGroup
	t.Cleanup(func() { unblock(); joined.Wait() })
	joined.Add(1)
	go func() { defer joined.Done(); var p [1]byte; _, err := wire.Read(p[:]); done <- err }()
	<-raw.entered
	if err := wire.enqueueControl(clientControl{}); err != nil {
		t.Fatal(err)
	}
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	cap := wire.packetDeadline
	before := time.Now()
	var p [1]byte
	if n, err := wire.Read(p[:]); err != nil || n != 1 {
		t.Fatalf("wake truncated partial packet: %d %v", n, err)
	}
	raw.mu.Lock()
	deadlines := append([]time.Time(nil), raw.deadlines...)
	raw.mu.Unlock()
	if len(deadlines) != 3 || deadlines[1].After(before) || !deadlines[2].After(before.Add(time.Second)) || deadlines[2].After(time.Now().Add(receiveProgressTimeout)) || !wire.packetDeadline.Equal(cap) {
		t.Fatalf("wake/progress cap changed: %v cap=%v", deadlines, cap)
	}
}

func TestClientOwnerFailedWakeJoinsAndReconnects(t *testing.T) {
	c := newDeadlineClient()
	raw, _ := deadlineFixture(t, c)
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	_, wire := c.snapshot()
	waitOwnerIdle(t, wire)
	raw.arm("read", 1)
	if err := c.Subscribe("owner/topic", func(string, []byte) {}); !errors.Is(err, errFixtureDeadline) {
		t.Fatalf("wake failure lost: %v", err)
	}
	select {
	case <-c.done:
	default:
		t.Fatal("failed wake returned before owner join")
	}
	if len(c.controls) != 0 || len(c.controlReplies) != 0 {
		t.Fatal("orphan command/reply")
	}
	wire.stopMu.Lock()
	count := wire.subRequests
	wire.stopMu.Unlock()
	if count != 0 || c.IsConnected() {
		t.Fatal("failed wake applied native subscribe or retained connection")
	}
	raw.arm("", 0)
	_, seen := deadlineFixture(t, c)
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	if err := c.Subscribe("owner/topic", func(string, []byte) {}); err != nil {
		t.Fatal(err)
	}
	if err := c.Publish("state", []byte("rebound"), 0, false, 0); err != nil {
		t.Fatal(err)
	}
	expectDeadlinePublication(t, seen, "rebound")
}

func TestClientOwnerCanceledQueuedCommandJoinsBeforeReuse(t *testing.T) {
	a, b := net.Pipe()
	raw := &ownerReadGate{Conn: a, entered: make(chan struct{}), release: make(chan struct{}), writes: make(chan byte, 16)}
	c := newDeadlineClient()
	c.dial = func(string, string) (net.Conn, error) { return raw, nil }
	ctx, cancel := context.WithCancel(context.Background())
	brokerDone := make(chan struct{})
	result := make(chan error, 1)
	var workers sync.WaitGroup
	observeStop := make(chan struct{})
	var stopOnce sync.Once
	stopObserve := func() { stopOnce.Do(func() { close(observeStop) }) }
	t.Cleanup(func() {
		raw.unblock()
		cancel()
		stopObserve()
		_ = a.Close()
		_ = b.Close()
		workers.Wait()
		_ = c.Disconnect()
		<-brokerDone
	})
	go func() {
		defer close(brokerDone)
		r := bufio.NewReader(b)
		if _, err := readPacket(r); err != nil {
			return
		}
		if _, err := b.Write([]byte{0x20, 2, 0, 0}); err != nil {
			return
		}
		if _, err := b.Write([]byte{0x90}); err != nil {
			return
		}
		if _, err := b.Write([]byte{3}); err != nil {
			return
		}
		for {
			if _, err := readPacket(r); err != nil {
				return
			}
		}
	}()
	if err := c.ConnectContext(ctx); err != nil {
		t.Fatal(err)
	}
	<-raw.writes
	select {
	case <-raw.entered:
	case <-time.After(time.Second):
		t.Fatal("RX gate not reached")
	}
	workers.Add(2)
	go func() { defer workers.Done(); result <- c.Subscribe("owner/topic", func(string, []byte) {}) }()
	go func() {
		defer workers.Done()
		for {
			select {
			case <-observeStop:
				return
			default:
				_ = c.IsConnected()
				_ = c.observedStatus()
			}
		}
	}()
	until := time.Now().Add(time.Second)
	for len(c.controls) == 0 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if len(c.controls) != 1 {
		t.Fatal("control not queued behind partial frame")
	}
	cancel()
	<-c.lifecycle.active.cancelDone
	select {
	case <-result:
		t.Fatal("public failure returned before blocked owner joined")
	default:
	}
	raw.unblock()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("canceled command accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not join owner")
	}
	stopObserve()
	workers.Wait()
	if len(c.controls) != 0 || len(c.controlReplies) != 0 || c.IsConnected() {
		t.Fatal("stale command/reply/status after cancel")
	}
	_, seen := deadlineFixture(t, c)
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	if err := c.Subscribe("owner/topic", func(string, []byte) {}); err != nil {
		t.Fatal(err)
	}
	if err := c.Publish("state", []byte("fresh"), 0, false, 0); err != nil {
		t.Fatal(err)
	}
	expectDeadlinePublication(t, seen, "fresh")
	if c.lifecycle.starts != 3 || cap(c.controls) != 1 || cap(c.controlReplies) != 1 {
		t.Fatal("durable control/worker geometry changed")
	}
}
