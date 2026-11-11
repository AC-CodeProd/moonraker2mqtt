//go:build native_storage_fixture

package xnet

import (
	"context"
	"github.com/soypat/lneto"
	"github.com/soypat/lneto/internal/tinygotcpfixture"
	"github.com/soypat/lneto/tcp"
	"net"
	"net/netip"
	"runtime"
	"testing"
	"time"
)

func TestNativeDialOwnerTinyGoCapabilityRoute(t *testing.T) {
	client, server, _, sc := newTCPStacksTwoPorts(t, 5678, 1500)
	// Two production TCP ports: MQTT control plus one owned Moonraker stream.
	backoff := lneto.BackoffStrategy(func(uint) time.Duration { return time.Millisecond })
	sg := client.StackGo(backoff, StackGoConfig{ListenerPoolConfig: TCPPoolConfig{PoolSize: 2, QueueSize: 4, TxBufSize: 4096, RxBufSize: 1024, NewBackoff: func() lneto.BackoffStrategy { return backoff }}})
	if e := server.ListenTCP4(sc, 8080); e != nil {
		t.Fatal(e)
	}
	mqttServer := new(tcp.Conn)
	if e := mqttServer.Configure(tcp.ConnConfig{TxBuf: make([]byte, 4096), RxBuf: make([]byte, 1024), TxPacketQueueSize: 4, RWBackoff: backoff}); e != nil {
		t.Fatal(e)
	}
	if e := server.ListenTCP4(mqttServer, 1883); e != nil {
		t.Fatal(e)
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		b := make([]byte, 1600)
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, _ := client.EgressEthernet(b)
			if n > 0 {
				_ = server.IngressEthernet(b[:n])
			}
			n, _ = server.EgressEthernet(b)
			if n > 0 {
				_ = client.IngressEthernet(b[:n])
			}
			time.Sleep(time.Millisecond)
		}
	}()
	defer func() { close(stop); <-done }()
	broker := NewBerkeleyStack(sg.Socket)
	mfd, e := broker.Socket(AF_INET, SOCK_STREAM, 6)
	if e != nil {
		t.Fatal(e)
	}
	if e = broker.Connect(mfd, "", netip.AddrPortFrom(netip.AddrFrom4(server.Addr4()), 1883)); e != nil {
		t.Fatal(e)
	}
	mqtt := broker.getConn(mfd).sock.(tcpconn).Conn
	mqttGeneration := mqtt.Generation()
	owner := NewNativeDialOwner(sg)
	var old *tinygotcpfixture.TCPConn
	var oldInterrupt func() error
	var first [4]any
	remote := net.TCPAddrFromAddrPort(netip.AddrPortFrom(netip.AddrFrom4(server.Addr4()), 8080))
	for i := 0; i < 8; i++ {
		if i > 0 {
			server.mu.Lock()
			sc.Abort()
			server.mu.Unlock()
			if e = server.ListenTCP4(sc, 8080); e != nil {
				t.Fatal(e)
			}
		}
		stream, err := owner.DialContext(context.Background(), "tcp", remote)
		if err != nil {
			t.Fatal(err)
		}
		raw := tinygotcpfixture.BindOwned(stream)
		c := owner.attempt.conn
		backing, caps := c.FixtureBackings()
		if caps != [4]int{4096, 1024, 4, 8} {
			t.Fatal(caps)
		}
		if i == 0 {
			first = backing
		} else if backing != first {
			t.Fatal("owned native route allocated new backings")
		}
		if stream.fd != 3 {
			t.Fatal("private fd namespace changed")
		}
		interrupt, err := stream.GenerationInterrupt()
		if err != nil {
			t.Fatal(err)
		}
		if old != nil {
			if _, err = old.Write([]byte("stale")); err == nil {
				t.Fatal("old private fd affected new stream")
			}
			if err = oldInterrupt(); err == nil {
				t.Fatal("old cancel capability affected new stream")
			}
		}
		// The concrete TinyGo methods route through the capability's private real
		// Berkeley instance, not the shared mutable netdev fd namespace.
		expired := time.Now().Add(-time.Hour)
		_ = c.SetDeadline(expired)
		_ = raw.SetDeadline(time.Time{})
		_, _ = raw.Read(nil)
		_, err = raw.Write(nil)
		r, w := c.FixtureDeadlines()
		if !r.IsZero() || !w.IsZero() || err != nil {
			t.Fatal("owned TinyGo/Berkeley clearing failed", r, w, err)
		}
		runtime.GC()
		if err = interrupt(); err != nil {
			t.Fatal(err)
		}
		_ = raw.Close()
		if err = owner.Retire(); err != nil {
			t.Fatal(err)
		}
		if mqtt.Generation() != mqttGeneration || mqtt.State() != tcp.StateEstablished {
			t.Fatal("unrelated MQTT native connection changed")
		}
		if _, err = broker.Send(mfd, nil, 0, time.Time{}); err != nil {
			t.Fatal("MQTT native send broken", err)
		}
		old, oldInterrupt = raw, interrupt
	}
	// This is concurrent native TCP isolation; MQTT protocol regression is a
	// separate real app suite, not fabricated from this TCP-only control.
	t.Log("REACHED TinyGo capability projection -> private Berkeley(fd3 each generation) -> real Socket/Configure/Conn; stale fd/cancel harmless, MQTT TCP simultaneous")
}
