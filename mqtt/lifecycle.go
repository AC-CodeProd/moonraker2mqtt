//go:build tinygo || natiu

package mqtt

import (
	"context"
	"errors"
	native "github.com/soypat/natiu-mqtt"
	"net"
	"sync"
)

// mqttLifecycle is retained by the client, never by a per-attempt Config or a
// GC-evictable pool. Queue geometry and decoder limit are unchanged.
type mqttLifecycle struct {
	serial                              sync.Mutex // Connect/Disconnect only, never acquired by Publish
	native                              mqttNativeOwner
	nativeErr                           error
	decoder                             []byte
	started, closed                     bool
	generation                          uint64 // saturates by rejecting another attempt at MaxUint64
	starts                              uint8  // stable worker count; never incremented on reconnect
	active                              *mqttGeneration
	rxStart, dispatchStart, cancelStart chan *mqttGeneration
	rxExit, dispatchExit, cancelExit    chan struct{}
}
type mqttNativeOwner interface {
	DialContext(context.Context, string, string) (net.Conn, error)
	Retire() error
}
type mqttGeneration struct {
	ctx                                              context.Context
	wire                                             *packetConn
	client                                           *native.Client
	stop, rxDone, dispatchDone, cancelDone, dialDone chan struct{}
	raw                                              chan *packetConn
	once                                             sync.Once
	running                                          bool
}

func (g *mqttGeneration) stopNow() { g.once.Do(func() { close(g.stop) }) }
func (c *NatiuClient) initMQTT() error {
	l := &c.lifecycle
	if l.started {
		return l.nativeErr
	}
	l.native, l.nativeErr = newMQTTNativeOwner()
	if l.nativeErr != nil {
		return l.nativeErr
	}
	l.decoder = make([]byte, MaxPayload+256)
	c.queue = make(chan incoming, 4)
	c.subacks = make(chan subscriptionAck, 1)
	l.rxStart = make(chan *mqttGeneration)
	l.dispatchStart = make(chan *mqttGeneration)
	l.cancelStart = make(chan *mqttGeneration)
	l.rxExit = make(chan struct{})
	l.dispatchExit = make(chan struct{})
	l.cancelExit = make(chan struct{})
	l.started = true
	l.starts = 3
	go c.mqttRXWorker()
	go c.mqttDispatchWorker()
	go c.mqttCancellationWorker()
	return nil
}
func (c *NatiuClient) mqttRXWorker() {
	defer close(c.lifecycle.rxExit)
	for g := range c.lifecycle.rxStart {
		c.readLoop(g.client, g.wire, c.queue, g.rxDone)
		// Native Disconnect marks shutdown before its final wire write. Do not
		// wake cancellation/Close until that caller has finished the packet.
		// Terminal Disconnect retires and joins this generation immediately after.
		if !errors.Is(g.client.Err(), errShutdown) {
			g.stopNow()
		}
	}
}
func (c *NatiuClient) mqttDispatchWorker() {
	defer close(c.lifecycle.dispatchExit)
	for g := range c.lifecycle.dispatchStart {
		c.dispatch(c.queue, g.stop)
		close(g.dispatchDone)
	}
}
func (c *NatiuClient) mqttCancellationWorker() {
	defer close(c.lifecycle.cancelExit)
	for g := range c.lifecycle.cancelStart {
		select {
		case <-g.stop:
		case <-g.ctx.Done():
			g.stopNow()
		}
		// Raw publication follows dial. If stop precedes publication, the caller
		// still owns the handshake and closes it before closing the done barrier.
		<-g.dialDone
		select {
		case wire := <-g.raw:
			_ = wire.closeCause(8)
		default:
		}
		close(g.cancelDone)
	}
}
func (c *NatiuClient) failedConnect(g *mqttGeneration, err error) error {
	if e := c.retireMQTT(); e != nil {
		return e
	}
	return c.connectResult(err)
}

// ops excludes Publish/Subscribe/Connect before this barrier; the cancellation
// worker interrupts without ops or the packet write mutex. App workers finish
// before NativeDialOwner closes admissions, detaches callbacks/FD and drains
// native ingress/egress/lookup snapshots. Only then can storage be rebound.
func (c *NatiuClient) retireMQTT() error {
	l := &c.lifecycle
	g := l.active
	if g == nil {
		return l.nativeErr
	}
	g.stopNow()
	if g.wire != nil {
		_ = g.wire.closeCause(1)
	}
	// Revoke public use before joining dispatch. A command callback may wait
	// for MR whose notification callback synchronously calls Publish. Holding
	// ops across that join would create cross-protocol lock coupling.
	c.mu.Lock()
	c.client = nil
	c.conn = nil
	c.mu.Unlock()
	c.ops.Unlock()
	defer c.ops.Lock()
	<-g.cancelDone
	if g.running {
		<-g.rxDone
		<-g.dispatchDone
	}
	if l.native != nil {
		if err := l.native.Retire(); err != nil {
			l.nativeErr = err
			return err
		}
	}
	c.mu.Lock()
	c.client = nil
	c.conn = nil
	c.mu.Unlock()
	for {
		select {
		case <-c.queue:
		default:
			goto subacks
		}
	}
subacks:
	for {
		select {
		case <-c.subacks:
		default:
			l.active = nil
			return nil
		}
	}
}
