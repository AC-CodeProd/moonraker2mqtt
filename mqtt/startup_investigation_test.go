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
			go func() {
				defer close(joined)
				defer b.Close()
				b.SetDeadline(time.Now().Add(time.Second))
				readPacket(bufio.NewReader(b))
				if reject {
					b.Write([]byte{0x20, 2, 0, 5})
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
