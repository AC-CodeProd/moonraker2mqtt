//go:build natiu && !tinygo

package mqtt

import (
	"bufio"
	"bytes"
	"errors"
	native "github.com/soypat/natiu-mqtt"
	"io"
	"net"
	"testing"
	"time"
)

func publishHeader(t *testing.T, n int, qos native.QoSLevel) (native.Header, native.VariablesPublish) {
	t.Helper()
	v := native.VariablesPublish{TopicName: []byte("topic"), PacketIdentifier: 1}
	h, err := native.NewHeader(native.PacketPublish, native.PacketFlags(qos<<1), uint32(n+v.Size(qos)))
	if err != nil {
		t.Fatal(err)
	}
	return h, v
}
func TestReceivePayloadExactCapacityAndOwned(t *testing.T) {
	for _, n := range []int{0, 1, 511, 512, 1024, 2048, MaxPayload} {
		for _, qos := range []native.QoSLevel{native.QoS0, native.QoS1, native.QoS2} {
			h, v := publishHeader(t, n, qos)
			input := bytes.Repeat([]byte{'x'}, n)
			data, err := readPublishPayload(h, v, bytes.NewReader(input))
			if err != nil || !bytes.Equal(data, input) {
				t.Fatalf("n=%d qos=%d data/error mismatch: %v", n, qos, err)
			}
			if cap(data) != len(data) {
				t.Fatalf("n=%d qos=%d capacity=%d: renewed backing-store growth", n, qos, cap(data))
			}
			for i := range input {
				input[i] = 'z'
			}
			other, err := readPublishPayload(h, v, bytes.NewReader(input))
			if err != nil {
				t.Fatal(err)
			}
			for i := range data {
				if data[i] != 'x' || other[i] != 'z' {
					t.Fatal("payload aliases transport or following message")
				}
			}
		}
	}
}

type shortPayloadReader struct {
	r   io.Reader
	max int
}

func (r shortPayloadReader) Read(b []byte) (int, error) {
	if len(b) > r.max {
		b = b[:r.max]
	}
	return r.r.Read(b)
}

type rejectRead struct{ called bool }

func (r *rejectRead) Read(b []byte) (int, error) {
	r.called = true
	return 0, errors.New("unexpected read")
}

type failedPayload struct{ err error }

func (r failedPayload) Read(b []byte) (int, error) { return 0, r.err }
func TestReceivePayloadFragmentedAndLeftovers(t *testing.T) {
	h, v := publishHeader(t, MaxPayload, native.QoS0)
	input := bytes.Repeat([]byte{'x'}, MaxPayload)
	input = append(input, 0xd0, 0)
	wire := bytes.NewReader(input)
	data, err := readPublishPayload(h, v, shortPayloadReader{wire, 7})
	if err != nil || len(data) != MaxPayload || cap(data) != MaxPayload || wire.Len() != 2 {
		t.Fatalf("fragmented data/leftovers: %d %d %d %v", len(data), cap(data), wire.Len(), err)
	}
}
func TestReceivePayloadErrorsBeforeAllocationOrRead(t *testing.T) {
	for _, n := range []int{MaxPayload + 1, 100000} {
		h, v := publishHeader(t, n, native.QoS0)
		r := new(rejectRead)
		if _, err := readPublishPayload(h, v, r); err == nil || r.called {
			t.Fatal("oversized payload read before rejection")
		}
	}
	h, v := publishHeader(t, 0, native.QoS0)
	h.RemainingLength = 1
	r := new(rejectRead)
	if _, err := readPublishPayload(h, v, r); err == nil || r.called {
		t.Fatal("malformed remaining length not rejected before read")
	}
	h, v = publishHeader(t, 10, native.QoS0)
	if _, err := readPublishPayload(h, v, bytes.NewReader([]byte("short"))); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated packet: %v", err)
	}
	if _, err := readPublishPayload(h, v, bytes.NewReader(nil)); !errors.Is(err, io.EOF) {
		t.Fatalf("missing packet: %v", err)
	}
	sentinel := errors.New("transport failed")
	if _, err := readPublishPayload(h, v, failedPayload{sentinel}); !errors.Is(err, sentinel) {
		t.Fatalf("transport error not preserved: %v", err)
	}
}
func TestNatiuReceiveMaximumOwnedWirePayloads(t *testing.T) {
	makeFrame := func(fill byte) []byte {
		h, err := native.NewHeader(native.PacketPublish, 0, MaxPayload+3)
		if err != nil {
			t.Fatal(err)
		}
		var header [5]byte
		n := h.Put(header[:])
		frame := append([]byte(nil), header[:n]...)
		frame = append(frame, 0, 1, 'x')
		return append(frame, bytes.Repeat([]byte{fill}, MaxPayload)...)
	}
	first, second := makeFrame('a'), makeFrame('b')
	c := regressionClient(t, func(b net.Conn) {
		reader := bufio.NewReader(b)
		if _, err := readPacket(reader); err != nil {
			return
		}
		if _, err := b.Write([]byte{0x20, 2, 0, 0}); err != nil {
			return
		}
		p, err := readPacket(reader)
		if err != nil {
			return
		}
		if _, err := b.Write([]byte{0x90, 3, p.data[0], p.data[1], 0}); err != nil {
			return
		}
		for _, frame := range [][]byte{first, second} {
			for len(frame) > 0 {
				n := min(len(frame), 7)
				if _, err := b.Write(frame[:n]); err != nil {
					return
				}
				frame = frame[n:]
			}
		}
		for {
			p, err := readPacket(reader)
			if err != nil || p.header>>4 == 14 {
				return
			}
		}
	})
	messages := make(chan incoming, 2)
	if err := c.Subscribe("x", func(topic string, data []byte) { messages <- incoming{topic, data} }); err != nil {
		t.Fatal(err)
	}
	var got [2]incoming
	for i := range got {
		select {
		case got[i] = <-messages:
		case <-time.After(3 * time.Second):
			t.Fatal("maximum payload not delivered")
		}
	}
	for i, msg := range got {
		fill := byte('a' + i)
		if msg.topic != "x" || len(msg.payload) != MaxPayload || cap(msg.payload) != MaxPayload || !bytes.Equal(msg.payload, bytes.Repeat([]byte{fill}, MaxPayload)) {
			t.Fatalf("message %d changed or grew: len=%d cap=%d", i, len(msg.payload), cap(msg.payload))
		}
	}
}

func TestReceivePayloadOneOwnedAllocation(t *testing.T) {
	h, v := publishHeader(t, MaxPayload, native.QoS0)
	input := bytes.Repeat([]byte{'x'}, MaxPayload)
	r := bytes.NewReader(input)
	allocs := testing.AllocsPerRun(100, func() {
		r.Reset(input)
		b, e := readPublishPayload(h, v, r)
		if e != nil || len(b) != MaxPayload {
			panic("receive failed")
		}
	})
	if allocs != 1 {
		t.Fatalf("owned payload requires one exact allocation, got %g", allocs)
	}
}
