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

	"github.com/soypat/lneto/tcp"
	"github.com/soypat/lneto/x/xnet"
	"moonraker2mqtt/config"
	"moonraker2mqtt/internal/nativetcp"
	"moonraker2mqtt/logger"
)

type nativeBrokerOwner struct {
	owner        *nativetcp.PublicOwner
	peerStack    *nativetcp.PublicStack
	peer         *tcp.Conn
	done         chan struct{}
	publications chan string
}

func (o *nativeBrokerOwner) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	p := new(tcp.Conn)
	if e := p.Configure(tcp.ConnConfig{TxBuf: make([]byte, 4096), RxBuf: make([]byte, 1024), TxPacketQueueSize: 4, RWBackoff: func(uint) time.Duration { return 5 * time.Millisecond }}); e != nil {
		return nil, e
	}
	var e error
	o.peerStack.Pump(func() { e = o.peerStack.Stack.ListenTCP4(p, 1883) })
	if e != nil {
		return nil, e
	}
	o.peer = p
	o.done = make(chan struct{})
	go func() {
		defer close(o.done)
		establishDeadline := time.Now().Add(time.Second)
		for p.State() != tcp.StateEstablished {
			if time.Now().After(establishDeadline) {
				return
			}
			time.Sleep(time.Millisecond)
		}
		r := bufio.NewReader(p)
		for {
			packet, e := readPacket(r)
			if e != nil {
				return
			}
			switch packet.header >> 4 {
			case 1:
				_, e = p.Write([]byte{0x20, 2, 0, 0})
			case 8:
				id := binary.BigEndian.Uint16(packet.data)
				_, e = p.Write([]byte{0x90, 3, byte(id >> 8), byte(id), 0})
			case 3:
				o.publications <- string(packet.data)
			case 12:
				_, e = p.Write([]byte{0xd0, 0})
			case 14:
				return
			}
			if e != nil {
				return
			}
		}
	}()
	return o.owner.DialContext(ctx, network, address)
}
func (o *nativeBrokerOwner) Retire() error {
	e := o.owner.Retire()
	if o.peer != nil {
		o.peerStack.Pump(func() { o.peer.Abort() })
		<-o.done
		o.peer = nil
	}
	return e
}

func TestPublicNativeMQTTBudgetRecovery(t *testing.T) {
	var a, b xnet.StackAsync
	for i, s := range []*xnet.StackAsync{&a, &b} {
		if e := s.Reset(xnet.StackConfig{Hostname: "offline-mqtt", RandSeed: int64(i + 1), StaticAddress4: [4]byte{192, 0, 2, byte(i + 1)}, HardwareAddress: [6]byte{2, 0, 0, 0, 0, byte(i + 1)}, MaxActiveTCPPorts: 2, MTU: 1500}); e != nil {
			t.Fatal(e)
		}
	}
	a.SetGatewayHardwareAddr(b.HardwareAddr())
	b.SetGatewayHardwareAddr(a.HardwareAddr())
	sa, sb := &nativetcp.PublicStack{Stack: &a}, &nativetcp.PublicStack{Stack: &b}
	stop := make(chan struct{})
	var pump sync.WaitGroup
	pump.Add(1)
	go func() {
		defer pump.Done()
		var buf [1600]byte
		for {
			select {
			case <-stop:
				return
			default:
			}
			var n int
			sa.Pump(func() { n, _ = a.EgressEthernet(buf[:]) })
			if n > 0 {
				sb.Pump(func() { _ = b.IngressEthernet(buf[:n]) })
			}
			sb.Pump(func() { n, _ = b.EgressEthernet(buf[:]) })
			if n > 0 {
				sa.Pump(func() { _ = a.IngressEthernet(buf[:n]) })
			}
			time.Sleep(time.Millisecond)
		}
	}()
	defer func() { close(stop); pump.Wait() }()
	owner, e := sa.NewOwner()
	if e != nil {
		t.Fatal(e)
	}
	mock := &nativeBrokerOwner{owner: owner, peerStack: sb, publications: make(chan string, 8)}
	cfg := config.DefaultConfig().MQTT
	cfg.Host = "192.0.2.2"
	cfg.Port = 1883
	c := NewNatiuClient(cfg, logger.NewSerial(io.Discard, logger.ERROR))
	if e = c.initMQTT(); e != nil {
		t.Fatal(e)
	}
	c.lifecycle.native = mock
	defer c.Disconnect()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var old net.Conn
	for generation := 0; generation < 3; generation++ {
		if e = c.ConnectContext(ctx); e != nil {
			t.Fatal(e)
		}
		native, wire := c.snapshot()
		if old != nil {
			if _, e = old.Write([]byte("stale")); e != net.ErrClosed {
				t.Fatal("old TCP generation still writable", e)
			}
		}
		old = wire.Conn
		for i := 0; i < subscriptionBudget; i++ {
			if e = c.Subscribe("offline/command", func(string, []byte) {}); e != nil {
				t.Fatal(e)
			}
		}
		if e = c.Publish("offline/state", []byte("synced"), 0, false, 0); e != nil {
			t.Fatal(e)
		}
		select {
		case <-mock.publications:
		case <-ctx.Done():
			t.Fatal("publication did not reach real TCP peer")
		}
		if len(native.SubscribedTopics()) != subscriptionBudget {
			t.Fatal("real native history budget")
		}
		if e = c.Subscribe("offline/command", func(string, []byte) {}); e == nil {
			t.Fatal("quota not enforced")
		}
	}
	if c.lifecycle.starts != 3 || c.lifecycle.generation != 3 {
		t.Fatal("workers/generations changed")
	}
	t.Log("OFFLINE real official MQTT Client -> first-party public owner -> official TCP stack; 3 healthy-link quota retirements, resubscription and state publication, stale old streams quiet")
}
