//go:build natiu && !tinygo

package mqtt

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	native "github.com/soypat/natiu-mqtt"
	"moonraker2mqtt/logger"
)

type publishCloseFixture struct {
	*streamTestConn
	closeError error
	closes     int
}

func (s *publishCloseFixture) Close() error {
	s.closes++
	s.closed = true
	return s.closeError
}

func TestMQTTPublishCleanupPreservesBothErrors(t *testing.T) {
	primary := errors.New("primary publication failure")
	secondary := errors.New("secondary publication close failure")
	for _, phase := range []string{"deadline", "write", "short", "partial"} {
		for _, closeFails := range []bool{false, true} {
			name := phase + "/healthy-close"
			if closeFails {
				name = phase + "/failed-close"
			}
			t.Run(name, func(t *testing.T) {
				underlying := &streamTestConn{}
				want := primary
				switch phase {
				case "deadline":
					underlying.deadlineError = primary
				case "write":
					underlying.failAt = 1
					underlying.failure = primary
				case "short":
					underlying.failAt = 1
					underlying.short = true
					want = io.ErrShortWrite
				case "partial":
					underlying.failAt = 3
					underlying.failure = primary
					underlying.partial = true
				}
				raw := &publishCloseFixture{streamTestConn: underlying}
				if closeFails {
					raw.closeError = secondary
				}
				wire := &packetConn{Conn: raw}
				err := wire.writePublish([]byte("topic"), []byte("payload"), false)
				if !errors.Is(err, want) || errors.Is(err, secondary) != closeFails {
					t.Fatalf("publication error=%v, want primary=%v close failure=%v", err, want, closeFails)
				}
				if !closeFails && err != want {
					t.Fatal("healthy cleanup changed primary error identity")
				}
				if raw.closes != 1 || !raw.closed || !wire.closed {
					t.Fatalf("closes=%d raw=%v wire=%v", raw.closes, raw.closed, wire.closed)
				}
				if phase == "deadline" && raw.calls != 0 {
					t.Fatal("write after deadline failure")
				}
				if phase == "partial" && (raw.calls != 3 || raw.output.Len() != 12) {
					t.Fatal("partial publication progress changed")
				}
			})
		}
	}
}

func TestMQTTPublishHealthyDoesNotClose(t *testing.T) {
	raw := &publishCloseFixture{streamTestConn: &streamTestConn{}, closeError: errors.New("must not close")}
	wire := &packetConn{Conn: raw}
	if err := wire.writePublish([]byte("topic"), []byte("payload"), false); err != nil {
		t.Fatal(err)
	}
	if raw.closes != 0 || raw.closed || wire.closed {
		t.Fatal("healthy publish closed transport")
	}
}

// The real native client consumes a valid CONNACK before readLoop is exercised.
// All operations are synchronous here; no worker or unjoined peer is invented.
type rxCleanupFixture struct {
	net.Conn
	input                                          bytes.Reader
	readDeadlineError, closeError, disconnectError error
	closes, disconnects                            int
	order                                          []string
}

func (s *rxCleanupFixture) Read(p []byte) (int, error) { return s.input.Read(p) }
func (s *rxCleanupFixture) Write(p []byte) (int, error) {
	if len(p) == 2 && p[0] == 0xe0 {
		s.disconnects++
		s.order = append(s.order, "disconnect")
		if s.disconnectError != nil {
			return 0, s.disconnectError
		}
	}
	return len(p), nil
}
func (s *rxCleanupFixture) SetReadDeadline(time.Time) error  { return s.readDeadlineError }
func (s *rxCleanupFixture) SetWriteDeadline(time.Time) error { return nil }
func (s *rxCleanupFixture) SetDeadline(time.Time) error      { return nil }
func (s *rxCleanupFixture) Close() error {
	s.closes++
	s.order = append(s.order, "close")
	return s.closeError
}
func connectedCleanupFixture(t *testing.T, raw *rxCleanupFixture) (*native.Client, *packetConn) {
	t.Helper()
	raw.input.Reset([]byte{0x20, 2, 0, 0})
	wire := &packetConn{Conn: raw}
	client := native.NewClient(native.ClientConfig{})
	var vc native.VariablesConnect
	vc.SetDefaultMQTT([]byte("cleanup-fixture"))
	if err := client.Connect(context.Background(), wire, &vc); err != nil {
		t.Fatal(err)
	}
	return client, wire
}

func TestMQTTRXCleanupLogsCloseAndDisconnectErrors(t *testing.T) {
	for _, closeFails := range []bool{false, true} {
		for _, disconnectFails := range []bool{false, true} {
			name := "healthy-close"
			if closeFails {
				name = "failed-close"
			}
			if disconnectFails {
				name += "/failed-disconnect"
			} else {
				name += "/healthy-disconnect"
			}
			t.Run(name, func(t *testing.T) {
				raw := &rxCleanupFixture{}
				client, wire := connectedCleanupFixture(t, raw)
				raw.readDeadlineError = errors.New("receive primary failure")
				if closeFails {
					raw.closeError = errors.New("receive close secondary failure")
				}
				if disconnectFails {
					raw.disconnectError = errors.New("receive disconnect secondary failure")
				}
				var logs bytes.Buffer
				c := newDeadlineClient()
				c.log = logger.NewSerial(&logs, logger.WARN)
				done := make(chan struct{})
				c.readLoop(client, wire, nil, done)
				select {
				case <-done:
				default:
					t.Fatal("RX completion not closed")
				}
				if client.IsConnected() || client.Err() == nil || client.Err().Error() != "MQTT transport stopped" {
					t.Fatal("cleanup replaced native stop reason")
				}
				// Native Disconnect also closes its RX and TX transports. Retain
				// those two real calls between our explicit and deferred Close.
				if raw.closes != 4 || raw.disconnects != 1 || strings.Join(raw.order, ",") != "close,disconnect,close,close,close" {
					t.Fatalf("cleanup order changed: %v", raw.order)
				}
				if !strings.Contains(logs.String(), "receive primary failure") {
					t.Fatal("primary receive error lost")
				}
				wantClose := 0
				if closeFails {
					wantClose = 2
				}
				wantDisconnect := 0
				if disconnectFails {
					wantDisconnect = 1
				}
				if strings.Count(logs.String(), "receive close secondary failure") != wantClose || strings.Count(logs.String(), "receive disconnect secondary failure") != wantDisconnect {
					t.Fatalf("secondary cleanup errors not observed: %s", logs.String())
				}
			})
		}
	}
}

func TestMQTTRXDeferredCloseErrorAndGracefulSkip(t *testing.T) {
	t.Run("decoder-already-disconnected", func(t *testing.T) {
		raw := &rxCleanupFixture{closeError: errors.New("deferred receive close failure")}
		client, wire := connectedCleanupFixture(t, raw)
		var logs bytes.Buffer
		c := newDeadlineClient()
		c.log = logger.NewSerial(&logs, logger.WARN)
		done := make(chan struct{})
		c.readLoop(client, wire, nil, done)
		if !errors.Is(client.Err(), io.EOF) || raw.closes != 1 || raw.disconnects != 1 {
			t.Fatalf("decoder cleanup changed: %v %v", client.Err(), raw.order)
		}
		if strings.Count(logs.String(), "deferred receive close failure") != 1 {
			t.Fatalf("deferred Close error lost: %s", logs.String())
		}
	})
	t.Run("graceful-shutdown", func(t *testing.T) {
		raw := &rxCleanupFixture{closeError: errors.New("must not close during graceful write")}
		client, wire := connectedCleanupFixture(t, raw)
		if err := client.Disconnect(errShutdown); err != nil {
			t.Fatal(err)
		}
		before := raw.closes
		var logs bytes.Buffer
		c := newDeadlineClient()
		c.log = logger.NewSerial(&logs, logger.WARN)
		done := make(chan struct{})
		c.readLoop(client, wire, nil, done)
		if !errors.Is(client.Err(), errShutdown) || before != 2 || raw.closes != before || raw.disconnects != 1 || logs.Len() != 0 {
			t.Fatalf("graceful owner/order changed: %v %s", raw.order, logs.String())
		}
	})
}

type reportingCloseConn struct {
	net.Conn
	failure error
}

func (s *reportingCloseConn) Close() error {
	if err := s.Conn.Close(); err != nil {
		return err
	}
	return s.failure
}

func TestMQTTPublishCleanupPublicFailureRetiresAndRecovers(t *testing.T) {
	c := newDeadlineClient()
	raw, _ := deadlineFixture(t, c)
	secondary := errors.New("public publication close failure")
	c.dial = func(string, string) (net.Conn, error) { return &reportingCloseConn{Conn: raw, failure: secondary}, nil }
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	raw.arm("write", 1)
	err := c.Publish("state", []byte("ready"), 0, false, 0)
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		t.Fatal("failed publication RX did not join")
	}
	if !errors.Is(err, errFixtureDeadline) || !errors.Is(err, secondary) {
		t.Fatalf("public primary/cleanup failure lost: %v", err)
	}
	if c.IsConnected() {
		t.Fatal("failed publication stayed connected")
	}
	first := c.lifecycle.rxStart
	_, seen := deadlineFixture(t, c)
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	if first != c.lifecycle.rxStart || c.lifecycle.starts != 3 || c.lifecycle.generation != 2 {
		t.Fatal("recovery replaced worker storage")
	}
	if err := c.Publish("state", []byte("resumed"), 0, false, 0); err != nil {
		t.Fatal(err)
	}
	expectDeadlinePublication(t, seen, "resumed")
}
