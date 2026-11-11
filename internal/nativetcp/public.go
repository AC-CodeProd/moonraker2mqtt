package nativetcp

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/soypat/lneto"
	"github.com/soypat/lneto/tcp"
	"github.com/soypat/lneto/x/xnet"
)

// PublicStack serializes retirement with the application's sole frame pump.
// Pump must cover every ingress/egress callback, not just Ethernet egress.
// StackAsync's own lock serializes registration with its callbacks; our gate
// additionally serializes public Abort (which only locks the Conn) with them.
// No upstream representation, unsafe access or private detach API is used.
type PublicStack struct {
	Stack *xnet.StackAsync
	gate  sync.Mutex
}

func (s *PublicStack) Pump(fn func()) { s.gate.Lock(); defer s.gate.Unlock(); fn() }

// PublicOwner retains one Conn and its buffers for the lifetime of a protocol.
// Old registered nodes retain the old ConnectionID, hence cannot call into a
// reopened Conn. Registrations are bounded by StackConfig.MaxActiveTCPPorts;
// public registration compacts invalid nodes when needed. Retire waits for the
// frame gate AND every admitted operation before another generation may dial.
type PublicOwner struct {
	stack      *PublicStack
	serial     sync.Mutex
	mu         sync.Mutex
	calls      sync.WaitGroup
	conn       tcp.Conn
	tx         [4096]byte
	rx         [1024]byte
	generation uint64
	active     bool
}

type publicConn struct {
	owner         *PublicOwner
	generation    uint64
	local, remote net.Addr
}

func (s *PublicStack) NewOwner() (*PublicOwner, error) {
	if s.Stack == nil {
		return nil, errors.New("native stack unavailable")
	}
	o := &PublicOwner{stack: s}
	err := o.conn.Configure(tcp.ConnConfig{TxBuf: o.tx[:], RxBuf: o.rx[:], TxPacketQueueSize: 4, RWBackoff: lneto.BackoffStrategy(func(uint) time.Duration { return 5 * time.Millisecond })})
	return o, err
}

func (o *PublicOwner) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	remote, e := netip.ParseAddrPort(address)
	if e != nil || !remote.Addr().Is4() || remote.Port() == 0 || (network != "tcp" && network != "tcp4") {
		return nil, errors.New("native TCP requires IPv4 address")
	}
	return o.DialAddr(ctx, remote)
}

func (o *PublicOwner) DialAddr(ctx context.Context, remote netip.AddrPort) (net.Conn, error) {
	if !remote.Addr().Is4() || remote.Port() == 0 {
		return nil, errors.New("native TCP requires IPv4 address")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	o.serial.Lock()
	defer o.serial.Unlock()
	o.mu.Lock()
	busy := o.active
	o.mu.Unlock()
	if busy {
		return nil, errors.New("native TCP generation not retired")
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	// A closed wrapper is not itself a drain receipt. Even callers which have
	// not explicitly retired must join all admitted operations before reuse.
	o.abort()
	o.calls.Wait()
	o.mu.Lock()
	if o.generation >= ^uint64(0)/4 {
		o.mu.Unlock()
		return nil, errors.New("native TCP generation exhausted")
	}
	o.generation++
	o.mu.Unlock()
	local := uint16(49152 + o.stack.Stack.Prand32()%16384)
	o.stack.gate.Lock()
	e := o.stack.Stack.DialTCP(&o.conn, local, remote)
	o.stack.gate.Unlock()
	if e != nil {
		o.abort()
		return nil, e
	}
	for o.conn.State() != tcp.StateEstablished {
		if e := ctx.Err(); e != nil {
			o.abort()
			return nil, e
		}
		if o.conn.State().IsClosed() && !o.conn.AwaitingSynSend() {
			o.abort()
			return nil, errors.New("native TCP connection failed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	o.mu.Lock()
	o.active = true
	o.mu.Unlock()
	return &publicConn{owner: o, generation: o.generation, local: net.TCPAddrFromAddrPort(netip.AddrPortFrom(netip.AddrFrom4(o.stack.Stack.Addr4()), local)), remote: net.TCPAddrFromAddrPort(remote)}, nil
}

func (o *PublicOwner) abort() { o.stack.gate.Lock(); o.conn.Abort(); o.stack.gate.Unlock() }
func (o *PublicOwner) Retire() error {
	o.serial.Lock()
	defer o.serial.Unlock()
	o.mu.Lock()
	o.active = false
	o.mu.Unlock()
	o.abort()
	o.calls.Wait()
	return nil
}
func (c *publicConn) enter() bool {
	o := c.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.active || c.generation != o.generation {
		return false
	}
	o.calls.Add(1)
	return true
}
func (c *publicConn) Read(p []byte) (int, error) {
	if !c.enter() {
		return 0, net.ErrClosed
	}
	defer c.owner.calls.Done()
	return c.owner.conn.Read(p)
}
func (c *publicConn) Write(p []byte) (int, error) {
	if !c.enter() {
		return 0, net.ErrClosed
	}
	defer c.owner.calls.Done()
	return c.owner.conn.Write(p)
}
func (c *publicConn) interrupt() error {
	o := c.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.active || c.generation != o.generation {
		return nil
	}
	o.active = false
	o.abort()
	return nil
}
func (c *publicConn) GenerationInterrupt() (func() error, error) {
	if !c.enter() {
		return nil, net.ErrClosed
	}
	c.owner.calls.Done()
	return c.interrupt, nil
}
func (c *publicConn) Close() error         { return c.interrupt() }
func (c *publicConn) LocalAddr() net.Addr  { return c.local }
func (c *publicConn) RemoteAddr() net.Addr { return c.remote }
func (c *publicConn) SetDeadline(t time.Time) error {
	if !c.enter() {
		return net.ErrClosed
	}
	defer c.owner.calls.Done()
	return c.owner.conn.SetDeadline(t)
}
func (c *publicConn) SetReadDeadline(t time.Time) error {
	if !c.enter() {
		return net.ErrClosed
	}
	defer c.owner.calls.Done()
	return c.owner.conn.SetReadDeadline(t)
}
func (c *publicConn) SetWriteDeadline(t time.Time) error {
	if !c.enter() {
		return net.ErrClosed
	}
	defer c.owner.calls.Done()
	return c.owner.conn.SetWriteDeadline(t)
}
