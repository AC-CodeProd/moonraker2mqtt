package nativetcp

import (
	"context"
	"github.com/soypat/lneto/tcp"
	"github.com/soypat/lneto/x/xnet"
	"io"
	"net"
	"net/netip"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

type publicFixture struct {
	a, b       PublicStack
	peers      [2]tcp.Conn
	stop, done chan struct{}
	paused     atomic.Bool
}

func fixture(t *testing.T) *publicFixture {
	t.Helper()
	f := &publicFixture{stop: make(chan struct{}), done: make(chan struct{})}
	f.a.Stack = new(xnet.StackAsync)
	f.b.Stack = new(xnet.StackAsync)
	for i, s := range []*xnet.StackAsync{f.a.Stack, f.b.Stack} {
		if e := s.Reset(xnet.StackConfig{Hostname: "offline", RandSeed: int64(i + 1), StaticAddress4: [4]byte{192, 0, 2, byte(i + 1)}, HardwareAddress: [6]byte{2, 0, 0, 0, 0, byte(i + 1)}, MaxActiveTCPPorts: 2, MTU: 1500}); e != nil {
			t.Fatal(e)
		}
	}
	f.a.Stack.SetGatewayHardwareAddr(f.b.Stack.HardwareAddr())
	f.b.Stack.SetGatewayHardwareAddr(f.a.Stack.HardwareAddr())
	for i := range f.peers {
		if e := f.peers[i].Configure(tcp.ConnConfig{TxBuf: make([]byte, 4096), RxBuf: make([]byte, 1024), TxPacketQueueSize: 4, RWBackoff: func(uint) time.Duration { return 5 * time.Millisecond }}); e != nil {
			t.Fatal(e)
		}
	}
	go func() {
		defer close(f.done)
		var buf [1600]byte
		for {
			select {
			case <-f.stop:
				return
			default:
			}
			if !f.paused.Load() {
				var n int
				f.a.Pump(func() { n, _ = f.a.Stack.EgressEthernet(buf[:]) })
				if n > 0 {
					f.b.Pump(func() { _ = f.b.Stack.IngressEthernet(buf[:n]) })
				}
				f.b.Pump(func() { n, _ = f.b.Stack.EgressEthernet(buf[:]) })
				if n > 0 {
					f.a.Pump(func() { _ = f.a.Stack.IngressEthernet(buf[:n]) })
				}
			}
			time.Sleep(time.Millisecond)
		}
	}()
	t.Cleanup(func() {
		close(f.stop)
		<-f.done
		for i := range f.peers {
			f.peers[i].Abort()
		}
	})
	return f
}
func (f *publicFixture) dial(t *testing.T, o *PublicOwner, i int) net.Conn {
	t.Helper()
	var err error
	f.b.Pump(func() { f.peers[i].Abort(); err = f.b.Stack.ListenTCP4(&f.peers[i], uint16(8080+i)) })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, e := o.DialAddr(ctx, netip.AddrPortFrom(netip.MustParseAddr("192.0.2.2"), uint16(8080+i)))
	if e != nil {
		t.Fatal(e)
	}
	// Client establishment can precede delivery of its final ACK to the peer.
	// A raw tcp.Conn (unlike Accept) returns EOF if read in SYN-RCVD. Wait for
	// the real peer handshake within the original one-second dial deadline.
	for f.peers[i].State() != tcp.StateEstablished {
		if ctx.Err() != nil {
			t.Fatal("peer handshake not completed", f.peers[i].State(), ctx.Err())
		}
		time.Sleep(time.Millisecond)
	}
	return c
}
func TestPublicOwnerReuseJoinsAndTwoConnections(t *testing.T) {
	f := fixture(t)
	o, e := f.a.NewOwner()
	if e != nil {
		t.Fatal(e)
	}
	other, e := f.a.NewOwner()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { o.Retire(); other.Retire() })
	keep := f.dial(t, other, 1)
	tx, rx, native := &o.tx[0], &o.rx[0], &o.conn
	var old net.Conn
	for i := 0; i < 32; i++ {
		c := f.dial(t, o, 0)
		if old != nil {
			if _, e = old.Write([]byte("stale")); e != net.ErrClosed {
				t.Fatal("stale write", e)
			}
			_ = old.SetDeadline(time.Now().Add(-time.Second))
			_ = old.Close()
		}
		if _, e = c.Write([]byte("ok")); e != nil {
			t.Fatal(e)
		}
		var got [2]byte
		_ = f.peers[0].SetReadDeadline(time.Now().Add(time.Second))
		if _, e = io.ReadFull(&f.peers[0], got[:]); e != nil || string(got[:]) != "ok" {
			t.Fatal("new generation contaminated", string(got[:]), e)
		}
		if _, e = keep.Write([]byte("on")); e != nil {
			t.Fatal("other connection lost", e)
		}
		_ = f.peers[1].SetReadDeadline(time.Now().Add(time.Second))
		if _, e = io.ReadFull(&f.peers[1], got[:]); e != nil || string(got[:]) != "on" {
			t.Fatal("other peer lost", e)
		}
		f.paused.Store(true)
		done := make(chan error, 1)
		go func() { _, e := c.Write(make([]byte, 4097)); done <- e }()
		until := time.Now().Add(time.Second)
		for o.conn.FreeOutput() != 0 && time.Now().Before(until) {
			runtime.Gosched()
		}
		if o.conn.FreeOutput() != 0 {
			t.Fatal("saturation not reached")
		}
		if e = o.Retire(); e != nil {
			t.Fatal(e)
		}
		select {
		case e = <-done:
			if e == nil {
				t.Fatal("blocked writer unexpectedly succeeded")
			}
		case <-time.After(time.Second):
			t.Fatal("writer not joined")
		}
		if tx != &o.tx[0] || rx != &o.rx[0] || native != &o.conn {
			t.Fatal("native backing renewed")
		}
		f.paused.Store(false)
		old = c
	}
	t.Log("32 TCP generations; TX4096/RX1024/Q4 retained; saturated writers joined; stale setters/close/write rejected; second connection remains live")
}

func TestPublicOwnerCancelAndPumpDrain(t *testing.T) {
	f := fixture(t)
	o, e := f.a.NewOwner()
	if e != nil {
		t.Fatal(e)
	}
	c := f.dial(t, o, 0)
	entered, release := make(chan struct{}), make(chan struct{})
	callbackDone := make(chan struct{})
	go func() { f.a.Pump(func() { close(entered); <-release }); close(callbackDone) }()
	<-entered
	retired := make(chan error, 1)
	go func() { retired <- o.Retire() }()
	select {
	case <-retired:
		t.Fatal("retirement overtook callback")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	<-callbackDone
	select {
	case e = <-retired:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("retirement not joined")
	}
	if _, e = c.Read(make([]byte, 1)); e != net.ErrClosed {
		t.Fatal("old read admitted", e)
	}
	f.paused.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = o.DialAddr(ctx, netip.MustParseAddrPort("192.0.2.2:8080")); e != context.Canceled {
		t.Fatal(e)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, e = o.DialAddr(ctx, netip.MustParseAddrPort("192.0.2.2:8080")); e != context.DeadlineExceeded {
		t.Fatal(e)
	}
	f.paused.Store(false)
	_ = f.dial(t, o, 0)
	_ = o.Retire()
}
