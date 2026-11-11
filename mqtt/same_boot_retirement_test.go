//go:build natiu && !tinygo

package mqtt

import (
	"bufio"
	"errors"
	"io"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
	"net"
	"sync"
	"testing"
	"time"
)

// Same public lifecycle oracle compiles against OLD and NEW. OLD waits RX but
// not dispatch, so its next dial overtakes an old-generation command callback.
func TestSameBootRetirementJoinsDispatchBeforeDial(t *testing.T) {
	c := NewNatiuClient(config.DefaultConfig().MQTT, logger.NewSerial(io.Discard, logger.INFO))
	a, b := net.Pipe()
	called := make(chan struct{}, 1)
	first := true
	c.dial = func(string, string) (net.Conn, error) {
		if first {
			first = false
			return a, nil
		}
		called <- struct{}{}
		return nil, errors.New("offline retry sentinel")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer func() { unblock(); _ = c.Disconnect() }()
	brokerDone := make(chan struct{})
	go func() {
		defer close(brokerDone)
		defer b.Close()
		r := bufio.NewReader(b)
		if _, e := readPacket(r); e != nil {
			return
		}
		if _, e := b.Write([]byte{0x20, 2, 0, 0}); e != nil {
			return
		}
		p, e := readPacket(r)
		if e != nil {
			return
		}
		if _, e = b.Write([]byte{0x90, 3, p.data[0], p.data[1], 0}); e != nil {
			return
		}
		topic := []byte("retire/commands")
		frame := []byte{0x30, byte(2 + len(topic) + 1), 0, byte(len(topic))}
		frame = append(frame, topic...)
		frame = append(frame, 'x')
		_, _ = b.Write(frame)
		<-entered
	}()
	if e := c.Connect(); e != nil {
		t.Fatal(e)
	}
	if e := c.Subscribe("retire/commands", func(string, []byte) { close(entered); <-release }); e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("callback not reached")
	}
	select {
	case <-c.done:
	case <-time.After(time.Second):
		t.Fatal("RX close not reached")
	}
	returned := make(chan error, 1)
	go func() { returned <- c.Connect() }()
	premature := false
	select {
	case <-called:
		premature = true
	case <-time.After(100 * time.Millisecond):
	}
	unblock()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("reconnect did not join after callback return")
	}
	<-brokerDone
	if premature {
		t.Fatal("next generation dial overtook old dispatch callback")
	}
}
