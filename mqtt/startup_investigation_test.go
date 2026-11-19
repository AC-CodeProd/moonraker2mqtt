//go:build natiu && !tinygo

package mqtt

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"net"
	"testing"
	"time"
)

func TestInvestigationNatiuDialErrorNoCauseLog(t *testing.T) {
	var out bytes.Buffer
	c := NewNatiuClient(config.DefaultConfig().MQTT, logger.NewSerial(&out, logger.INFO))
	sentinel := errors.New("offline dial failure")
	c.dial = func(string, string) (net.Conn, error) { return nil, sentinel }
	if err := c.Connect(); !errors.Is(err, sentinel) {
		t.Fatalf("error %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("Connect unexpectedly logged %s", out.String())
	}
	if c.done != nil || c.client != nil || c.conn != nil || c.IsConnected() {
		t.Fatal("dial error allocated/installed client")
	}
	if err := c.Disconnect(); err != nil {
		t.Fatal(err)
	}
}
func TestInvestigationNatiuHandshakeRejectOrEOF(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "EOF", true: "rejected"}[reject], func(t *testing.T) {
			c := NewNatiuClient(config.DefaultConfig().MQTT, logger.NewSerial(io.Discard, logger.INFO))
			a, b := net.Pipe()
			c.dial = func(string, string) (net.Conn, error) { return a, nil }
			joined := make(chan struct{})
			t.Cleanup(func() { finishNatiuFixture(t, c, a, b, joined) })
			go func() {
				defer close(joined)
				defer closeNatiuFixture(t, b)
				if err := b.SetDeadline(time.Now().Add(time.Second)); err != nil {
					t.Errorf("set startup fixture deadline: %v", err)
					return
				}
				packet, err := readPacket(bufio.NewReader(b))
				if err != nil {
					t.Errorf("read startup fixture CONNECT: %v", err)
					return
				}
				if packet.header != 0x10 {
					t.Errorf("startup fixture expected CONNECT, got %#v", packet)
					return
				}
				if reject {
					if !writeNatiuFixture(t, b, []byte{0x20, 2, 0, 5}) {
						return
					}
				}
			}()
			if err := c.Connect(); err == nil {
				t.Fatal("handshake failure accepted")
			}
			if c.IsConnected() || c.done != nil || c.conn != nil {
				t.Fatal("workers installed after failed handshake")
			}
			if err := c.Disconnect(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-joined:
			case <-time.After(time.Second):
				t.Fatal("fake peer not joined")
			}
		})
	}
}
