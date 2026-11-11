package network

import (
	"context"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/soypat/lneto"
	"github.com/soypat/lneto/x/xnet"
)

// Real pinned lneto TCP/ARP/IP and Berkeley descriptors; Ethernet frames pass
// only between memory stacks. No OS sockets, radio, printer, or broker access.
func TestBridgeTwoSockets(t *testing.T) {
	var client, server xnet.StackAsync
	cip, sip := [4]byte{192, 0, 2, 1}, [4]byte{192, 0, 2, 2}
	cmac, smac := [6]byte{2, 0, 0, 0, 0, 1}, [6]byte{2, 0, 0, 0, 0, 2}
	for _, s := range []struct {
		stack *xnet.StackAsync
		ip    [4]byte
		mac   [6]byte
	}{{&client, cip, cmac}, {&server, sip, smac}} {
		ports := uint16(TCPPorts)
		if s.stack == &server {
			ports = 2 // Simulated peers are not limited by candidate client capacity.
		}
		if err := s.stack.Reset(xnet.StackConfig{Hostname: "memory-test", MTU: 1500, StaticAddress4: s.ip, HardwareAddress: s.mac, MaxActiveTCPPorts: ports, MaxActiveUDPPorts: UDPPorts, PassivePeers: PassivePeers, RandSeed: 17}); err != nil {
			t.Fatal(err)
		}
		s.stack.SetSubnet4(s.ip, 24)
	}
	client.SetGatewayHardwareAddr(smac)
	server.SetGatewayHardwareAddr(cmac)
	cg, sg := GoStack(&client), GoStack(&server)
	var listeners []net.Listener
	for _, port := range []uint16{1883, 7125} {
		sock, err := sg.SocketNetip(context.Background(), "tcp4", xnet.AF_INET, xnet.SOCK_STREAM, netip.AddrPortFrom(netip.AddrFrom4(sip), port), netip.AddrPort{})
		if err != nil {
			t.Fatal(err)
		}
		listener, ok := sock.(net.Listener)
		if !ok {
			t.Fatal("not a listener")
		}
		listeners = append(listeners, listener)
		defer listener.Close()
	}
	ctx, cancel := context.WithCancel(context.Background())
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		buf := make([]byte, 1536)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			for _, pair := range [][2]*xnet.StackAsync{{&client, &server}, {&server, &client}} {
				n, _ := pair[0].EgressEthernet(buf)
				if n > 0 {
					_ = pair[1].IngressEthernet(buf[:n])
				}
			}
		}
	}()
	defer func() { cancel(); <-pumpDone }()
	peers := make(chan net.Conn, 2)
	acceptErr := make(chan error, 2)
	for _, l := range listeners {
		go func(l net.Listener) {
			c, err := l.Accept()
			if err != nil {
				acceptErr <- err
				return
			}
			peers <- c
		}(l)
	}
	b := xnet.NewBerkeleyStack(cg.Socket)
	fds := make([]int, 0, 2)
	for _, port := range []uint16{1883, 7125} {
		fd, err := b.Socket(xnet.AF_INET, xnet.SOCK_STREAM, int(lneto.IPProtoTCP))
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close(fd)
		if err := b.Connect(fd, "", netip.AddrPortFrom(netip.AddrFrom4(sip), port)); err != nil {
			t.Fatalf("second simultaneous MQTT/Moonraker socket unavailable (capacity=%d port=%d): %v", TCPPorts, port, err)
		}
		fds = append(fds, fd)
	}
	remote := make([]net.Conn, 0, 2)
	for len(remote) < 2 {
		select {
		case c := <-peers:
			remote = append(remote, c)
			defer c.Close()
		case err := <-acceptErr:
			t.Fatal(err)
		case <-time.After(3 * time.Second):
			t.Fatal("peer accept timeout")
		}
	}
	for i, fd := range fds {
		want := []byte{byte('A' + i), byte('a' + i)}
		if n, err := b.Send(fd, want, 0, time.Now().Add(time.Second)); err != nil || n != len(want) {
			t.Fatal(n, err)
		}
	}
	// Both remain established while reading their distinct packets; no close-to-
	// free-slot trick. The server's accept order is irrelevant (use local port).
	for _, c := range remote {
		c.SetDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 2)
		if _, err := io.ReadFull(c, buf); err != nil {
			t.Fatal(err)
		}
		port := c.LocalAddr().(*net.TCPAddr).Port
		want := byte('A')
		if port == 7125 {
			want = 'B'
		}
		if buf[0] != want {
			t.Fatal("cross-socket data", port, buf)
		}
		if _, err := c.Write(buf); err != nil {
			t.Fatal(err)
		}
	}
	for i, fd := range fds {
		buf := make([]byte, 2)
		n, err := b.Recv(fd, buf, 0, time.Now().Add(time.Second))
		if err != nil || n != 2 || buf[0] != byte('A'+i) {
			t.Fatal("echo", i, n, err, buf)
		}
	}
	t.Log("two simultaneous Berkeley sockets established and exchanged distinct payloads via in-memory Ethernet")
}
