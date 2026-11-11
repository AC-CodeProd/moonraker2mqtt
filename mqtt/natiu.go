//go:build tinygo || natiu

package mqtt

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	native "github.com/soypat/natiu-mqtt"
	"moonraker2mqtt/config"
	"moonraker2mqtt/logger"
)

const MaxPayload = 4096
const maxSubscriptions = 4
const receiveIdleTimeout = 2 * time.Second
const receiveProgressTimeout = 2 * time.Second
const receivePacketTimeout = 10 * time.Second

type subscriptionAck struct {
	id   uint16
	code byte
}

var errShutdown = errors.New("application shutdown")

type incoming struct {
	topic   string
	payload []byte
}

// NatiuClient is the firmware's MQTT 3.1.1 QoS0 adapter. The natiu build
// tag also enables local transport tests without importing the radio driver.
type NatiuClient struct {
	cfg          config.MQTTConfig
	log          logger.Logger
	ops          sync.Mutex
	mu           sync.Mutex
	client       *native.Client
	conn         *packetConn
	done         chan struct{}
	handlers     map[string]MessageHandler
	queue        chan incoming
	subacks      chan subscriptionAck
	packetID     uint16
	dial         func(string, string) (net.Conn, error)
	connectStage uint8 // Owned by ops, diagnostics only.
	lifecycle    mqttLifecycle
}

// packetConn serializes complete wire writes, including the standalone UNSUBSCRIBE.
type packetConn struct {
	net.Conn
	mu        sync.Mutex
	diag      transportDiagnostic
	stopMu    sync.Mutex
	closed    bool
	interrupt func() error

	txPrefix [7]byte // fixed header + topic length; owned by mu
	// Receive state is owned only by the decoder goroutine. During CONNECT
	// receive is false, preserving the connection's handshake deadline.
	receive        bool
	packetDeadline time.Time
	prefix         [8]byte // maximum fixed header (5) plus single-topic SUBACK (3)
	prefixLen      int
	packetBytes    uint32 // diagnostic count, owned by the decoder goroutine
}

// beginReceive separates idle polling from packet completion. The native
// decoder cannot resume a partially consumed packet after a timeout.
func (c *packetConn) beginReceive() {
	c.receive = true
	c.packetDeadline = time.Time{}
	c.prefixLen = 0
	c.packetBytes = 0
	c.Conn.SetReadDeadline(time.Now().Add(receiveIdleTimeout))
}

func (c *packetConn) Read(p []byte) (int, error) {
	if !c.receive || len(p) == 0 {
		return c.Conn.Read(p)
	}
	if !c.packetDeadline.IsZero() {
		deadline := time.Now().Add(receiveProgressTimeout)
		if c.packetDeadline.Before(deadline) {
			deadline = c.packetDeadline
		}
		c.Conn.SetReadDeadline(deadline)
	}
	c.stopMu.Lock()
	closed := c.closed
	c.stopMu.Unlock()
	if closed {
		return 0, net.ErrClosed
	}
	n, err := c.Conn.Read(p)
	c.stopMu.Lock()
	closed = c.closed
	c.stopMu.Unlock()
	if closed && err != nil {
		err = net.ErrClosed
	}
	if n > 0 {
		c.packetBytes += uint32(n)
		if c.packetDeadline.IsZero() {
			c.packetDeadline = time.Now().Add(receivePacketTimeout)
		}
		c.prefixLen += copy(c.prefix[c.prefixLen:], p[:n])
	}
	return n, err
}

// suback reads the current packet's wire result, not natiu's append-only
// historical subscription list. Called only after successful native decoding.
func (c *packetConn) suback() (subscriptionAck, bool) {
	if c.prefixLen == 0 || c.prefix[0] != 0x90 {
		return subscriptionAck{}, false
	}
	r := bytes.NewReader(c.prefix[:c.prefixLen])
	h, _, err := native.DecodeHeader(r)
	if err != nil || h.Type() != native.PacketSuback || h.RemainingLength != 3 {
		return subscriptionAck{}, false
	}
	var data [3]byte
	if _, err := io.ReadFull(r, data[:]); err != nil {
		return subscriptionAck{}, false
	}
	return subscriptionAck{id: binary.BigEndian.Uint16(data[:2]), code: data[2]}, true
}

func (c *packetConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	deadline := time.Now().Add(10 * time.Second)
	seterr := c.Conn.SetWriteDeadline(deadline)
	if TransportDiagnostics == "true" && seterr != nil {
		c.observeWrite(1, len(p), 0, time.Until(deadline).Nanoseconds(), seterr, true)
	}
	n, err := c.Conn.Write(p)
	if TransportDiagnostics == "true" && err != nil {
		c.observeWrite(1, len(p), n, time.Until(deadline).Nanoseconds(), err, false)
	}
	return n, err
}

func NewNatiuClient(cfg config.MQTTConfig, log logger.Logger) *NatiuClient {
	return &NatiuClient{cfg: cfg, log: log, handlers: make(map[string]MessageHandler), dial: func(network, address string) (net.Conn, error) {
		return net.DialTimeout(network, address, 10*time.Second)
	}}
}
func (c *NatiuClient) snapshot() (*native.Client, *packetConn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.client, c.conn
}
func (c *NatiuClient) IsConnected() bool {
	client, _ := c.snapshot()
	return client != nil && client.IsConnected()
}

// ConnectDiagnostics prints no error text, endpoints or configuration.
var ConnectDiagnostics = "false"

func (c *NatiuClient) connectResult(result error) error {
	if ConnectDiagnostics == "true" {
		reason := uint8(0)
		if result != nil {
			reason = 1
		}
		println("SESSIONDIAG: mqtt stage=", c.connectStage, "reason=", reason)
	}
	return result
}

func (c *NatiuClient) Connect() error { return c.ConnectContext(context.Background()) }
func (c *NatiuClient) ConnectContext(ctx context.Context) error {
	c.lifecycle.serial.Lock()
	defer c.lifecycle.serial.Unlock()
	c.ops.Lock()
	defer c.ops.Unlock()
	c.connectStage = 0
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if c.lifecycle.closed {
		return errors.New("MQTT client permanently closed")
	}
	if c.IsConnected() {
		return c.connectResult(nil)
	}
	if c.cfg.UseTLS {
		return c.connectResult(errors.New("firmware MQTT TLS is not supported"))
	}
	if c.cfg.QoS != 0 {
		return c.connectResult(errors.New("firmware MQTT supports QoS0 only"))
	}
	if c.cfg.Password != "" && c.cfg.Username == "" {
		return c.connectResult(errors.New("MQTT password requires a username"))
	}
	if err := c.retireMQTT(); err != nil {
		return err
	}
	if err := c.initMQTT(); err != nil {
		return err
	}
	l := &c.lifecycle
	if l.generation == ^uint64(0) {
		return errors.New("MQTT generation exhausted")
	}
	l.generation++
	g := &mqttGeneration{ctx: ctx, stop: make(chan struct{}), rxDone: make(chan struct{}), dispatchDone: make(chan struct{}), cancelDone: make(chan struct{}), dialDone: make(chan struct{}), raw: make(chan *packetConn, 1)}
	l.active = g
	l.cancelStart <- g
	c.connectStage = 1
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var raw net.Conn
	var err error
	if l.native != nil {
		raw, err = l.native.DialContext(dialCtx, "tcp", net.JoinHostPort(c.cfg.Host, strconv.Itoa(c.cfg.Port)))
	} else {
		raw, err = c.dial("tcp", net.JoinHostPort(c.cfg.Host, strconv.Itoa(c.cfg.Port)))
	}
	if err != nil {
		close(g.dialDone)
		return c.failedConnect(g, err)
	}
	wire := &packetConn{Conn: raw}
	if cap, ok := raw.(interface{ GenerationInterrupt() (func() error, error) }); ok {
		wire.interrupt, err = cap.GenerationInterrupt()
		if err != nil {
			_ = raw.Close()
			close(g.dialDone)
			return c.failedConnect(g, err)
		}
	}
	g.wire = wire
	g.raw <- wire
	close(g.dialDone)
	if TransportDiagnostics == "true" {
		println("TMA2:", uintptr(unsafe.Pointer(wire)), diagnosticFD(raw))
	}
	c.connectStage = 2
	// handlers is the bounded authoritative subscription registry. Native's
	// append-only history is unused and grows even for an ID-mismatched SUBACK
	// rejected below. Keep current wire ID/code validation and handler rollback;
	// omit only that redundant history, including across successful resubscribe.
	client := native.NewClient(native.ClientConfig{DisableSubscriptionHistory: true, Decoder: native.DecoderNoAlloc{UserBuffer: l.decoder}, OnPub: func(h native.Header, v native.VariablesPublish, r io.Reader) error {
		data, err := readPublishPayload(h, v, r)
		if err != nil {
			return err
		}
		msg := incoming{topic: string(v.TopicName), payload: data}
		select {
		case <-g.stop:
			return net.ErrClosed
		default:
		}
		select {
		case c.queue <- msg:
		default:
			c.log.Warn("MQTT command queue full; dropping message")
		}
		return nil
	}})
	g.client = client
	var vc native.VariablesConnect
	vc.SetDefaultMQTT([]byte(c.cfg.ClientID))
	vc.KeepAlive = 60
	vc.CleanSession = true
	vc.Username = []byte(c.cfg.Username)
	vc.Password = []byte(c.cfg.Password)
	wire.SetDeadline(time.Now().Add(10 * time.Second))
	handshakeCtx, handshakeCancel := context.WithTimeout(ctx, 10*time.Second)
	defer handshakeCancel()
	if err = client.Connect(handshakeCtx, wire, &vc); err != nil {
		return c.failedConnect(g, err)
	}
	if ctx.Err() != nil {
		return c.failedConnect(g, ctx.Err())
	}
	wire.SetDeadline(time.Time{})
	c.mu.Lock()
	c.client = client
	c.conn = wire
	c.done = g.rxDone
	c.mu.Unlock()
	g.running = true
	l.rxStart <- g
	l.dispatchStart <- g
	c.connectStage = 3
	return c.connectResult(nil)
}
func (c *NatiuClient) readLoop(client *native.Client, wire *packetConn, queue chan incoming, done chan struct{}) {
	defer close(done)
	defer func() {
		// Native Disconnect marks the client disconnected before writing its
		// packet. Let the caller finish that write before closing the wire.
		if !errors.Is(client.Err(), errShutdown) {
			wire.closeCause(3)
		}
	}()
	lastPing := time.Now()
	pingAt := time.Time{}
	var pingBytes, pingPackets uint32
	var lastPacketType byte
	for client.IsConnected() {
		wire.beginReceive()
		if err := client.HandleNext(); err != nil {
			c.log.Warn("MQTT receive stopped: %v (packet_bytes=%d)", err, wire.packetBytes)
			break
		}
		if !pingAt.IsZero() && wire.packetBytes > 0 {
			pingBytes += wire.packetBytes
			pingPackets++
			lastPacketType = wire.prefix[0] >> 4
		}
		if ack, ok := wire.suback(); ok {
			select {
			case c.subacks <- ack:
			default:
			}
		}
		if !pingAt.IsZero() && !client.AwaitingPingresp() {
			c.log.Debug("MQTT PINGRESP decoded: wait_ms=%d", time.Since(pingAt).Milliseconds())
			pingAt = time.Time{}
		}
		if !pingAt.IsZero() && time.Since(pingAt) > 10*time.Second {
			// Counts describe what this decoder received, not whether the
			// broker sent a response or whether TCP acknowledged the request.
			c.log.Warn("MQTT ping timeout: wait_ms=%d rx_bytes=%d rx_packets=%d last_type=%d", time.Since(pingAt).Milliseconds(), pingBytes, pingPackets, lastPacketType)
			break
		}
		if time.Since(lastPing) >= 20*time.Second {
			if err := client.StartPing(); err != nil {
				c.log.Warn("MQTT PINGREQ write failed: %v", err)
				break
			}
			lastPing = time.Now()
			pingAt = lastPing
			pingBytes, pingPackets, lastPacketType = 0, 0, 0
			c.log.Debug("MQTT PINGREQ write completed")
		}
	}
	if client.IsConnected() {
		client.Disconnect(errors.New("MQTT transport stopped"))
	}
}
func (c *NatiuClient) dispatch(queue chan incoming, done chan struct{}) {
	for {
		select {
		case <-done:
			return
		case msg := <-queue:
			select {
			case <-done:
				return
			default:
			}
			c.mu.Lock()
			handler := c.handlers[msg.topic]
			c.mu.Unlock()
			if handler != nil {
				handler(msg.topic, msg.payload)
			}
		}
	}
}
func (c *NatiuClient) Disconnect() error {
	c.lifecycle.serial.Lock()
	defer c.lifecycle.serial.Unlock()
	c.ops.Lock()
	defer c.ops.Unlock()
	l := &c.lifecycle
	if l.closed {
		return nil
	}
	l.closed = true
	client, wire := c.snapshot()
	var result error
	if client != nil && client.IsConnected() {
		result = client.Disconnect(errShutdown)
	}
	if wire != nil {
		_ = wire.closeCause(4)
	}
	if err := c.retireMQTT(); err != nil && result == nil {
		result = err
	}
	if l.started {
		close(l.rxStart)
		close(l.dispatchStart)
		close(l.cancelStart)
		<-l.rxExit
		<-l.dispatchExit
		<-l.cancelExit
	}
	return result
}
func (c *NatiuClient) Publish(topic string, payload []byte, qos byte, retain bool, maxRetries int) error {
	c.ops.Lock()
	defer c.ops.Unlock()
	if qos != 0 {
		return errors.New("firmware MQTT supports QoS0 only")
	}
	if len(payload) > MaxPayload || len(topic) > 256 {
		return errors.New("MQTT publication exceeds firmware limit")
	}
	client, wire := c.snapshot()
	if client == nil || !client.IsConnected() {
		return errors.New("MQTT disconnected")
	}
	// QoS0 has no native in-flight state. The native success callback only
	// updates LastTx, which this adapter does not use (fixed ping schedule).
	// Stream under the wire lock instead of Tx's growing payload buffer.
	c.nextID() // preserve the adapter's identifier sequence (not sent for QoS0)
	return wire.writePublish([]byte(topic), payload, retain)
}
func (c *NatiuClient) nextID() uint16 {
	c.packetID++
	if c.packetID == 0 {
		c.packetID = 1
	}
	return c.packetID
}
func (c *NatiuClient) Subscribe(topic string, handler MessageHandler) error {
	c.ops.Lock()
	defer c.ops.Unlock()
	if handler == nil || topic == "" || len(topic) > 256 || strings.ContainsAny(topic, "+#") {
		return errors.New("firmware subscriptions require an exact topic and handler")
	}
	c.mu.Lock()
	_, exists := c.handlers[topic]
	full := len(c.handlers) >= maxSubscriptions
	c.mu.Unlock()
	if full && !exists {
		return errors.New("firmware subscription limit reached")
	}
	client, wire := c.snapshot()
	if client == nil || !client.IsConnected() {
		return errors.New("MQTT disconnected")
	}
	c.mu.Lock()
	previous := c.handlers[topic]
	c.handlers[topic] = handler
	c.mu.Unlock()
	success := false
	defer func() {
		if !success {
			c.mu.Lock()
			if previous == nil {
				delete(c.handlers, topic)
			} else {
				c.handlers[topic] = previous
			}
			c.mu.Unlock()
		}
	}()
	wire.SetWriteDeadline(time.Now().Add(10 * time.Second))
	id := c.nextID()
	if err := client.StartSubscribe(native.VariablesSubscribe{PacketIdentifier: id, TopicFilters: []native.SubscribeRequest{{TopicFilter: []byte(topic), QoS: native.QoS0}}}); err != nil {
		return err
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case ack := <-c.subacks:
		if ack.id != id {
			return errors.New("MQTT subscription acknowledgement identifier mismatch")
		}
		if ack.code != byte(native.QoS0) {
			return fmt.Errorf("MQTT subscription rejected: %s", topic)
		}
		success = true
		return nil
	case <-c.done:
		return errors.New("MQTT disconnected during subscription")
	case <-timer.C:
		return errors.New("MQTT subscription timeout")
	}
}
func (c *NatiuClient) Unsubscribe(topic string) error {
	c.ops.Lock()
	defer c.ops.Unlock()
	client, wire := c.snapshot()
	if client == nil || !client.IsConnected() {
		return errors.New("MQTT disconnected")
	}
	if topic == "" || len(topic) > 256 {
		return errors.New("invalid MQTT unsubscribe topic")
	}
	// natiu exposes UNSUBSCRIBE via its wire encoder, not Client. Send one
	// complete encoded packet through the same serialized transport.
	var packet bytes.Buffer
	var tx native.Tx
	tx.SetTxTransport(packetBuffer{&packet})
	if err := tx.WriteUnsubscribe(native.VariablesUnsubscribe{PacketIdentifier: c.nextID(), Topics: [][]byte{[]byte(topic)}}); err != nil {
		return err
	}
	wire.SetWriteDeadline(time.Now().Add(10 * time.Second))
	n, err := wire.Write(packet.Bytes())
	if err != nil {
		return err
	}
	if n != packet.Len() {
		return io.ErrShortWrite
	}
	c.mu.Lock()
	delete(c.handlers, topic)
	c.mu.Unlock()
	return nil
}

type packetBuffer struct{ *bytes.Buffer }

func (packetBuffer) Close() error { return nil }

var _ MQTTClient = (*NatiuClient)(nil)
