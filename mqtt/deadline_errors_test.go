//go:build natiu && !tinygo

package mqtt

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
)

var errFixtureDeadline = errors.New("injected MQTT deadline failure")

type deadlineStub struct {
	net.Conn
	readCalls, writeCalls       int
	readDeadline, writeDeadline time.Time
	failure                     error
}

func (s *deadlineStub) SetReadDeadline(d time.Time) error  { s.readDeadline = d; return s.failure }
func (s *deadlineStub) SetWriteDeadline(d time.Time) error { s.writeDeadline = d; return s.failure }
func (s *deadlineStub) Read(p []byte) (int, error)         { s.readCalls++; p[0] = 0xd0; return 1, nil }
func (s *deadlineStub) Write(p []byte) (int, error)        { s.writeCalls++; return len(p), nil }

func TestNatiuDeadlineProgressFailureDoesNotRead(t *testing.T) {
	s := &deadlineStub{failure: errFixtureDeadline}
	wire := &packetConn{Conn: s, receive: true, packetDeadline: time.Now().Add(receivePacketTimeout), packetBytes: 1, prefixLen: 1}
	wire.prefix[0] = 0xd0
	var p [1]byte
	n, err := wire.Read(p[:])
	if n != 0 || !errors.Is(err, errFixtureDeadline) || s.readCalls != 0 {
		t.Fatalf("progress setter failed: n=%d err=%v reads=%d", n, err, s.readCalls)
	}
	if wire.packetBytes != 1 || wire.prefixLen != 1 || wire.prefix[0] != 0xd0 {
		t.Fatal("failed read changed packet ownership or progress")
	}
}

func TestNatiuDeadlineWriteFailureDoesNotWrite(t *testing.T) {
	for _, diagnostics := range []string{"false", "true"} {
		t.Run(diagnostics, func(t *testing.T) {
			before := TransportDiagnostics
			TransportDiagnostics = diagnostics
			defer func() { TransportDiagnostics = before }()
			s := &deadlineStub{failure: errFixtureDeadline}
			wire := &packetConn{Conn: s}
			n, err := wire.Write([]byte{0xc0, 0})
			if n != 0 || !errors.Is(err, errFixtureDeadline) || s.writeCalls != 0 {
				t.Fatalf("write setter failed: n=%d err=%v writes=%d", n, err, s.writeCalls)
			}
			if diagnostics == "true" && (!wire.diag.setterSeen || wire.diag.writeSeen || wire.diag.seq != 1) {
				t.Fatal("setter diagnostic was lost or a write was reported")
			}
		})
	}
}

func TestNatiuDeadlineProgressBoundsAndWriteSuccess(t *testing.T) {
	for _, remaining := range []time.Duration{time.Second, receivePacketTimeout} {
		s := &deadlineStub{}
		limit := time.Now().Add(remaining)
		wire := &packetConn{Conn: s, receive: true, packetDeadline: limit}
		start := time.Now()
		var p [1]byte
		if n, err := wire.Read(p[:]); n != 1 || err != nil || s.readCalls != 1 {
			t.Fatalf("healthy read n=%d err=%v calls=%d", n, err, s.readCalls)
		}
		if s.readDeadline.After(limit) || s.readDeadline.After(time.Now().Add(receiveProgressTimeout)) || s.readDeadline.Before(start) {
			t.Fatal("progress deadline exceeded existing packet/progress limits")
		}
		if remaining == time.Second && !s.readDeadline.Equal(limit) {
			t.Fatal("hard packet cap was not preserved")
		}
		if n, err := wire.Write([]byte{0xc0, 0}); n != 2 || err != nil || s.writeCalls != 1 {
			t.Fatalf("healthy write n=%d err=%v calls=%d", n, err, s.writeCalls)
		}
		if s.writeDeadline.Before(start.Add(10*time.Second)) || s.writeDeadline.After(time.Now().Add(10*time.Second)) {
			t.Fatal("write budget changed")
		}
	}
}

// Fault selection and transport counters are synchronized with the real RX worker.
// The wrapper rejects the setter, not the subsequent I/O, so the guard itself is tested.
type deadlineFaultConn struct {
	net.Conn
	mu                               sync.Mutex
	kind                             string
	at, calls, reads, writes, closes int
	readsAtFailure                   int
}

func (s *deadlineFaultConn) arm(kind string, at int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kind, s.at, s.calls = kind, at, 0
}
func (s *deadlineFaultConn) reject(kind string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.kind != kind {
		return false
	}
	s.calls++
	if s.calls == s.at {
		s.readsAtFailure = s.reads
		return true
	}
	return false
}
func (s *deadlineFaultConn) SetDeadline(d time.Time) error {
	if s.reject("all") {
		return errFixtureDeadline
	}
	return s.Conn.SetDeadline(d)
}
func (s *deadlineFaultConn) SetReadDeadline(d time.Time) error {
	if s.reject("read") {
		return errFixtureDeadline
	}
	return s.Conn.SetReadDeadline(d)
}
func (s *deadlineFaultConn) SetWriteDeadline(d time.Time) error {
	if s.reject("write") {
		return errFixtureDeadline
	}
	return s.Conn.SetWriteDeadline(d)
}
func (s *deadlineFaultConn) Read(p []byte) (int, error) {
	s.mu.Lock()
	s.reads++
	s.mu.Unlock()
	return s.Conn.Read(p)
}
func (s *deadlineFaultConn) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.writes++
	s.mu.Unlock()
	return s.Conn.Write(p)
}
func (s *deadlineFaultConn) Close() error {
	s.mu.Lock()
	s.closes++
	s.mu.Unlock()
	return s.Conn.Close()
}
func (s *deadlineFaultConn) counts() (int, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads, s.writes, s.closes
}

func deadlineFixture(t *testing.T, c *NatiuClient, receive ...[]byte) (*deadlineFaultConn, chan wirePacket) {
	t.Helper()
	a, b := net.Pipe()
	raw := &deadlineFaultConn{Conn: a}
	c.dial = func(string, string) (net.Conn, error) { return raw, nil }
	seen := make(chan wirePacket, 32)
	done := make(chan struct{})
	t.Cleanup(func() { finishNatiuFixture(t, c, a, b, done) })
	go func() {
		defer close(done)
		defer closeNatiuFixture(t, b)
		r := bufio.NewReader(b)
		for {
			p, err := readPacket(r)
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
					t.Errorf("read deadline fixture: %v", err)
				}
				return
			}
			seen <- p
			switch p.header >> 4 {
			case 1:
				if !writeNatiuFixture(t, b, []byte{0x20, 2, 0, 0}) {
					return
				}
				for _, fragment := range receive {
					if !writeNatiuFixture(t, b, fragment) {
						return
					}
				}
			case 8:
				if len(p.data) < 2 {
					t.Error("short SUBSCRIBE")
					return
				}
				if !writeNatiuFixture(t, b, []byte{0x90, 3, p.data[0], p.data[1], 0}) {
					return
				}
			case 14:
				return
			}
		}
	}()
	return raw, seen
}
func newDeadlineClient() *NatiuClient {
	return NewNatiuClient(config.DefaultConfig().MQTT, logger.NewSerial(io.Discard, logger.WARN))
}

func expectDeadlinePublication(t *testing.T, seen <-chan wirePacket, payload string) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case packet := <-seen:
			if packet.header>>4 != 3 {
				continue
			}
			if packet.header != 0x30 || string(packet.data) != "\x00\x05state"+payload {
				t.Fatalf("unexpected resumed publication: %#v", packet)
			}
			return
		case <-timer.C:
			t.Fatal("broker did not decode resumed publication")
		}
	}
}

func TestNatiuDeadlineConnectSetAndClearFailureRetiresAndReconnects(t *testing.T) {
	for _, at := range []int{1, 2} {
		t.Run(map[int]string{1: "set", 2: "clear"}[at], func(t *testing.T) {
			c := newDeadlineClient()
			raw, _ := deadlineFixture(t, c)
			raw.arm("all", at)
			err := c.Connect()
			if !errors.Is(err, errFixtureDeadline) {
				t.Fatalf("CONNECT deadline failure lost: %v", err)
			}
			_, writes, closes := raw.counts()
			if closes == 0 || (at == 1 && writes != 0) {
				t.Fatalf("failed handshake writes=%d closes=%d", writes, closes)
			}
			client, wire := c.snapshot()
			if c.IsConnected() || client != nil || wire != nil || c.lifecycle.active != nil {
				t.Fatal("failed generation remained published")
			}
			if c.lifecycle.starts != 3 {
				t.Fatal("unexpected worker count")
			}
			first := c.lifecycle.rxStart
			_, seen := deadlineFixture(t, c)
			if err := c.Connect(); err != nil {
				t.Fatal(err)
			}
			if !c.IsConnected() || c.lifecycle.generation != 2 || c.lifecycle.starts != 3 || first != c.lifecycle.rxStart {
				t.Fatal("same-boot recovery replaced workers or failed")
			}
			if err := c.Publish("state", []byte("ready"), 0, false, 0); err != nil {
				t.Fatal(err)
			}
			expectDeadlinePublication(t, seen, "ready")
		})
	}
}

func TestNatiuDeadlineProgressFailureStopsRXAndReconnects(t *testing.T) {
	c := newDeadlineClient()
	raw, _ := deadlineFixture(t, c, []byte{0xd0})
	raw.arm("read", 2) // Idle setup succeeds; first packet byte arrives, then progress fails.
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	g := c.lifecycle.active
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		t.Fatal("progress deadline failure did not retire RX")
	}
	reads, _, closes := raw.counts()
	raw.mu.Lock()
	before := raw.readsAtFailure
	raw.mu.Unlock()
	if reads != before || closes == 0 || c.IsConnected() || g.wire.packetBytes != 1 || !errors.Is(g.client.Err(), errFixtureDeadline) {
		t.Fatalf("partial packet cleanup: reads=%d before=%d closes=%d bytes=%d err=%v", reads, before, closes, g.wire.packetBytes, g.client.Err())
	}
	_, seen := deadlineFixture(t, c)
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	if err := c.Publish("state", []byte("resumed"), 0, false, 0); err != nil {
		t.Fatal(err)
	}
	expectDeadlinePublication(t, seen, "resumed")
}

func TestNatiuDeadlineIdleFailureStopsRXWithoutRead(t *testing.T) {
	c := newDeadlineClient()
	raw, _ := deadlineFixture(t, c)
	raw.arm("read", 1)
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		t.Fatal("idle deadline failure did not retire RX")
	}
	reads, _, closes := raw.counts()
	raw.mu.Lock()
	before := raw.readsAtFailure
	raw.mu.Unlock()
	// Compare with the actual handshake's reads, not an assumed decoder shape.
	if reads != before || before == 0 || closes == 0 || c.IsConnected() {
		t.Fatalf("idle failure reads=%d closes=%d connected=%v", reads, closes, c.IsConnected())
	}
	_, seen := deadlineFixture(t, c)
	if err := c.ConnectContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Publish("state", []byte("resumed"), 0, false, 0); err != nil {
		t.Fatal(err)
	}
	expectDeadlinePublication(t, seen, "resumed")
}

func TestNatiuDeadlineSubscriptionFailureDoesNotSendOrLoseHandler(t *testing.T) {
	for _, operation := range []string{"subscribe-new", "subscribe-existing", "unsubscribe"} {
		t.Run(operation, func(t *testing.T) {
			c := newDeadlineClient()
			raw, seen := deadlineFixture(t, c)
			if err := c.Connect(); err != nil {
				t.Fatal(err)
			}
			calls := 0
			previous := func(string, []byte) { calls++ }
			if operation != "subscribe-new" {
				if err := c.Subscribe("command", previous); err != nil {
					t.Fatal(err)
				}
			}
			_, before, _ := raw.counts()
			raw.arm("write", 1)
			var err error
			if operation == "unsubscribe" {
				err = c.Unsubscribe("command")
			} else {
				err = c.Subscribe("command", func(string, []byte) { calls += 100 })
			}
			if !errors.Is(err, errFixtureDeadline) {
				t.Fatalf("subscription deadline error lost: %v", err)
			}
			_, after, _ := raw.counts()
			if before != after {
				t.Fatalf("I/O after rejected deadline: writes %d -> %d", before, after)
			}
			c.mu.Lock()
			handler := c.handlers["command"]
			c.mu.Unlock()
			if operation == "subscribe-new" {
				if handler != nil {
					t.Fatal("failed new subscription retained handler")
				}
			} else {
				if handler == nil {
					t.Fatal("existing handler lost")
				}
				handler("command", nil)
				if calls != 1 {
					t.Fatal("existing handler replaced")
				}
			}
			raw.arm("", 0)
			if err := c.Subscribe("command", previous); err != nil {
				t.Fatal(err)
			}
			if err := c.Unsubscribe("command"); err != nil {
				t.Fatal(err)
			}
			if err := c.Publish("state", []byte("ready"), 0, false, 0); err != nil {
				t.Fatal(err)
			}
			expectDeadlinePublication(t, seen, "ready")
		})
	}
}
