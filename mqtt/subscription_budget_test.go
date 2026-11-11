//go:build natiu && !tinygo

package mqtt

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
)

// The real official decoder/client receives unmodified wire packets.
func budgetClient(t *testing.T, mode byte) (*NatiuClient, context.Context) {
	t.Helper()
	c := NewNatiuClient(config.DefaultConfig().MQTT, logger.NewSerial(io.Discard, logger.ERROR))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	var peers sync.WaitGroup
	c.dial = func(string, string) (net.Conn, error) {
		a, b := net.Pipe()
		peers.Add(1)
		go func() {
			defer peers.Done()
			defer b.Close()
			r := bufio.NewReader(b)
			for {
				p, e := readPacket(r)
				if e != nil {
					return
				}
				switch p.header >> 4 {
				case 1:
					if _, e = b.Write([]byte{0x20, 2, 0, 0}); e != nil {
						return
					}
				case 8:
					id := binary.BigEndian.Uint16(p.data)
					code := byte(0)
					if mode == 1 {
						id ^= 0x8000
					}
					if mode == 2 {
						code = 0x80
					}
					ack := []byte{0x90, 3, byte(id >> 8), byte(id), code}
					if mode == 3 {
						ack = []byte{0x90, 4, byte(id >> 8), byte(id), 0, 0}
					}
					if _, e = b.Write(ack); e != nil {
						return
					}
					if mode == 4 {
						if _, e = b.Write(ack); e != nil {
							return
						}
					}
				case 12:
					if _, e = b.Write([]byte{0xd0, 0}); e != nil {
						return
					}
				case 14:
					return
				}
			}
		}()
		return a, nil
	}
	t.Cleanup(func() { cancel(); _ = c.Disconnect(); peers.Wait() })
	if e := c.ConnectContext(ctx); e != nil {
		t.Fatal(e)
	}
	return c, ctx
}

func TestSubscriptionBudgetFreshGeneration(t *testing.T) {
	c, ctx := budgetClient(t, 0)
	first, _ := c.snapshot()
	for i := 0; i < 16; i++ {
		if e := c.Subscribe("offline/topic", func(string, []byte) {}); e != nil {
			t.Fatalf("request %d: %v", i, e)
		}
	}
	if n := len(first.SubscribedTopics()); n != 16 {
		t.Fatalf("history=%d", n)
	}
	if e := c.Subscribe("offline/topic", func(string, []byte) {}); e == nil {
		t.Fatal("17th request admitted")
	}
	if e := c.ConnectContext(ctx); e != nil {
		t.Fatal(e)
	}
	second, _ := c.snapshot()
	if second == first {
		t.Fatal("old native client reused")
	}
	if c.lifecycle.generation != 2 || c.lifecycle.starts != 3 {
		t.Fatal("generation/workers")
	}
	if e := c.Subscribe("offline/topic", func(string, []byte) {}); e != nil {
		t.Fatal(e)
	}
	if e := c.Publish("offline/state", []byte("recovered"), 0, false, 0); e != nil {
		t.Fatal(e)
	}
}

func TestSubscriptionBudgetProtocolEdges(t *testing.T) {
	for _, mode := range []byte{1, 2, 3, 4} {
		t.Run(string(rune('0'+mode)), func(t *testing.T) {
			c, ctx := budgetClient(t, mode)
			for i := 0; i < 24; i++ {
				client, _ := c.snapshot()
				c.mu.Lock()
				beforeHandlers := len(c.handlers)
				c.mu.Unlock()
				err := c.Subscribe("offline/topic", func(string, []byte) {})
				if mode != 4 && err == nil {
					t.Fatal("invalid/rejected SUBACK accepted")
				}
				if n := len(client.SubscribedTopics()); n > 16 {
					t.Fatalf("history=%d", n)
				}
				if mode != 2 {
					select {
					case <-c.done:
					case <-time.After(time.Second):
						t.Fatal("protocol violation did not stop receive generation")
					}
					if len(client.SubscribedTopics()) > 1 {
						t.Fatal("decoded after terminal SUBACK")
					}
				}
				if err != nil {
					c.mu.Lock()
					count := len(c.handlers)
					c.mu.Unlock()
					if count != beforeHandlers {
						t.Fatal("failed subscription escaped handler rollback")
					}
				}
				if !c.IsConnected() {
					if e := c.ConnectContext(ctx); e != nil {
						t.Fatal(e)
					}
				}
			}
		})
	}
}
