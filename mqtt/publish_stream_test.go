//go:build natiu && !tinygo

package mqtt

import (
	"bufio"
	"bytes"
	"errors"
	native "github.com/soypat/natiu-mqtt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type streamTestConn struct {
	net.Conn
	output        bytes.Buffer
	maxWrite      int
	calls         int
	failAt        int
	failure       error
	short         bool
	partial       bool
	deadlineError error
	closed        bool
	entered       chan struct{}
	release       chan struct{}
}

func (c *streamTestConn) SetWriteDeadline(time.Time) error { return c.deadlineError }
func (c *streamTestConn) Close() error                     { c.closed = true; return nil }
func (c *streamTestConn) Write(p []byte) (int, error) {
	c.calls++
	if len(p) > c.maxWrite {
		c.maxWrite = len(p)
	}
	if c.calls == 1 && c.entered != nil {
		close(c.entered)
		<-c.release
	}
	if c.calls == c.failAt {
		n := 0
		if c.partial {
			n = len(p) / 2
			c.output.Write(p[:n])
		}
		if c.short {
			return n, nil
		}
		return n, c.failure
	}
	return c.output.Write(p)
}
func TestStreamPublishBoundedAndEquivalent(t *testing.T) {
	for _, size := range []int{0, 125, 126, 4096} {
		for _, retained := range []bool{false, true} {
			underlying := &streamTestConn{}
			c := &packetConn{Conn: underlying}
			topic := bytes.Repeat([]byte{'t'}, 256)
			payload := bytes.Repeat([]byte{'p'}, size)
			if err := c.writePublish(topic, payload, retained); err != nil {
				t.Fatal(err)
			}
			if underlying.maxWrite > 256 {
				t.Fatalf("assembled large write=%d", underlying.maxWrite)
			}
			reference := &streamTestConn{}
			var tx native.Tx
			tx.SetTxTransport(reference)
			flags, _ := native.NewPublishFlags(native.QoS0, false, retained)
			h, _ := native.NewHeader(native.PacketPublish, flags, 0)
			if err := tx.WritePublishPayload(h, native.VariablesPublish{TopicName: topic, PacketIdentifier: 1}, payload); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(underlying.output.Bytes(), reference.output.Bytes()) {
				t.Fatal("wire differs from pinned natiu")
			}
		}
	}
}
func TestStreamPublishShortWriteAndErrorsClose(t *testing.T) {
	sentinel := errors.New("write error")
	for _, short := range []bool{false, true} {
		for _, at := range []int{1, 2, 3, 4} {
			underlying := &streamTestConn{failAt: at, failure: sentinel, short: short}
			c := &packetConn{Conn: underlying}
			err := c.writePublish([]byte("topic"), bytes.Repeat([]byte{'p'}, 1024), false)
			want := sentinel
			if short {
				want = io.ErrShortWrite
			}
			if !errors.Is(err, want) || !underlying.closed {
				t.Fatalf("at=%d short=%v err=%v closed=%v", at, short, err, underlying.closed)
			}
		}
	}
}
func TestStreamPublishPartialAndDeadlineFailures(t *testing.T) {
	sentinel := errors.New("partial/deadline")
	for _, short := range []bool{false, true} {
		for _, at := range []int{1, 2, 3, 4} {
			underlying := &streamTestConn{failAt: at, failure: sentinel, short: short, partial: true}
			conn := &packetConn{Conn: underlying}
			err := conn.writePublish([]byte("topic"), bytes.Repeat([]byte{'p'}, 1024), false)
			want := sentinel
			if short {
				want = io.ErrShortWrite
			}
			if !errors.Is(err, want) || !underlying.closed {
				t.Fatalf("at=%d err=%v closed=%v", at, err, underlying.closed)
			}
		}
	}
	underlying := &streamTestConn{deadlineError: sentinel}
	conn := &packetConn{Conn: underlying}
	if err := conn.writePublish([]byte("topic"), nil, false); !errors.Is(err, sentinel) || !underlying.closed || underlying.calls != 0 {
		t.Fatalf("deadline err=%v calls=%d closed=%v", err, underlying.calls, underlying.closed)
	}
}

func TestStreamPublishInterleaving(t *testing.T) {
	underlying := &streamTestConn{entered: make(chan struct{}), release: make(chan struct{})}
	c := &packetConn{Conn: underlying}
	done := make(chan error, 2)
	go func() { done <- c.writePublish([]byte("topic"), bytes.Repeat([]byte{'p'}, 4096), false) }()
	<-underlying.entered
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { wg.Done(); _, err := c.Write([]byte{0xc0, 0}); done <- err }()
	wg.Wait()
	close(underlying.release)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	r := bufio.NewReader(&underlying.output)
	first, err := readPacket(r)
	if err != nil || first.header != 0x30 || len(first.data) != 4103 {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := readPacket(r)
	if err != nil || second.header != 0xc0 || len(second.data) != 0 {
		t.Fatalf("second=%#v err=%v", second, err)
	}
}
func TestStreamPublishNoPayloadAllocation(t *testing.T) {
	underlying := &streamTestConn{}
	underlying.output.Grow(10000)
	c := &packetConn{Conn: underlying}
	topic := []byte("topic")
	payload := bytes.Repeat([]byte{'p'}, 4096)
	allocs := testing.AllocsPerRun(100, func() {
		underlying.output.Reset()
		if err := c.writePublish(topic, payload, false); err != nil {
			panic(err)
		}
	})
	if allocs > 0 {
		t.Fatalf("writes allocate after startup: %v", allocs)
	}
}
