//go:build natiu && !tinygo

package mqtt

import (
	"bufio"
	"context"
	"errors"
	"io"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"net"
	"runtime"
	"testing"
	"time"
)

func disconnectSameBootFixture(t *testing.T, c *NatiuClient) {
	t.Helper()
	if err := c.Disconnect(); err != nil {
		t.Errorf("disconnect same-boot fixture: %v", err)
	}
}

func joinSameBootFixture(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Error("same-boot fixture worker did not join")
	}
}

func TestSameBootWorkersAndDecoderSurviveRetryAndGC(t *testing.T) {
	c := NewNatiuClient(config.DefaultConfig().MQTT, logger.NewSerial(io.Discard, logger.ERROR))
	defer disconnectSameBootFixture(t, c)
	var backing *byte
	var rx, dispatch, cancel chan struct{}
	var wires []*packetConn
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for cycle := 0; cycle < 8; cycle++ {
		c.dial = func(string, string) (net.Conn, error) { return nil, errors.New("offline dial failure") }
		if e := c.Connect(); e == nil {
			t.Fatal("failed dial succeeded")
		}
		if backing == nil {
			backing = &c.lifecycle.decoder[0]
			rx = c.lifecycle.rxExit
			dispatch = c.lifecycle.dispatchExit
			cancel = c.lifecycle.cancelExit
		}
		a, b := net.Pipe()
		c.dial = func(string, string) (net.Conn, error) { return a, nil }
		peer := make(chan struct{})
		t.Cleanup(func() { closeNatiuFixture(t, a); closeNatiuFixture(t, b); joinSameBootFixture(t, peer) })
		go func() {
			defer close(peer)
			defer closeNatiuFixture(t, b)
			r := bufio.NewReader(b)
			if _, e := readPacket(r); e != nil {
				return
			}
			if _, e := b.Write([]byte{0x20, 2, 0, 0}); e != nil {
				return
			}
			_, _ = readPacket(r)
		}()
		if e := c.Connect(); e != nil {
			t.Fatal(e)
		}
		runtime.GC()
		if &c.lifecycle.decoder[0] != backing || c.lifecycle.rxExit != rx || c.lifecycle.dispatchExit != dispatch || c.lifecycle.cancelExit != cancel || c.lifecycle.starts != 3 {
			t.Fatal("durable worker/decoder owner changed")
		}
		_, wire := c.snapshot()
		for _, old := range wires {
			if old == wire {
				t.Fatal("wire identity reused while old generation retained")
			}
		}
		wires = append(wires, wire)
		if e := c.Publish("offline/state", []byte("ready"), 0, false, 3); e != nil {
			t.Fatal(e)
		}
		select {
		case <-c.done:
		case <-time.After(time.Second):
			t.Fatal("failed generation did not leave RX")
		}
		<-peer
	}
	runtime.ReadMemStats(&after)
	t.Logf("HOST_ONLY transient mallocs=%d total_alloc_bytes=%d; decoder capacity=%d worker starts=%d generations=%d; NOT MCU allocation/peak evidence", after.Mallocs-before.Mallocs, after.TotalAlloc-before.TotalAlloc, cap(c.lifecycle.decoder), c.lifecycle.starts, c.lifecycle.generation)
}
func TestSameBootCancellationInterruptsHandshakeAndPublish(t *testing.T) {
	for _, stage := range []string{"handshake", "publish"} {
		t.Run(stage, func(t *testing.T) {
			c := NewNatiuClient(config.DefaultConfig().MQTT, logger.NewSerial(io.Discard, logger.ERROR))
			ctx, cancel := context.WithCancel(context.Background())
			defer func() {
				cancel()
				// Cancellation closes the pipe independently of RX. Wait until RX
				// has retired before requesting the final, checked Disconnect.
				c.mu.Lock()
				rxDone := c.done
				c.mu.Unlock()
				if rxDone != nil {
					joinSameBootFixture(t, rxDone)
				}
				disconnectSameBootFixture(t, c)
			}()
			defer cancel()
			a, b := net.Pipe()
			defer closeNatiuFixture(t, b)
			c.dial = func(string, string) (net.Conn, error) { return a, nil }
			reached := make(chan struct{})
			peer := make(chan struct{})
			t.Cleanup(func() { closeNatiuFixture(t, a); closeNatiuFixture(t, b); joinSameBootFixture(t, peer) })
			go func() {
				defer close(peer)
				r := bufio.NewReader(b)
				if _, e := readPacket(r); e != nil {
					return
				}
				if stage == "publish" {
					if _, e := b.Write([]byte{0x20, 2, 0, 0}); e != nil {
						return
					}
				}
				close(reached)
				<-ctx.Done()
			}()
			connected := make(chan error, 1)
			connectFinished := make(chan struct{})
			go func() { defer close(connectFinished); connected <- c.ConnectContext(ctx) }()
			t.Cleanup(func() { joinSameBootFixture(t, connectFinished) })
			<-reached
			if stage == "handshake" {
				cancel()
				select {
				case e := <-connected:
					if e == nil {
						t.Fatal("canceled handshake succeeded")
					}
				case <-time.After(time.Second):
					t.Fatal("handshake not interrupted")
				}
			} else {
				if e := <-connected; e != nil {
					t.Fatal(e)
				}
				wrote := make(chan error, 1)
				publishFinished := make(chan struct{})
				go func() {
					defer close(publishFinished)
					wrote <- c.Publish("offline/state", make([]byte, 4096), 0, false, 3)
				}()
				t.Cleanup(func() { joinSameBootFixture(t, publishFinished) })
				// A pipe with no broker reads cannot accept even the first publication byte.
				cancel()
				select {
				case e := <-wrote:
					if e == nil {
						t.Fatal("blocked canceled publish succeeded")
					}
				case <-time.After(time.Second):
					t.Fatal("publish not interrupted")
				}
			}
			<-peer
		})
	}
}
func TestSameBootGenerationCounterDoesNotWrap(t *testing.T) {
	c := NewNatiuClient(config.DefaultConfig().MQTT, logger.NewSerial(io.Discard, logger.ERROR))
	defer disconnectSameBootFixture(t, c)
	c.lifecycle.generation = ^uint64(0)
	dialed := false
	c.dial = func(string, string) (net.Conn, error) { dialed = true; return nil, io.EOF }
	if e := c.Connect(); e == nil || dialed || c.lifecycle.generation != ^uint64(0) {
		t.Fatal("exhausted generation accepted or wrapped")
	}
}
