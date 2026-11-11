//go:build natiu && !tinygo

package mqtt

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise accepted resubscription, rejected replacement and unique unsubscribe
// churn through the real adapter. All wire traffic stays within net.Pipe.
func TestSubscriptionHistoryBoundAcrossTopicChurn(t *testing.T) {
	c := NewNatiuClient(config.DefaultConfig().MQTT, logger.NewSerial(io.Discard, logger.ERROR))
	a, b := net.Pipe()
	c.dial = func(string, string) (net.Conn, error) { return a, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var wrong atomic.Bool
	peer := make(chan struct{})
	go func() {
		defer close(peer)
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
				if wrong.Load() {
					id ^= 0x8000
				}
				if _, e = b.Write([]byte{0x90, 3, byte(id >> 8), byte(id), 0}); e != nil {
					return
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
	defer func() {
		cancel()
		b.Close()
		c.Disconnect()
		select {
		case <-peer:
		case <-time.After(time.Second):
			t.Error("peer not joined")
		}
	}()
	if err := c.ConnectContext(ctx); err != nil {
		t.Fatal(err)
	}
	var originalCalls, replacementCalls int
	first := func(string, []byte) { originalCalls++ }
	replacement := func(string, []byte) { replacementCalls++ }
	for i := 0; i < 512; i++ {
		topic := fmt.Sprintf("offline/topic/%d", i)
		if err := c.Subscribe(topic, first); err != nil {
			t.Fatal(err)
		}
		if err := c.Subscribe(topic, first); err != nil {
			t.Fatal(err)
		}
		wrong.Store(true)
		if err := c.Subscribe(topic, replacement); err == nil || !strings.Contains(err.Error(), "identifier mismatch") {
			t.Fatalf("replacement accepted: %v", err)
		}
		wrong.Store(false)
		c.mu.Lock()
		h := len(c.handlers)
		restored := c.handlers[topic]
		c.mu.Unlock()
		if h != 1 || restored == nil {
			t.Fatal("prior handler not restored")
		}
		restored(topic, nil)
		if originalCalls != i+1 || replacementCalls != 0 {
			t.Fatal("rejected replacement handler escaped rollback")
		}
		if err := c.Unsubscribe(topic); err != nil {
			t.Fatal(err)
		}
		client, _ := c.snapshot()
		c.mu.Lock()
		h = len(c.handlers)
		c.mu.Unlock()
		if n := len(client.SubscribedTopics()); n != 0 || h != 0 {
			t.Fatalf("cycle=%d native=%d handlers=%d", i, n, h)
		}
	}
	for i := 0; i < maxSubscriptions; i++ {
		if err := c.Subscribe(fmt.Sprintf("offline/keep/%d", i), first); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Subscribe("offline/fifth", first); err == nil {
		t.Fatal("subscription bound lost")
	}
	client, _ := c.snapshot()
	c.mu.Lock()
	h := len(c.handlers)
	c.mu.Unlock()
	if len(client.SubscribedTopics()) != 0 || h != 4 || !c.IsConnected() || c.lifecycle.generation != 1 || c.lifecycle.starts != 3 {
		t.Fatal("capacity/lifecycle contract changed")
	}
	t.Log("512 unique topics; accepted repeat and rejected replacement with rollback; unsubscribe clears handlers; native history stays empty; four active subscriptions preserved; one generation and three reusable workers")
}
