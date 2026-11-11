//go:build jsonborrowtest && natiu && !tinygo

package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"moonraker2mqtt/mqtt"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type statusFixtureMoon struct {
	sessionMoonraker
	result             map[string]any
	queryErr, stateErr error
}

func (m *statusFixtureMoon) GetKlippyState(context.Context) (string, error) {
	return "ready", m.stateErr
}
func (m *statusFixtureMoon) QueryObjects(context.Context, map[string]any) (map[string]any, error) {
	return m.result, m.queryErr
}

type statusFixtureConn struct {
	net.Conn
	mu              sync.Mutex
	writes          int64
	armed, blocked  bool
	mode            string
	entered, resume chan struct{}
}

func (c *statusFixtureConn) Write(p []byte) (int, error) {
	atomic.AddInt64(&c.writes, 1)
	c.mu.Lock()
	block := c.armed && !c.blocked && len(p) > 0 && (p[0] == '"' || p[0] == '{' || p[0] == '[' || p[0] == 'n')
	if block {
		c.blocked = true
	}
	mode := c.mode
	c.mu.Unlock()
	if block {
		saved := append([]byte(nil), p...)
		close(c.entered)
		<-c.resume
		if !bytes.Equal(saved, p) {
			panic("status payload mutated before Publish returned")
		}
		// A nested owned marshal is deliberately counted separately.
		_, _ = json.Marshal(map[string]any{"nested": "<&"})
		switch mode {
		case "panic":
			panic(borrowWriteErr)
		case "partial":
			return len(p) / 2, borrowWriteErr
		case "short":
			return 0, nil
		case "disconnect":
			c.Conn.Close()
			return 0, net.ErrClosed
		}
	}
	return c.Conn.Write(p)
}

type statusPacket struct {
	h byte
	p []byte
}

func statusReal(t *testing.T) (*App, *statusFixtureMoon, *statusFixtureConn, <-chan statusPacket) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.MQTT.Retain = true
	lg := logger.NewSerial(io.Discard, logger.ERROR)
	a, b := net.Pipe()
	conn := &statusFixtureConn{Conn: a, entered: make(chan struct{}), resume: make(chan struct{})}
	packets := make(chan statusPacket, 128)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(packets)
		defer b.Close()
		r := bufio.NewReader(b)
		if _, _, e := readBorrowPacket(r); e != nil {
			return
		}
		if _, e := b.Write([]byte{0x20, 2, 0, 0}); e != nil {
			return
		}
		for {
			h, p, e := readBorrowPacket(r)
			if e != nil {
				return
			}
			packets <- statusPacket{h, p}
		}
	}()
	c := mqtt.NotificationFixtureClient(cfg.MQTT, lg, conn)
	if e := c.Connect(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.Close(); b.Close(); c.Disconnect(); <-done })
	atomic.StoreInt64(&conn.writes, 0)
	app := New(cfg, c, lg)
	m := &statusFixtureMoon{}
	app.moonrakerClient = m
	return app, m, conn, packets
}
func statusWait(t *testing.T, c <-chan struct{}) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(3 * time.Second):
		t.Fatal("boundary not reached")
	}
}
func statusCheckPacket(t *testing.T, p statusPacket, topic string, v any) {
	t.Helper()
	if len(p.p) < 2 {
		t.Fatal("short MQTT packet")
	}
	n := int(binary.BigEndian.Uint16(p.p))
	if n+2 > len(p.p) {
		t.Fatal("short topic")
	}
	want, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	if p.h != 0x30 || string(p.p[2:2+n]) != topic || !bytes.Equal(p.p[2+n:], want) || bytes.HasSuffix(p.p[2+n:], []byte{'\n'}) {
		t.Fatalf("V1 byte/topic/retain parity mismatch %q", topic)
	}
}
func TestStatusBorrowProducer2RED(t *testing.T) {
	app, m, _, packets := statusReal(t)
	m.result = map[string]any{"status": map[string]any{"state": "printing"}, "eventtime": 1.5}
	before := atomic.LoadUint64(&bytes.BorrowTestCloneCalls)
	if e := app.publishStatus(context.Background()); e != nil {
		t.Fatal(e)
	}
	delta := atomic.LoadUint64(&bytes.BorrowTestCloneCalls) - before
	<-packets
	statusCheckPacket(t, <-packets, "moonraker/objects/status", m.result["status"])
	if delta != 0 {
		t.Fatalf("CONTROL_REACHED: producer2 final owned clone remains: %d", delta)
	}
}
func TestStatusBorrowActualBoundary(t *testing.T) {
	for _, mode := range []string{"success", "partial", "short", "disconnect", "panic"} {
		t.Run(mode, func(t *testing.T) {
			app, m, conn, packets := statusReal(t)
			m.result = map[string]any{"status": strings.Repeat("x", 1771), "eventtime": 4.5}
			conn.mode = mode
			conn.armed = true
			done := make(chan struct{})
			var err error
			var pan any
			before := atomic.LoadUint64(&bytes.BorrowTestCloneCalls)
			go func() {
				defer close(done)
				defer func() { pan = recover() }()
				err = app.publishStatus(context.Background())
			}()
			statusWait(t, conn.entered)
			if json.BorrowTestActive() != 1 {
				close(conn.resume)
				statusWait(t, done)
				t.Fatal("status lease absent while adapter holds payload")
			}
			for i := 0; i < 100; i++ {
				var l json.MarshalLease
				_ = l.Encode(strings.Repeat("z", 4094))
				l.Release()
			}
			close(conn.resume)
			statusWait(t, done)
			if json.BorrowTestActive() != 0 {
				t.Fatal("status lease leaked")
			}
			if mode == "panic" {
				if pan != borrowWriteErr {
					t.Fatal("wrong panic")
				}
			} else if pan != nil {
				t.Fatal(pan)
			} else if (err == nil) != (mode == "success") {
				t.Fatalf("error semantics %v", err)
			}
			if delta := atomic.LoadUint64(&bytes.BorrowTestCloneCalls) - before; delta != 1 {
				t.Fatalf("producer2 copy not removed, delta=%d (nested=1)", delta)
			}
			if mode == "success" {
				<-packets
				statusCheckPacket(t, <-packets, "moonraker/objects/status", m.result["status"])
			}
		})
	}
}

type statusHook struct {
	panicHook bool
	seen      *int64
}

func (h statusHook) MarshalJSON() ([]byte, error) {
	*h.seen = json.BorrowTestActive()
	var nested json.MarshalLease
	defer nested.Release()
	if e := nested.Encode(map[string]any{"nested": "<&"}); e != nil {
		return nil, e
	}
	if json.BorrowTestActive() != 2 {
		panic("nested lease missing")
	}
	if h.panicHook {
		panic(borrowWriteErr)
	}
	return []byte(`{"x":"<&"}`), nil
}
func TestStatusBorrowHooksAndErrors(t *testing.T) {
	for _, mode := range []string{"encode-error", "hook", "hook-panic", "query-error", "state-error", "nil-result", "empty-result"} {
		t.Run(mode, func(t *testing.T) {
			app, m, conn, packets := statusReal(t)
			var seen int64
			m.result = map[string]any{"status": make(chan int)}
			switch mode {
			case "hook", "hook-panic":
				m.result["status"] = statusHook{mode == "hook-panic", &seen}
			case "query-error":
				m.queryErr = borrowWriteErr
			case "state-error":
				m.stateErr = borrowWriteErr
			case "nil-result":
				m.result = nil
			case "empty-result":
				m.result = map[string]any{}
			}
			var err error
			var pan any
			func() { defer func() { pan = recover() }(); err = app.publishStatus(context.Background()) }()
			if json.BorrowTestActive() != 0 {
				t.Fatal("lease not released on encoding exit")
			}
			if mode == "hook" || mode == "hook-panic" {
				if seen != 1 {
					t.Fatal("hook invoked before lease checked out")
				}
			}
			if mode == "hook-panic" {
				if pan != borrowWriteErr {
					t.Fatal("panic changed")
				}
			} else if pan != nil {
				t.Fatal(pan)
			}
			wantErr := mode == "encode-error" || mode == "query-error" || mode == "state-error"
			if (err != nil) != wantErr {
				t.Fatalf("error semantics %v", err)
			}
			want := 1
			if mode == "state-error" {
				want = 0
			}
			if mode == "hook" {
				want = 2
			}
			for i := 0; i < want; i++ {
				select {
				case <-packets:
				case <-time.After(time.Second):
					t.Fatal("expected broker packet missing")
				}
			}
			if n := atomic.LoadInt64(&conn.writes); n != int64(3*want) {
				t.Fatalf("Write calls=%d want=%d (zero object Publish on encoding failure)", n, 3*want)
			}
		})
	}
}
func TestStatusBorrowMultiObjectParity(t *testing.T) {
	app, m, _, packets := statusReal(t)
	m.result = map[string]any{"a": nil, "b": []any{nil, true, 1.25, "<&\n"}, "c": map[string]any{"key": "value"}, "eventtime": 3.5}
	if e := app.publishStatus(context.Background()); e != nil {
		t.Fatal(e)
	}
	<-packets
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		p := <-packets
		n := int(binary.BigEndian.Uint16(p.p))
		topic := string(p.p[2 : 2+n])
		name := strings.TrimPrefix(topic, "moonraker/objects/")
		if seen[name] {
			t.Fatal("duplicate object")
		}
		seen[name] = true
		statusCheckPacket(t, p, topic, m.result[name])
	}
	if len(seen) != 3 || json.BorrowTestActive() != 0 {
		t.Fatal("objects or release")
	}
}
func TestStatusBorrowTwoPublishers(t *testing.T) {
	app, m, conn, packets := statusReal(t)
	m.result = map[string]any{"status": strings.Repeat("s", 1771)}
	conn.armed = true
	done := make(chan struct{}, 2)
	go func() {
		if e := app.publishStatus(context.Background()); e != nil {
			panic(e)
		}
		done <- struct{}{}
	}()
	statusWait(t, conn.entered)
	go func() { app.OnNotification("second", strings.Repeat("n", 4094)); done <- struct{}{} }()
	until := time.Now().Add(2 * time.Second)
	for json.BorrowTestActive() != 2 && time.Now().Before(until) {
		runtime.Gosched()
	}
	if json.BorrowTestActive() != 2 {
		close(conn.resume)
		t.Fatal("second lease not held waiting for actual publication mutex")
	}
	for i := 0; i < 100; i++ {
		var l json.MarshalLease
		_ = l.Encode(strings.Repeat("c", 4094))
		l.Release()
	}
	close(conn.resume)
	statusWait(t, done)
	statusWait(t, done)
	if json.BorrowTestActive() != 0 {
		t.Fatal("concurrent lease leak")
	}
	<-packets
	statusCheckPacket(t, <-packets, "moonraker/objects/status", m.result["status"])
	p := <-packets
	n := int(binary.BigEndian.Uint16(p.p))
	want, _ := json.Marshal(strings.Repeat("n", 4094))
	if string(p.p[2:2+n]) != "moonraker/notifications/second" || !bytes.Equal(p.p[2+n:], want) {
		t.Fatal("notification corrupt")
	}
}

type statusRetainer struct {
	mqtt.MQTTClient
	saved map[string][]byte
}

func (c *statusRetainer) Publish(topic string, p []byte, q byte, r bool, n int) error {
	c.saved[topic] = p
	return c.MQTTClient.Publish(topic, p, q, r, n)
}
func TestStatusBorrowWrappedOwnedFallback(t *testing.T) {
	app, m, _, packets := statusReal(t)
	wrapped := &statusRetainer{app.mqttClient, map[string][]byte{}}
	app.mqttClient = wrapped
	if notificationBorrowQualified(wrapped) {
		t.Fatal("wrapper incorrectly qualified")
	}
	m.result = map[string]any{"a": "first", "b": "second"}
	before := atomic.LoadUint64(&bytes.BorrowTestCloneCalls)
	if e := app.publishStatus(context.Background()); e != nil {
		t.Fatal(e)
	}
	if atomic.LoadUint64(&bytes.BorrowTestCloneCalls)-before != 2 {
		t.Fatal("wrapped producer2 not owned")
	}
	for i := 0; i < 100; i++ {
		var l json.MarshalLease
		_ = l.Encode(strings.Repeat("x", 4094))
		l.Release()
	}
	if string(wrapped.saved["moonraker/objects/a"]) != `"first"` || string(wrapped.saved["moonraker/objects/b"]) != `"second"` {
		t.Fatal("retained payload aliased encoder or another object")
	}
	for i := 0; i < 3; i++ {
		<-packets
	}
}
func TestStatusBorrowUnqualifiedErrorsPolicy(t *testing.T) {
	cfg := config.DefaultConfig()
	q := &retainingMQTT{}
	q.connected = true
	app := New(cfg, q, logger.NewSerial(io.Discard, logger.ERROR))
	m := &statusFixtureMoon{result: map[string]any{"a": "ok", "b": make(chan int), "c": "ok", "eventtime": 1.0}}
	app.moonrakerClient = m
	if e := app.publishStatus(context.Background()); e != nil {
		t.Fatal("one of three errors should keep baseline threshold policy", e)
	}
	if string(q.saved) != `"ok"` {
		t.Fatal("encoding failure published or owned bytes changed")
	}
	m.queryErr = errors.New("query")
	if e := app.publishStatus(context.Background()); e == nil || !strings.Contains(e.Error(), "failed to query objects") {
		t.Fatal(e)
	}
}
