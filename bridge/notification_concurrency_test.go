//go:build jsonborrowtest && natiu && !tinygo

package bridge

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"moonraker2mqtt/mqtt"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBorrowTwoActualPublishers(t *testing.T) {
	cfg := config.DefaultConfig()
	lg := logger.NewSerial(io.Discard, logger.INFO)
	a, b := net.Pipe()
	conn := &borrowConn{Conn: a}
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		defer b.Close()
		r := bufio.NewReader(b)
		if _, _, e := readBorrowPacket(r); e != nil {
			return
		}
		b.Write([]byte{0x20, 2, 0, 0})
		io.Copy(io.Discard, r)
	}()
	c := mqtt.NotificationFixtureClient(cfg.MQTT, lg, conn)
	if e := c.Connect(); e != nil {
		t.Fatal(e)
	}
	defer func() { a.Close(); b.Close(); c.Disconnect(); <-serverDone }()
	conn.mu.Lock()
	conn.calls = 0
	conn.wire.Reset()
	conn.entered = make(chan struct{})
	conn.resume = make(chan struct{})
	conn.mu.Unlock()
	app := New(cfg, c, lg)
	first := strings.Repeat("a", 1771)
	second := strings.Repeat("b", 4094)
	done := make(chan struct{}, 2)
	go func() { app.OnNotification("first", first); done <- struct{}{} }()
	select {
	case <-conn.entered:
	case <-time.After(time.Second):
		t.Fatal("first not blocked")
	}
	go func() { app.OnNotification("second", second); done <- struct{}{} }()
	until := time.Now().Add(time.Second)
	for json.BorrowTestActive() != 2 && time.Now().Before(until) {
		runtime.Gosched()
	}
	if json.BorrowTestActive() != 2 {
		t.Fatal("second encoder not held while waiting for Publish lock")
	}
	for i := 0; i < 100; i++ {
		var l json.MarshalLease
		if e := l.Encode(strings.Repeat("c", 4094)); e != nil {
			t.Fatal(e)
		}
		l.Release()
	}
	close(conn.resume)
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("publication stuck")
		}
	}
	if json.BorrowTestActive() != 0 {
		t.Fatal("leases not returned after both publications")
	}
	conn.mu.Lock()
	data := append([]byte(nil), conn.wire.Bytes()...)
	conn.mu.Unlock()
	r := bufio.NewReader(bytes.NewReader(data))
	for i, v := range []string{first, second} {
		h, p, e := readBorrowPacket(r)
		if e != nil {
			t.Fatal(e)
		}
		n := int(binary.BigEndian.Uint16(p))
		want, _ := json.Marshal(v)
		if h != 0x30 || !bytes.Equal(p[2+n:], want) {
			t.Fatalf("concurrent packet %d corrupt", i)
		}
	}
	if r.Buffered() != 0 {
		t.Fatal("extra wire bytes")
	}
}
