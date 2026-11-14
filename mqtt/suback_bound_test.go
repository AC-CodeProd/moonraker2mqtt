//go:build natiu && !tinygo

package mqtt

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	native "github.com/soypat/natiu-mqtt"
)

type subackBoundConn struct {
	net.Conn
	input                        bytes.Reader
	readBytes, readCalls, closes int
	maxRead                      int
}

func (s *subackBoundConn) Read(p []byte) (int, error) {
	s.readCalls++
	if s.maxRead > 0 && len(p) > s.maxRead {
		p = p[:s.maxRead]
	}
	n, err := s.input.Read(p)
	s.readBytes += n
	return n, err
}
func (s *subackBoundConn) Write(p []byte) (int, error)    { return len(p), nil }
func (s *subackBoundConn) Close() error                   { s.closes++; return nil }
func (*subackBoundConn) SetDeadline(time.Time) error      { return nil }
func (*subackBoundConn) SetReadDeadline(time.Time) error  { return nil }
func (*subackBoundConn) SetWriteDeadline(time.Time) error { return nil }

func subackBoundHeader(length uint32) []byte {
	h := []byte{0x90}
	for {
		b := byte(length & 127)
		length >>= 7
		if length != 0 {
			b |= 128
		}
		h = append(h, b)
		if length == 0 {
			return h
		}
	}
}

func TestSubackBoundRejectsBeforeNativeBody(t *testing.T) {
	for _, length := range []uint32{0, 1, 2, 4, 127, 128, 16384, 268435455} {
		t.Run(strconv.FormatUint(uint64(length), 10), func(t *testing.T) {
			header := subackBoundHeader(length)
			body := bytes.Repeat([]byte{0}, 32)
			raw := &subackBoundConn{}
			raw.input.Reset(append(append([]byte(nil), header...), body...))
			wire := &packetConn{Conn: raw}
			if err := wire.beginReceive(); err != nil {
				t.Fatal(err)
			}
			var rx native.Rx
			rx.SetRxTransport(wire)
			called := false
			rx.RxCallbacks.OnSuback = func(*native.Rx, native.VariablesSuback) error { called = true; return nil }
			_, err := rx.ReadNextPacket()
			if err == nil || called || raw.readBytes != len(header) {
				t.Fatalf("SUBACK length=%d err=%v callback=%v consumed=%d header=%d", length, err, called, raw.readBytes, len(header))
			}
			calls := raw.readCalls
			var p [16]byte
			if n, err := wire.Read(p[:]); n != 0 || err == nil || raw.readCalls != calls {
				t.Fatal("rejected packet admitted another read")
			}
		})
	}
}

func TestSubackBoundRejectsDuringConnect(t *testing.T) {
	header := subackBoundHeader(16386)
	raw := &subackBoundConn{}
	raw.input.Reset(append(header, make([]byte, 16386)...))
	wire := &packetConn{Conn: raw}
	client := native.NewClient(native.ClientConfig{})
	var vc native.VariablesConnect
	vc.SetDefaultMQTT([]byte("suback-bound-handshake"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Connect(ctx, wire, &vc); err == nil || raw.readBytes != len(header) || client.IsConnected() {
		t.Fatalf("handshake accepted oversized SUBACK: reads=%d header=%d err=%v", raw.readBytes, len(header), err)
	}
}

func TestSubackBoundValidCodesUseOfficialDecoder(t *testing.T) {
	for _, code := range []byte{0, 1, 2, 0x80} {
		raw := &subackBoundConn{}
		raw.input.Reset([]byte{0x90, 3, 0x12, 0x34, code})
		wire := &packetConn{Conn: raw}
		if err := wire.beginReceive(); err != nil {
			t.Fatal(err)
		}
		var rx native.Rx
		rx.SetRxTransport(wire)
		called := false
		rx.RxCallbacks.OnSuback = func(_ *native.Rx, ack native.VariablesSuback) error {
			called = true
			if ack.PacketIdentifier != 0x1234 || len(ack.ReturnCodes) != 1 || byte(ack.ReturnCodes[0]) != code {
				t.Fatalf("SUBACK changed: %#v", ack)
			}
			return nil
		}
		if _, err := rx.ReadNextPacket(); err != nil || !called || raw.readBytes != 5 {
			t.Fatalf("valid code=%x err=%v callback=%v read=%d", code, err, called, raw.readBytes)
		}
	}
}

func TestSubackBoundPreservesBytesAndFramePosition(t *testing.T) {
	// 0x90 inside a PUBLISH body must not be mistaken for another header.
	stream := []byte{0xd0, 0, 0x90, 3, 0, 1, 0, 0x30, 4, 0, 1, 'x', 0x90, 0xd0, 0}
	for _, maxRead := range []int{0, 1, 2} {
		raw := &subackBoundConn{maxRead: maxRead}
		raw.input.Reset(stream)
		wire := &packetConn{Conn: raw}
		got, err := subackWireBytes(wire)
		if err != nil || !bytes.Equal(got, stream) {
			t.Fatalf("maxRead=%d bytes changed: %x err=%v", maxRead, got, err)
		}
	}
	// A preceding complete packet cannot hide an oversized SUBACK in a handshake.
	raw := &subackBoundConn{}
	raw.input.Reset([]byte{0xd0, 0, 0x90, 4, 0, 1, 0, 0})
	wire := &packetConn{Conn: raw}
	got, err := subackWireBytes(wire)
	if err == nil || raw.readBytes != 4 || !bytes.Equal(got, []byte{0xd0, 0, 0x90, 4}) {
		t.Fatalf("subsequent invalid frame consumed: %x err=%v reads=%d", got, err, raw.readBytes)
	}
}

func TestSubackBoundPublicRXRetiresBeforeBodyAndRecovers(t *testing.T) {
	c := newDeadlineClient()
	header := subackBoundHeader(16386)
	raw := &subackBoundConn{}
	raw.input.Reset(append([]byte{0x20, 2, 0, 0}, append(header, make([]byte, 16386)...)...))
	c.dial = func(string, string) (net.Conn, error) { return raw, nil }
	t.Cleanup(func() {
		if err := c.Disconnect(); err != nil {
			t.Errorf("disconnect bound fixture: %v", err)
		}
	})
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.done:
	case <-time.After(time.Second):
		t.Fatal("oversized SUBACK did not join RX")
	}
	g := c.lifecycle.active
	if raw.readBytes != 4+len(header) || g.wire.packetBytes != uint32(len(header)) || len(g.client.SubscribedTopics()) != 0 || c.IsConnected() {
		t.Fatalf("body reached native RX: read=%d bytes=%d history=%d connected=%v", raw.readBytes, g.wire.packetBytes, len(g.client.SubscribedTopics()), c.IsConnected())
	}
	first := c.lifecycle.rxStart
	_, seen := deadlineFixture(t, c)
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	if first != c.lifecycle.rxStart || c.lifecycle.starts != 3 || c.lifecycle.generation != 2 {
		t.Fatal("same-boot recovery replaced workers")
	}
	if err := c.Subscribe("command", func(string, []byte) {}); err != nil {
		t.Fatal(err)
	}
	if err := c.Publish("state", []byte("resumed"), 0, false, 0); err != nil {
		t.Fatal(err)
	}
	expectDeadlinePublication(t, seen, "resumed")
}

func TestSubackBoundMalformedLengthStopsAtFourBytes(t *testing.T) {
	raw := &subackBoundConn{}
	raw.input.Reset([]byte{0x90, 0x80, 0x80, 0x80, 0x80, 3, 0, 1, 0})
	wire := &packetConn{Conn: raw}
	_, err := subackWireBytes(wire)
	if err == nil || raw.readBytes != 5 {
		t.Fatalf("malformed RemainingLength read=%d err=%v", raw.readBytes, err)
	}
}

// Observe unchanged wire bytes without requesting a field beyond its packet.
// Large io.ReadAll requests are not the official decoder's field-sized reads.
func subackWireBytes(wire *packetConn) ([]byte, error) {
	var out []byte
	var b [1]byte
	for {
		n, err := wire.Read(b[:])
		out = append(out, b[:n]...)
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
	}
}

// Preserve the adapter's exact single-topic shape without altering packet IDs.
func TestSubackBoundOriginalBytesReachAdapterSnapshot(t *testing.T) {
	raw := &subackBoundConn{}
	frame := []byte{0x90, 3, 0xab, 0xcd, 0x80}
	raw.input.Reset(frame)
	wire := &packetConn{Conn: raw}
	if err := wire.beginReceive(); err != nil {
		t.Fatal(err)
	}
	got, err := subackWireBytes(wire)
	if err != nil || !bytes.Equal(got, frame) {
		t.Fatal("wire bytes changed", err)
	}
	ack, ok := wire.suback()
	if !ok || ack.id != binary.BigEndian.Uint16(frame[2:4]) || ack.code != 0x80 {
		t.Fatal("adapter SUBACK snapshot changed")
	}

}
