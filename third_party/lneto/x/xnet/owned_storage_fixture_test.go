//go:build native_storage_fixture

package xnet

import (
	"context"
	"github.com/soypat/lneto"
	"github.com/soypat/lneto/tcp"
	"net"
	"net/netip"
	"runtime"
	"testing"
	"time"
)

func TestSocketOwnsStableNativeBackings(t *testing.T) {
	factory := fixtureSocketOwner()
	var first [4]any
	var retained []*tcp.Conn
	for i := 0; i < 8; i++ {
		client, server, _, sc := newTCPStacks(t, 5678, 1500)
		backoff := lneto.BackoffStrategy(func(uint) time.Duration { return time.Millisecond })
		sg := client.StackGo(backoff, StackGoConfig{ListenerPoolConfig: TCPPoolConfig{PoolSize: 2, QueueSize: 4, TxBufSize: 4096, RxBufSize: 1024, NewBackoff: func() lneto.BackoffStrategy { return backoff }}})
		if e := server.ListenTCP4(sc, 8080); e != nil {
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
		ctx, release := factory(context.Background())
		raw, e := sg.Socket(ctx, "tcp4", AF_INET, SOCK_STREAM, nil, net.TCPAddrFromAddrPort(netip.AddrPortFrom(netip.AddrFrom4(server.Addr4()), 8080)))
		close(stop)
		<-done
		if e != nil {
			t.Fatal(e)
		}
		c, ok := raw.(tcpconn)
		if !ok {
			t.Fatalf("not real active Socket: %T", raw)
		}
		backing, caps := c.Conn.FixtureBackings()
		if caps != [4]int{4096, 1024, 4, 8} {
			t.Fatal("wrong native geometry", caps)
		}
		if i == 0 {
			first = backing
		} else if first != backing {
			t.Error("STABLE_NATIVE_BACKINGS_REQUIREMENT: actual Socket/Configure renewed TX/RX/queue/reassembly")
		}
		for _, old := range retained {
			if old == c.Conn {
				t.Fatal("Conn reused")
			}
		}
		retained = append(retained, c.Conn)
		runtime.GC()
		if e = release(client, c.Conn); e != nil {
			t.Fatal(e)
		}
	}
	runtime.KeepAlive(retained)
	t.Log("REACHED real Socket/Configure + TCP packet handshake, native TX4096 RX1024 queue4 reassembly8; fresh Conn, GC forced")
}
