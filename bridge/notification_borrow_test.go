//go:build jsonborrowtest && natiu && !tinygo

package bridge

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"moonraker2mqtt/mqtt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

var borrowWriteErr = errors.New("synthetic partial write")

type borrowConn struct {
	net.Conn
	mu      sync.Mutex
	wire    bytes.Buffer
	calls   int
	mode    string
	entered chan struct{}
	resume  chan struct{}
}

func (c *borrowConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	mode := c.mode
	c.mu.Unlock()
	if call == 3 && c.entered != nil {
		saved := append([]byte(nil), p...)
		close(c.entered)
		<-c.resume
		if !bytes.Equal(p, saved) {
			panic("borrow bytes mutated while Publish blocked")
		}
		if _, e := json.Marshal(map[string]any{"nested": "<&"}); e != nil {
			panic(e)
		}
		switch mode {
		case "partial":
			c.mu.Lock()
			c.wire.Write(p[:len(p)/2])
			c.mu.Unlock()
			return len(p) / 2, borrowWriteErr
		case "short":
			return 0, nil
		case "disconnect":
			c.Conn.Close()
			return 0, net.ErrClosed
		case "panic":
			panic(borrowWriteErr)
		}
	}
	c.mu.Lock()
	c.wire.Write(p)
	c.mu.Unlock()
	return c.Conn.Write(p)
}
func (c *borrowConn) SetWriteDeadline(d time.Time) error {
	c.mu.Lock()
	mode := c.mode
	c.mu.Unlock()
	if mode == "deadline" {
		return borrowWriteErr
	}
	return c.Conn.SetWriteDeadline(d)
}
func readBorrowPacket(r *bufio.Reader) (byte, []byte, error) {
	h, e := r.ReadByte()
	if e != nil {
		return 0, nil, e
	}
	n, m := 0, 1
	for {
		v, e := r.ReadByte()
		if e != nil {
			return 0, nil, e
		}
		n += int(v&127) * m
		if v&128 == 0 {
			break
		}
		m *= 128
	}
	p := make([]byte, n)
	_, e = io.ReadFull(r, p)
	return h, p, e
}
func TestBorrowActualNotificationMQTT(t *testing.T) {
	for _, mode := range []string{"success", "maximum", "partial", "short", "disconnect", "panic", "deadline", "encode-error", "oversize", "qos", "long-topic"} {
		t.Run(mode, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.MQTT.Retain = true
			var log bytes.Buffer
			lg := logger.NewSerial(&log, logger.INFO)
			a, b := net.Pipe()
			conn := &borrowConn{Conn: a}
			serverDone := make(chan struct{})
			go func() {
				defer close(serverDone)
				defer b.Close()
				r := bufio.NewReader(b)
				_, _, e := readBorrowPacket(r)
				if e != nil {
					return
				}
				if _, e = b.Write([]byte{0x20, 2, 0, 0}); e != nil {
					return
				}
				io.Copy(io.Discard, r)
			}()
			c := mqtt.NotificationFixtureClient(cfg.MQTT, lg, conn)
			if e := c.Connect(); e != nil {
				t.Fatal(e)
			}
			defer func() { conn.Conn.Close(); b.Close(); c.Disconnect(); <-serverDone }()
			conn.mu.Lock()
			conn.calls = 0
			conn.wire.Reset()
			conn.mode = mode
			conn.mu.Unlock()
			app := New(cfg, c, lg)
			if !notificationBorrowQualified(c) {
				t.Fatal("actual adapter not qualified")
			}
			params := any(strings.Repeat("x", 1771))
			method := "notify_status_update"
			switch mode {
			case "maximum":
				params = strings.Repeat("x", 4094)
			case "encode-error":
				params = make(chan int)
			case "oversize":
				params = strings.Repeat("x", 4095)
			case "qos":
				cfg.MQTT.QoS = 1
			case "long-topic":
				method = strings.Repeat("m", 256)
			}
			blocked := mode == "success" || mode == "maximum" || mode == "partial" || mode == "short" || mode == "disconnect" || mode == "panic"
			done := make(chan any, 1)
			beforeClone := bytes.BorrowTestCloneCalls
			if blocked {
				conn.entered = make(chan struct{})
				conn.resume = make(chan struct{})
			}
			go func() {
				var pan any
				defer func() {
					if p := recover(); p != nil {
						pan = p
					}
					done <- pan
				}()
				app.OnNotification(method, params)
			}()
			if blocked {
				select {
				case <-conn.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("Publish boundary not reached")
				}
				if json.BorrowTestActive() != 1 {
					t.Fatalf("lease not held during Publish: %d", json.BorrowTestActive())
				}
				for i := 0; i < 80; i++ {
					var other json.MarshalLease
					if e := other.Encode(strings.Repeat("z", 4094)); e != nil {
						t.Fatal(e)
					}
					other.Release()
				}
				select {
				case <-done:
					t.Fatal("callback returned before write")
				default:
				}
				close(conn.resume)
			}
			var pan any
			select {
			case pan = <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("callback stuck")
			}
			if mode == "panic" {
				if pan != borrowWriteErr {
					t.Fatalf("panic %v", pan)
				}
			} else if pan != nil {
				t.Fatal(pan)
			}
			if json.BorrowTestActive() != 0 {
				t.Fatalf("lease leaked on %s: %d", mode, json.BorrowTestActive())
			}
			// Only the deliberate nested json.Marshal at the blocked writer may clone.
			delta := bytes.BorrowTestCloneCalls - beforeClone
			if blocked {
				if delta != 1 {
					t.Fatalf("unexpected final clone count %d", delta)
				}
			} else if delta != 0 {
				t.Fatalf("notification final clone executed: %d", delta)
			}
			conn.mu.Lock()
			wire := append([]byte(nil), conn.wire.Bytes()...)
			conn.mu.Unlock()
			if mode == "success" || mode == "maximum" {
				h, p, e := readBorrowPacket(bufio.NewReader(bytes.NewReader(wire)))
				if e != nil {
					t.Fatal(e)
				}
				n := int(binary.BigEndian.Uint16(p))
				want, _ := json.Marshal(params)
				if h != 0x31 || string(p[2:2+n]) != "moonraker/notifications/notify_status_update" || !bytes.Equal(p[2+n:], want) {
					t.Fatal("actual wire differs")
				}
			}
			if mode == "encode-error" {
				if len(wire) != 0 || !strings.Contains(log.String(), "Failed to marshal notification params") || strings.Contains(log.String(), "Failed to publish notification") {
					t.Fatal("encode error path changed")
				}
			}
			if mode != "success" && mode != "maximum" && mode != "panic" && mode != "encode-error" {
				if !strings.Contains(log.String(), "Failed to publish notification") {
					t.Fatal("publish error not distinct")
				}
			}
		})
	}
}

type retainingMQTT struct {
	recordingMQTT
	saved []byte
}

func (c *retainingMQTT) Publish(topic string, p []byte, q byte, r bool, retries int) error {
	c.saved = p
	return c.recordingMQTT.Publish(topic, p, q, r, retries)
}
func TestBorrowUnqualifiedOwnedFallback(t *testing.T) {
	cfg := config.DefaultConfig()
	c := &retainingMQTT{}
	c.connected = true
	app := New(cfg, c, logger.NewSerial(io.Discard, logger.INFO))
	if notificationBorrowQualified(c) {
		t.Fatal("unknown client qualified")
	}
	app.OnNotification("one", "first")
	saved := c.saved
	for i := 0; i < 100; i++ {
		var l json.MarshalLease
		_ = l.Encode(strings.Repeat("x", 4094))
		l.Release()
	}
	if string(saved) != `"first"` {
		t.Fatal("owned fallback invalidated")
	}
	if len(c.messages) != 1 || c.messages[0].retries != 3 {
		t.Fatal("publish arguments changed")
	}
}
