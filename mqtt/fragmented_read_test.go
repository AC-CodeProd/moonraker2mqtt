//go:build natiu && !tinygo

package mqtt

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	native "github.com/soypat/natiu-mqtt"
)

func TestFragmentedBodyReadCompletesExactRequest(t *testing.T) {
	raw := &subackBoundConn{maxRead: 1}
	raw.input.Reset([]byte{0x90, 3, 0x12, 0x34, 0})
	wire := &packetConn{Conn: raw}
	var header [2]byte
	if _, err := io.ReadFull(wire, header[:]); err != nil {
		t.Fatal(err)
	}
	var id [2]byte
	if n, err := wire.Read(id[:]); n != 2 || err != nil || id != [2]byte{0x12, 0x34} || raw.readBytes != 4 {
		t.Fatalf("short native field escaped: n=%d err=%v id=%x consumed=%d", n, err, id, raw.readBytes)
	}
	var code [1]byte
	if n, err := wire.Read(code[:]); n != 1 || err != nil || code[0] != 0 {
		t.Fatal("return code lost", n, err)
	}
}

func TestFragmentedOfficialDecoderFieldsAndAdjacentPackets(t *testing.T) {
	// Includes two-byte CONNACK/SUBACK/UNSUBACK fields, a topic string,
	// and a following PINGRESP which must not be consumed by a field read.
	stream := []byte{0x20, 2, 0, 0, 0x90, 3, 0x12, 0x34, 0,
		0x30, 8, 0, 3, 'a', 'b', 'c', 'x', 'y', 'z', 0xb0, 2, 0x12, 0x34, 0xd0, 0}
	for _, split := range []int{0, 1, 2} {
		raw := &subackBoundConn{maxRead: split}
		raw.input.Reset(stream)
		wire := &packetConn{Conn: raw}
		var rx native.Rx
		rx.SetRxTransport(wire)
		rx.SetDecoder(native.DecoderNoAlloc{UserBuffer: make([]byte, 256)})
		var events []string
		rx.RxCallbacks.OnConnack = func(*native.Rx, native.VariablesConnack) error { events = append(events, "connack"); return nil }
		rx.RxCallbacks.OnSuback = func(_ *native.Rx, ack native.VariablesSuback) error {
			if ack.PacketIdentifier != 0x1234 || len(ack.ReturnCodes) != 1 || ack.ReturnCodes[0] != native.QoS0 {
				t.Fatalf("SUBACK corrupted: %#v", ack)
			}
			events = append(events, "suback")
			return nil
		}
		rx.RxCallbacks.OnPub = func(_ *native.Rx, v native.VariablesPublish, r io.Reader) error {
			p, err := io.ReadAll(r)
			if err != nil {
				return err
			}
			if string(v.TopicName) != "abc" || string(p) != "xyz" {
				t.Fatalf("PUBLISH corrupted: %q %q", v.TopicName, p)
			}
			events = append(events, "publish")
			return nil
		}
		rx.RxCallbacks.OnOther = func(r *native.Rx, id uint16) error {
			if r.LastReceivedHeader.Type() == native.PacketUnsuback {
				if id != 0x1234 {
					t.Fatalf("UNSUBACK ID corrupted: %x", id)
				}
				events = append(events, "unsuback")
			} else {
				events = append(events, "pingresp")
			}
			return nil
		}
		for i := 0; i < 5; i++ {
			if err := wire.beginReceive(); err != nil {
				t.Fatal(err)
			}
			if _, err := rx.ReadNextPacket(); err != nil {
				t.Fatalf("split=%d packet=%d: %v", split, i, err)
			}
		}
		if strings.Join(events, ",") != "connack,suback,publish,unsuback,pingresp" || raw.readBytes != len(stream) {
			t.Fatalf("packet boundary lost: %v reads=%d", events, raw.readBytes)
		}
	}
}

type fragmentedConn struct{ net.Conn }

func (s *fragmentedConn) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return s.Conn.Read(p)
}

func TestFragmentedPublicConnectSubscribeDispatchAndNextPacket(t *testing.T) {
	c := newDeadlineClient()
	a, b := net.Pipe()
	c.dial = func(string, string) (net.Conn, error) { return &fragmentedConn{Conn: a}, nil }
	done := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel) // Graceful fixture Disconnect runs first (LIFO cleanup).
	t.Cleanup(func() { finishNatiuFixture(t, c, a, b, done) })
	go func() {
		defer close(done)
		defer closeNatiuFixture(t, b)
		r := bufio.NewReader(b)
		for {
			p, err := readPacket(r)
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
					t.Errorf("fragment broker read: %v", err)
				}
				return
			}
			switch p.header >> 4 {
			case 1:
				if !writeNatiuFixture(t, b, []byte{0x20, 2, 0, 0}) {
					return
				}
			case 8:
				if len(p.data) < 2 {
					t.Error("short SUBSCRIBE")
					return
				}
				if !writeNatiuFixture(t, b, []byte{0x90, 3, p.data[0], p.data[1], 0}) {
					return
				}
				// Same valid frame as the historical reproducer, followed by another packet.
				if !writeNatiuFixture(t, b, []byte{0x30, 4, 0, 1, 'x', 'y', 0x30, 4, 0, 1, 'x', 'z'}) {
					return
				}
			case 14:
				return
			}
		}
	}()
	if err := c.ConnectContext(ctx); err != nil {
		t.Fatal(err)
	}
	messages := make(chan string, 2)
	if err := c.Subscribe("x", func(topic string, payload []byte) { messages <- topic + ":" + string(payload) }); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"x:y", "x:z"} {
		select {
		case got := <-messages:
			if got != want {
				t.Fatalf("got %q want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatal("fragmented message lost")
		}
	}
	if !c.IsConnected() {
		t.Fatal("valid fragmented packets stopped connection")
	}
	if err := c.Publish("state", []byte("healthy"), 0, false, 0); err != nil {
		t.Fatal(err)
	}
}

type fragmentReadStep struct {
	data []byte
	err  error
}
type fragmentScriptConn struct {
	net.Conn
	steps           []fragmentReadStep
	reads           int
	deadlines       []time.Time
	setterFailureAt int
	setterError     error
}

func (s *fragmentScriptConn) Read(p []byte) (int, error) {
	s.reads++
	if len(s.steps) == 0 {
		return 0, io.EOF
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	return copy(p, step.data), step.err
}
func (s *fragmentScriptConn) SetReadDeadline(d time.Time) error {
	s.deadlines = append(s.deadlines, d)
	if len(s.deadlines) == s.setterFailureAt {
		return s.setterError
	}
	return nil
}

func TestFragmentedReadErrorsProgressAndAbsoluteCap(t *testing.T) {
	sentinel := errors.New("fragment read failed")
	for _, tc := range []struct {
		name  string
		steps []fragmentReadStep
		n     int
		err   error
	}{
		{"partial-eof", []fragmentReadStep{{[]byte{0x12}, nil}, {nil, io.EOF}}, 1, io.ErrUnexpectedEOF},
		{"full-eof", []fragmentReadStep{{[]byte{0x12}, nil}, {[]byte{0x34}, io.EOF}}, 2, nil},
		{"partial-error", []fragmentReadStep{{[]byte{0x12}, sentinel}}, 1, sentinel},
		{"full-error", []fragmentReadStep{{[]byte{0x12, 0x34}, sentinel}}, 2, sentinel},
		{"temporary-empty", []fragmentReadStep{{nil, nil}, {[]byte{0x12}, nil}, {nil, nil}, {[]byte{0x34}, nil}}, 2, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := &fragmentScriptConn{steps: tc.steps}
			cap := time.Now().Add(time.Second)
			wire := &packetConn{Conn: raw, receive: true, packetDeadline: cap, frame: receiveFrame{state: 5, remaining: 2}}
			var p [2]byte
			n, err := wire.Read(p[:])
			if n != tc.n || !errors.Is(err, tc.err) || wire.packetBytes != uint32(n) || !wire.packetDeadline.Equal(cap) {
				t.Fatalf("n=%d err=%v bytes=%d cap=%v", n, err, wire.packetBytes, wire.packetDeadline)
			}
			for _, d := range raw.deadlines {
				if !d.Equal(cap) {
					t.Fatal("physical read renewed absolute packet cap")
				}
			}
		})
	}
	t.Run("second-setter-fails", func(t *testing.T) {
		raw := &fragmentScriptConn{steps: []fragmentReadStep{{[]byte{0x12}, nil}, {[]byte{0x34}, nil}}, setterFailureAt: 2, setterError: sentinel}
		wire := &packetConn{Conn: raw, receive: true, packetDeadline: time.Now().Add(time.Second), frame: receiveFrame{state: 5, remaining: 2}}
		var p [2]byte
		if n, err := wire.Read(p[:]); n != 1 || !errors.Is(err, sentinel) || raw.reads != 1 || wire.packetBytes != 1 {
			t.Fatal("second setter failure admitted I/O", n, err, raw.reads)
		}
	})
	t.Run("bounded-no-progress", func(t *testing.T) {
		raw := &fragmentScriptConn{steps: make([]fragmentReadStep, 100)}
		wire := &packetConn{Conn: raw, frame: receiveFrame{state: 5, remaining: 2}}
		var p [2]byte
		if n, err := wire.Read(p[:]); n != 0 || !errors.Is(err, io.ErrNoProgress) || raw.reads != 100 {
			t.Fatal("no-progress reads not bounded", n, err, raw.reads)
		}
	})
}

func TestFragmentedMalformedTopicCannotConsumeNextFrame(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header byte
		body   []byte
	}{
		{"empty-body", 0x30, nil},
		{"short-length", 0x30, []byte{0}},
		{"exhausted-body", 0x30, []byte{0, 1}},
		{"short-topic", 0x30, []byte{0, 2, 'x'}},
		{"missing-id", 0x32, []byte{0, 1, 'x'}},
		{"short-id", 0x32, []byte{0, 1, 'x', 0x12}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, split := range []int{0, 1, 2} {
				for _, receive := range []bool{false, true} {
					raw := &subackBoundConn{maxRead: split}
					stream := append([]byte{tc.header, byte(len(tc.body))}, tc.body...)
					raw.input.Reset(append(stream, 0xd0, 0))
					wire := &packetConn{Conn: raw}
					if receive {
						if err := wire.beginReceive(); err != nil {
							t.Fatal(err)
						}
					}
					var rx native.Rx
					rx.SetRxTransport(wire)
					rx.SetDecoder(native.DecoderNoAlloc{UserBuffer: make([]byte, 256)})
					rx.RxCallbacks.OnPub = func(r *native.Rx, v native.VariablesPublish, payload io.Reader) error {
						_, err := readPublishPayload(r.LastReceivedHeader, v, payload)
						return err
					}
					if _, err := rx.ReadNextPacket(); err == nil || raw.readBytes > 2+len(tc.body) {
						t.Fatalf("split=%d receive=%v malformed field crossed frame: consumed=%d allowed=%d err=%v", split, receive, raw.readBytes, 2+len(tc.body), err)
					}
				}
			}
		})
	}
}

func TestFragmentedPublishTopicGeometryPositiveControls(t *testing.T) {
	for _, length := range []int{1, 255, 256} {
		for _, qos := range []byte{0, 1, 2} {
			for _, payload := range []string{"", "payload"} {
				for _, split := range []int{0, 1, 2} {
					topic := strings.Repeat("x", length)
					body := append([]byte{byte(length >> 8), byte(length)}, topic...)
					if qos != 0 {
						body = append(body, 0x12, 0x34)
					}
					body = append(body, payload...)
					header := subackBoundHeader(uint32(len(body)))
					header[0] = 0x30 | qos<<1
					stream := append(append(header, body...), 0xd0, 0)
					raw := &subackBoundConn{maxRead: split}
					raw.input.Reset(stream)
					wire := &packetConn{Conn: raw}
					var rx native.Rx
					rx.SetRxTransport(wire)
					rx.SetDecoder(native.DecoderNoAlloc{UserBuffer: make([]byte, 512)})
					called := false
					rx.RxCallbacks.OnPub = func(_ *native.Rx, v native.VariablesPublish, r io.Reader) error {
						p, err := io.ReadAll(r)
						if err != nil {
							return err
						}
						if string(v.TopicName) != topic || string(p) != payload || (qos != 0 && v.PacketIdentifier != 0x1234) {
							t.Fatal("valid variable header corrupted", length, qos, split)
						}
						called = true
						return nil
					}
					for i := 0; i < 2; i++ {
						if _, err := rx.ReadNextPacket(); err != nil {
							t.Fatal("valid frame rejected", length, qos, split, err)
						}
					}
					if !called || raw.readBytes != len(stream) || wire.frame.rejected {
						t.Fatal("positive control not consumed exactly", length, qos, split)
					}
				}
			}
		}
	}
}

func TestFragmentedMalformedTopicRetiresAndRecovers(t *testing.T) {
	c := newDeadlineClient()
	raw := &subackBoundConn{maxRead: 1}
	raw.input.Reset([]byte{0x20, 2, 0, 0, 0x30, 2, 0, 1, 0xd0, 0})
	c.dial = func(string, string) (net.Conn, error) { return raw, nil }
	t.Cleanup(func() {
		if err := c.Disconnect(); err != nil {
			t.Error(err)
		}
	})
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.done:
	case <-time.After(time.Second):
		t.Fatal("malformed topic did not join RX")
	}
	if raw.readBytes != 8 || c.IsConnected() {
		t.Fatalf("next frame consumed or RX still connected: consumed=%d", raw.readBytes)
	}
	first := c.lifecycle.rxStart
	_, seen := deadlineFixture(t, c)
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	if first != c.lifecycle.rxStart || c.lifecycle.starts != 3 || c.lifecycle.generation != 2 {
		t.Fatal("sameboot recovery replaced workers")
	}
	if err := c.Subscribe("command", func(string, []byte) {}); err != nil {
		t.Fatal(err)
	}
	if err := c.Publish("state", []byte("resumed"), 0, false, 0); err != nil {
		t.Fatal(err)
	}
	expectDeadlinePublication(t, seen, "resumed")
}

func TestFragmentedBodyCannotConsumeNextFrame(t *testing.T) {
	raw := &subackBoundConn{maxRead: 1}
	// Body declares only one byte: a two-byte field request is malformed.
	raw.input.Reset([]byte{0xb0, 1, 0x12, 0xd0, 0})
	wire := &packetConn{Conn: raw}
	var header [2]byte
	if _, err := io.ReadFull(wire, header[:]); err != nil {
		t.Fatal(err)
	}
	var id [2]byte
	if n, err := wire.Read(id[:]); err == nil || n > 1 || raw.readBytes > 3 {
		t.Fatalf("field crossed frame boundary: n=%d read=%d err=%v", n, raw.readBytes, err)
	}
}
