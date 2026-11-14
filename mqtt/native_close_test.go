//go:build natiu && !tinygo

package mqtt

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/soypat/lneto/tcp"
	"github.com/soypat/lneto/x/xnet"
	"moonraker2mqtt/config"
	"moonraker2mqtt/internal/nativetcp"
	"moonraker2mqtt/logger"
)

// The gate stops the real pump after the final MQTT bytes have entered the
// native TX ring. Close must resume it and wait for TCP ACK, not just Write.
// Embedding net.Conn deliberately does not promote the optional capability.
type nativeFinalConn struct {
	net.Conn
	paused    *atomic.Bool
	final     chan struct{}
	once      sync.Once
	cap       time.Duration
	primary   error
	stack     *nativetcp.PublicStack
	closing   chan struct{}
	closeOnce sync.Once
	setErr    error
}

func (c *nativeFinalConn) GenerationInterrupt() (func() error, error) {
	return c.Conn.(interface{ GenerationInterrupt() (func() error, error) }).GenerationInterrupt()
}
func (c *nativeFinalConn) CloseGracefully() error {
	c.closeOnce.Do(func() { c.setErr = c.SetWriteDeadline(time.Now().Add(c.cap)); close(c.closing) })
	if c.setErr != nil {
		return c.setErr
	}
	return c.Conn.(interface{ CloseGracefully() error }).CloseGracefully()
}
func (c *nativeFinalConn) Close() error {
	c.closeOnce.Do(func() { close(c.closing) })
	return c.Conn.Close()
}
func (c *nativeFinalConn) Write(p []byte) (int, error) {
	final := len(p) == 2 && p[0] == 0xe0
	if final {
		c.paused.Store(true)
		c.stack.Pump(func() {})
	}
	n, e := c.Conn.Write(p)
	if final {

		c.once.Do(func() { close(c.final) })
		if c.primary != nil {
			return n, c.primary
		}
	}
	return n, e
}

func TestNativeMQTTDisconnectDrainsAndReportsFailure(t *testing.T) {
	for _, mode := range []string{"acknowledged", "withheldACK", "writePrimary"} {
		t.Run(mode, func(t *testing.T) {
			var a, b nativetcp.PublicStack
			a.Stack = new(xnet.StackAsync)
			b.Stack = new(xnet.StackAsync)
			for i, s := range []*xnet.StackAsync{a.Stack, b.Stack} {
				if e := s.Reset(xnet.StackConfig{Hostname: "offline", RandSeed: int64(i + 1), StaticAddress4: [4]byte{192, 0, 2, byte(i + 1)}, HardwareAddress: [6]byte{2, 0, 0, 0, 0, byte(i + 1)}, MaxActiveTCPPorts: 2, MTU: 1500}); e != nil {
					t.Fatal(e)
				}
			}
			a.Stack.SetGatewayHardwareAddr(b.Stack.HardwareAddr())
			b.Stack.SetGatewayHardwareAddr(a.Stack.HardwareAddr())
			var peer tcp.Conn
			if e := peer.Configure(tcp.ConnConfig{TxBuf: make([]byte, 4096), RxBuf: make([]byte, 1024), TxPacketQueueSize: 4, RWBackoff: func(uint) time.Duration { return 5 * time.Millisecond }}); e != nil {
				t.Fatal(e)
			}
			if e := b.Stack.ListenTCP4(&peer, 1883); e != nil {
				t.Fatal(e)
			}
			var paused, drop atomic.Bool
			stop, pumped := make(chan struct{}), make(chan struct{})
			pumpErr := make(chan error, 1)
			record := func(e error) {
				if e != nil {
					select {
					case pumpErr <- e:
					default:
					}
				}
			}
			go func() {
				defer close(pumped)
				var buf [1600]byte
				for {
					select {
					case <-stop:
						return
					default:
					}
					if !paused.Load() {
						var n int
						var e error
						a.Pump(func() {
							if !paused.Load() {
								n, e = a.Stack.EgressEthernet(buf[:])
							}
						})
						record(e)
						if n > 0 {
							b.Pump(func() { e = b.Stack.IngressEthernet(buf[:n]) })
							record(e)
						}
						b.Pump(func() { n, e = b.Stack.EgressEthernet(buf[:]) })
						record(e)
						if n > 0 && !drop.Load() {
							a.Pump(func() { e = a.Stack.IngressEthernet(buf[:n]) })
							record(e)
						}
					}
					time.Sleep(time.Millisecond)
				}
			}()
			o, e := a.NewOwner()
			if e != nil {
				close(stop)
				<-pumped
				t.Fatal(e)
			}
			t.Cleanup(func() {
				if e := o.Retire(); e != nil {
					t.Error(e)
				}
				close(stop)
				<-pumped
				peer.Abort()
				select {
				case e := <-pumpErr:
					t.Errorf("native pump: %v", e)
				default:
				}
			})
			cfg := config.DefaultConfig().MQTT
			c := NewNatiuClient(cfg, logger.NewSerial(io.Discard, logger.ERROR))
			var wrapped *nativeFinalConn
			primary := errors.New("final write primary")
			c.dial = func(string, string) (net.Conn, error) {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				raw, e := o.DialAddr(ctx, netip.MustParseAddrPort("192.0.2.2:1883"))
				if e != nil {
					return nil, e
				}
				for peer.State() != tcp.StateEstablished {
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					time.Sleep(time.Millisecond)
				}
				wrapped = &nativeFinalConn{Conn: raw, paused: &paused, final: make(chan struct{}), closing: make(chan struct{}), stack: &a, cap: 150 * time.Millisecond}
				if mode == "writePrimary" {
					wrapped.primary = primary
				}
				return wrapped, nil
			}
			peerDone := make(chan error, 1)
			got := make(chan wirePacket, 1)
			go func() {
				if e := peer.SetReadDeadline(time.Now().Add(4 * time.Second)); e != nil {
					peerDone <- e
					return
				}
				// Raw peer has the same Established precondition as the client fixture.
				until := time.Now().Add(time.Second)
				for peer.State() != tcp.StateEstablished && time.Now().Before(until) {
					time.Sleep(time.Millisecond)
				}
				r := bufio.NewReader(&peer)
				if _, e := readPacket(r); e != nil {
					peerDone <- e
					return
				}
				if n, e := peer.Write([]byte{0x20, 2, 0, 0}); e != nil || n != 4 {
					peerDone <- fmt.Errorf("CONNACK n=%d: %w", n, e)
					return
				}
				if mode == "writePrimary" {
					peerDone <- nil
					return
				}
				p, e := readPacket(r)
				if e == nil {
					got <- p
				}
				peerDone <- e
			}()
			// Always unblock and join both workers, including Fatal paths.
			disconnected := make(chan error, 1)
			started := false
			t.Cleanup(func() {
				paused.Store(false)
				drop.Store(false)
				if e := o.Retire(); e != nil {
					t.Error(e)
				}
				if started {
					<-disconnected
				} else {
					_ = c.Disconnect()
				}
				peer.Abort()
				if e := <-peerDone; e != nil {
					t.Errorf("peer: %v", e)
				}
			})
			if e = c.Connect(); e != nil {
				t.Fatal(e)
			}
			go func() { disconnected <- c.Disconnect() }()
			started = true
			select {
			case <-wrapped.final:
			case <-time.After(3 * time.Second):
				t.Fatal("final DISCONNECT not written")
			}
			// A final-write failure intentionally takes the destructive error
			// route; it must preserve the write error, not promise delivery.
			if mode == "writePrimary" {
				select {
				case e := <-disconnected:
					disconnected <- e
					if !errors.Is(e, primary) {
						t.Fatalf("write error replaced: %v", e)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("error disconnect did not finish")
				}
				return
			}
			select {
			case <-wrapped.closing:
			case <-time.After(3 * time.Second):
				t.Fatal("native close not reached")
			}
			select {
			case e := <-disconnected:
				disconnected <- e
				t.Fatalf("Close completed before final bytes left native ring: %v", e)
			case <-time.After(20 * time.Millisecond):
			}
			if mode != "acknowledged" {
				drop.Store(true)
			}
			paused.Store(false)
			select {
			case p := <-got:
				if p.header != 0xe0 || len(p.data) != 0 {
					t.Fatal("wrong DISCONNECT", p)
				}
			case <-time.After(time.Second):
				t.Fatal("peer missed DISCONNECT")
			}
			select {
			case e := <-disconnected:
				disconnected <- e
				switch mode {
				case "acknowledged":
					if e != nil {
						t.Fatal(e)
					}
				case "withheldACK":
					if !errors.Is(e, os.ErrDeadlineExceeded) {
						t.Fatalf("drain failure hidden: %v", e)
					}

				}
			case <-time.After(time.Second):
				t.Fatal("Disconnect did not finish")
			}
		})
	}
}
