//go:build native_storage_fixture && !tinygo

package websocket

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"github.com/soypat/lneto"
	"github.com/soypat/lneto/tcp"
	"github.com/soypat/lneto/x/xnet"

	"net"
	"net/http"
	"net/netip"
	"runtime"
	"testing"
	"time"
)

func TestNativeSocketThroughBoundedWebSocketHTTP(t *testing.T) {
	client, server := new(xnet.StackAsync), new(xnet.StackAsync)
	for i, s := range []*xnet.StackAsync{client, server} {
		if e := s.Reset(xnet.StackConfig{Hostname: "private-native-fixture", RandSeed: int64(5678 + i), StaticAddress4: [4]byte{10, 0, 0, byte(i + 1)}, HardwareAddress: [6]byte{0xbe, 0xef, 0, 0, 0, byte(i + 1)}, MaxActiveTCPPorts: 2, MTU: 1500}); e != nil {
			t.Fatal(e)
		}
	}
	client.SetGatewayHardwareAddr(server.HardwareAddr())
	server.SetGatewayHardwareAddr(client.HardwareAddr())
	backoff := lneto.BackoffStrategy(func(uint) time.Duration { return time.Millisecond })
	sg := client.StackGo(backoff, xnet.StackGoConfig{ListenerPoolConfig: xnet.TCPPoolConfig{PoolSize: 2, QueueSize: 4, TxBufSize: 4096, RxBufSize: 1024, NewBackoff: func() lneto.BackoffStrategy { return backoff }}})
	owner := xnet.NewNativeDialOwner(sg)
	remote := net.TCPAddrFromAddrPort(netip.MustParseAddrPort("10.0.0.2:8080"))
	stop, pumpDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(pumpDone)
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
	defer func() { close(stop); <-pumpDone }()
	var buffers DialBuffers
	buffers.Init()
	pair := buffers.pair
	seen := map[string]bool{}
	var first [4]any
	var prior *tcp.Conn
	for i := 0; i < 8; i++ {
		sc := new(tcp.Conn)
		if e := sc.Configure(tcp.ConnConfig{TxBuf: make([]byte, 4096), RxBuf: make([]byte, 1024), TxPacketQueueSize: 4, RWBackoff: backoff}); e != nil {
			t.Fatal(e)
		}
		if e := server.ListenTCP4(sc, 8080); e != nil {
			t.Fatal(e)
		}
		serverDone := make(chan error, 1)
		finish := make(chan struct{})
		bad := i%2 == 1
		go func() {
			bound := time.Now().Add(time.Second)
			for sc.State() != tcp.StateEstablished {
				if time.Now().After(bound) {
					serverDone <- fmt.Errorf("server TCP establish bound")
					return
				}
				time.Sleep(time.Millisecond)
			}
			req, e := http.ReadRequest(bufio.NewReader(sc))
			if e != nil {
				serverDone <- e
				return
			}
			key := req.Header.Get("Sec-Websocket-Key")
			if key == "" || seen[key] {
				serverDone <- fmt.Errorf("nonce missing/reused")
				return
			}
			seen[key] = true
			if bad {
				_, e = sc.Write([]byte("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n"))
			} else {
				sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
				response := "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n"
				response += string([]byte{0x81, 5}) + "hello" + string([]byte{0x81, 5}) + "world"
				_, e = sc.Write([]byte(response))
			}
			<-finish
			serverDone <- e
		}()
		cfg, e := NewConfig("ws://private-native.invalid:8080/websocket", "http://private-native.invalid:8080")
		if e != nil {
			t.Fatal(e)
		}
		cfg.OwnedDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			stream, e := owner.DialContext(ctx, network, remote)
			if e != nil {
				return nil, e
			}
			return xnet.BindStream(stream, remote), nil
		}
		ws, e := cfg.DialBoundedWithBuffers(context.Background(), time.Second, func(net.Conn) {}, &buffers)
		native, token, backings, caps := owner.FixtureOwnerSnapshot()
		if native == prior || token != uint64(i+1) || caps != [4]int{4096, 1024, 4, 8} {
			t.Fatal("native identity/token/geometry changed incorrectly", token, caps)
		}
		if i == 0 {
			first = backings
		} else if first != backings {
			t.Fatal("real TCP -> TinyGo -> WS -> HTTP route renewed native backing")
		}
		if bad {
			if e == nil {
				t.Fatal("HTTP403 accepted")
			}
		} else {
			if e != nil {
				t.Fatal(e)
			}
			for _, want := range []string{"hello", "world"} {
				var got string
				if e = Message.Receive(ws, &got); e != nil || got != want {
					t.Fatal("101 prefetch/multiframe lost", got, e)
				}
			}
			_ = ws.AbortTransport()
		}
		close(finish)
		if e = <-serverDone; e != nil {
			t.Fatal(e)
		}
		runtime.GC()
		if e = owner.Retire(); e != nil {
			t.Fatal(e)
		}
		buffers.Release()
		if buffers.pair != pair {
			t.Fatal("bufio pair reallocated")
		}
		server.FixtureAbortTCP(sc)
		prior = native
	}
	t.Log("REACHED bounded WebSocket real TCP Socket/Configure -> verbatim TinyGo capability methods -> real Berkeley -> HTTP101/403, nonce, prefetched frames, GC, stable native/bufio ownership")
}
