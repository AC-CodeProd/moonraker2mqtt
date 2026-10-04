//go:build natiu && !tinygo

package mqtt

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"net"
	"testing"
	"time"
)

type wirePacket struct {
	header byte
	data   []byte
}

func readPacket(r *bufio.Reader) (wirePacket, error) {
	h, err := r.ReadByte()
	if err != nil {
		return wirePacket{}, err
	}
	length := 0
	mult := 1
	for {
		b, err := r.ReadByte()
		if err != nil {
			return wirePacket{}, err
		}
		length += int(b&127) * mult
		if b&128 == 0 {
			break
		}
		mult *= 128
		if mult > 128*128*128 {
			return wirePacket{}, io.ErrUnexpectedEOF
		}
	}
	data := make([]byte, length)
	_, err = io.ReadFull(r, data)
	return wirePacket{h, data}, err
}
func TestNatiuWireLifecycle(t *testing.T) {
	cfg := config.DefaultConfig().MQTT
	log := logger.NewSerial(io.Discard, logger.INFO)
	client := NewNatiuClient(cfg, log)
	a, b := net.Pipe()
	client.dial = func(string, string) (net.Conn, error) { return a, nil }
	seen := make(chan wirePacket, 8)
	errs := make(chan error, 1)
	go func() {
		defer b.Close()
		reader := bufio.NewReader(b)
		for {
			packet, err := readPacket(reader)
			if err != nil {
				errs <- err
				return
			}
			seen <- packet
			switch packet.header >> 4 {
			case 1:
				b.Write([]byte{0x20, 2, 0, 0})
			case 8:
				b.Write([]byte{0x90, 3, packet.data[0], packet.data[1], 0})
				topic := []byte("moonraker/commands")
				payload := []byte(`{"command":"restart"}`)
				frame := []byte{0x30, byte(2 + len(topic) + len(payload)), 0, byte(len(topic))}
				frame = append(frame, topic...)
				frame = append(frame, payload...)
				b.Write(frame)
			case 10:
				b.Write([]byte{0xb0, 2, packet.data[0], packet.data[1]})
			case 12:
				b.Write([]byte{0xd0, 0})
			case 14:
				return
			}
		}
	}()
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect()
	if !client.IsConnected() {
		t.Fatal("not connected")
	}
	received := make(chan incoming, 1)
	if err := client.Subscribe("moonraker/commands", func(topic string, payload []byte) { received <- incoming{topic, append([]byte(nil), payload...)} }); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-received:
		if msg.topic != "moonraker/commands" || string(msg.payload) != `{"command":"restart"}` {
			t.Fatalf("bad message: %#v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("no command dispatched")
	}
	if err := client.Publish("moonraker/state", []byte("ready"), 0, true, 3); err != nil {
		t.Fatal(err)
	}
	if err := client.Unsubscribe("moonraker/commands"); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	remaining := len(client.handlers)
	client.mu.Unlock()
	if remaining != 0 {
		t.Fatal("unsubscribe retained handler")
	}
	if err := client.Disconnect(); err != nil {
		t.Fatal(err)
	}
	expected := []byte{1, 8, 3, 10, 14}
	for _, want := range expected {
		select {
		case packet := <-seen:
			if packet.header>>4 != want {
				t.Fatalf("packet type %d, want %d", packet.header>>4, want)
			}
			if want == 3 {
				if packet.header&1 == 0 {
					t.Fatal("retain flag lost")
				}
				n := int(binary.BigEndian.Uint16(packet.data))
				if !bytes.Equal(packet.data[2:2+n], []byte("moonraker/state")) || string(packet.data[2+n:]) != "ready" {
					t.Fatal("publication changed")
				}
			}
		case <-time.After(time.Second):
			t.Fatalf("no wire packet %d", want)
		}
	}
	if client.IsConnected() {
		t.Fatal("still connected after disconnect")
	}
}

// Local fake brokers only: never contact a printer or external MQTT service.
func regressionClient(t *testing.T, broker func(net.Conn)) *NatiuClient {
	t.Helper()
	c := NewNatiuClient(config.DefaultConfig().MQTT, logger.NewSerial(io.Discard, logger.INFO))
	a, b := net.Pipe()
	c.dial = func(string, string) (net.Conn, error) { return a, nil }
	go func() {
		defer b.Close()
		broker(b)
	}()
	t.Cleanup(func() { c.Disconnect() })
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNatiuPublishAfterNearDeadlineIdle(t *testing.T) {
	received := make(chan incoming, 1)
	brokerErr := make(chan error, 1)
	c := regressionClient(t, func(b net.Conn) {
		r := bufio.NewReader(b)
		if _, err := readPacket(r); err != nil {
			brokerErr <- err
			return
		}
		if _, err := b.Write([]byte{0x20, 2, 0, 0}); err != nil {
			brokerErr <- err
			return
		}
		p, err := readPacket(r)
		if err != nil {
			brokerErr <- err
			return
		}
		if _, err = b.Write([]byte{0x90, 3, p.data[0], p.data[1], 0}); err != nil {
			brokerErr <- err
			return
		}
		// The next receive is idle for 1.9s, then a valid PUBLISH takes
		// 200ms to complete: it must not inherit the idle deadline.
		time.Sleep(1900 * time.Millisecond)
		if _, err = b.Write([]byte{0x30, 6, 0, 1, 'x', 'a'}); err != nil {
			brokerErr <- err
			return
		}
		time.Sleep(200 * time.Millisecond)
		_, err = b.Write([]byte{'b', 'c'})
		brokerErr <- err
		for {
			p, err := readPacket(r)
			if err != nil || p.header>>4 == 14 {
				return
			}
		}
	})
	if err := c.Subscribe("x", func(topic string, payload []byte) { received <- incoming{topic, payload} }); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-received:
		if msg.topic != "x" || string(msg.payload) != "abc" {
			t.Fatalf("bad publication: %#v", msg)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("valid fragmented PUBLISH was not delivered after near-deadline idle")
	}
	if err := <-brokerErr; err != nil {
		t.Fatal(err)
	}
	if !c.IsConnected() {
		t.Fatal("valid PUBLISH disconnected client")
	}
}

func TestNatiuRejectedResubscribeDoesNotUseHistoricalTopic(t *testing.T) {
	c := regressionClient(t, func(b net.Conn) {
		r := bufio.NewReader(b)
		subscriptions := 0
		for {
			p, err := readPacket(r)
			if err != nil {
				return
			}
			switch p.header >> 4 {
			case 1:
				b.Write([]byte{0x20, 2, 0, 0})
			case 8:
				subscriptions++
				code := byte(0)
				if subscriptions == 2 || subscriptions == 4 {
					code = 0x80
				}
				b.Write([]byte{0x90, 3, p.data[0], p.data[1], code})
			case 10:
				b.Write([]byte{0xb0, 2, p.data[0], p.data[1]})
			case 14:
				return
			}
		}
	})
	handler := func(string, []byte) {}
	if err := c.Subscribe("x", handler); err != nil {
		t.Fatal(err)
	}
	if err := c.Unsubscribe("x"); err != nil {
		t.Fatal(err)
	}
	if err := c.Subscribe("x", handler); err == nil {
		t.Fatal("rejected resubscription reported success from stale native topic list")
	}
	c.mu.Lock()
	_, exists := c.handlers["x"]
	c.mu.Unlock()
	if exists {
		t.Fatal("rejected resubscription retained handler")
	}
	if err := c.Subscribe("x", handler); err != nil {
		t.Fatalf("accepted retry failed: %v", err)
	}
	if err := c.Subscribe("x", handler); err == nil {
		t.Fatal("rejected replacement reported success")
	}
	c.mu.Lock()
	_, exists = c.handlers["x"]
	c.mu.Unlock()
	if !exists {
		t.Fatal("rejected replacement removed previous handler")
	}
}

func TestNatiuIncompletePacketRemainsBounded(t *testing.T) {
	for _, trickle := range []bool{false, true} {
		name := "stalled"
		limit := receiveProgressTimeout
		if trickle {
			name = "trickle"
			limit = receivePacketTimeout
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan time.Time, 1)
			c := regressionClient(t, func(b net.Conn) {
				r := bufio.NewReader(b)
				if _, err := readPacket(r); err != nil {
					return
				}
				if _, err := b.Write([]byte{0x20, 2, 0, 0}); err != nil {
					return
				}
				// QoS0 PUBLISH with 100 bytes of payload promised but not
				// completed. Keep reading so native error DISCONNECT cannot block.
				drained := make(chan struct{})
				go func() { io.Copy(io.Discard, r); close(drained) }()
				started <- time.Now()
				if _, err := b.Write([]byte{0x30, 103, 0, 1, 'x'}); err != nil {
					return
				}
				if !trickle {
					<-drained
					return
				}
				for {
					time.Sleep(time.Second)
					if _, err := b.Write([]byte{'a'}); err != nil {
						return
					}
				}
			})
			start := <-started
			select {
			case <-c.done:
				elapsed := time.Since(start)
				if elapsed < limit-200*time.Millisecond || elapsed > limit+time.Second {
					t.Fatalf("receive ended after %v, expected near %v", elapsed, limit)
				}
			case <-time.After(limit + 2*time.Second):
				t.Fatal("incomplete packet prevented bounded receive shutdown")
			}
			if c.IsConnected() {
				t.Fatal("incomplete packet left client connected")
			}
		})
	}
}

func TestNatiuRejectsUnsupportedAndOversized(t *testing.T) {
	cfg := config.DefaultConfig().MQTT
	c := NewNatiuClient(cfg, logger.NewSerial(io.Discard, logger.INFO))
	if err := c.Publish("x", nil, 1, false, 3); err == nil {
		t.Fatal("QoS1 accepted")
	}
	if err := c.Publish("x", make([]byte, MaxPayload+1), 0, false, 3); err == nil {
		t.Fatal("oversized payload accepted")
	}
	if err := c.Subscribe("x/#", func(string, []byte) {}); err == nil {
		t.Fatal("wildcard accepted")
	}
	c.cfg.UseTLS = true
	if err := c.Connect(); err == nil {
		t.Fatal("TLS accepted")
	}
}
