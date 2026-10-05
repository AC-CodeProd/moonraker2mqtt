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
	cfg      config.MQTTConfig
	log      logger.Logger
	ops      sync.Mutex
	mu       sync.Mutex
	client   *native.Client
	conn     *packetConn
	done     chan struct{}
	handlers map[string]MessageHandler
	queue    chan incoming
	subacks  chan subscriptionAck
	packetID uint16
	dial     func(string, string) (net.Conn, error)
}

// packetConn serializes complete wire writes, including the standalone UNSUBSCRIBE.
type packetConn struct {
	net.Conn
	mu sync.Mutex
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
	n, err := c.Conn.Read(p)
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
	c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.Conn.Write(p)
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
func (c *NatiuClient) Connect() error {
	c.ops.Lock()
	defer c.ops.Unlock()
	if c.IsConnected() {
		return nil
	}
	if c.cfg.UseTLS {
		return errors.New("firmware MQTT TLS is not supported")
	}
	if c.cfg.QoS != 0 {
		return errors.New("firmware MQTT supports QoS0 only")
	}
	if c.cfg.Password != "" && c.cfg.Username == "" {
		return errors.New("MQTT password requires a username")
	}
	if _, old := c.snapshot(); old != nil {
		old.Close()
		if c.done != nil {
			<-c.done
		}
	}
	conn, err := c.dial("tcp", net.JoinHostPort(c.cfg.Host, strconv.Itoa(c.cfg.Port)))
	if err != nil {
		return err
	}
	wire := &packetConn{Conn: conn}
	queue := make(chan incoming, 4)
	client := native.NewClient(native.ClientConfig{Decoder: native.DecoderNoAlloc{UserBuffer: make([]byte, MaxPayload+256)}, OnPub: func(_ native.Header, v native.VariablesPublish, r io.Reader) error {
		data, err := io.ReadAll(io.LimitReader(r, MaxPayload+1))
		if err != nil {
			return err
		}
		if len(data) > MaxPayload {
			return errors.New("MQTT incoming payload exceeds limit")
		}
		msg := incoming{topic: string(v.TopicName), payload: data}
		select {
		case queue <- msg:
		default:
			c.log.Warn("MQTT command queue full; dropping message")
		}
		return nil
	}})
	var vc native.VariablesConnect
	vc.SetDefaultMQTT([]byte(c.cfg.ClientID))
	vc.KeepAlive = 60
	vc.CleanSession = true
	vc.Username = []byte(c.cfg.Username)
	vc.Password = []byte(c.cfg.Password)
	wire.SetDeadline(time.Now().Add(10 * time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = client.Connect(ctx, wire, &vc); err != nil {
		wire.Close()
		return err
	}
	wire.SetDeadline(time.Time{})
	done := make(chan struct{})
	c.done = done
	c.mu.Lock()
	c.client = client
	c.conn = wire
	c.queue = queue
	c.subacks = make(chan subscriptionAck, 1)
	c.mu.Unlock()
	go c.readLoop(client, wire, queue, done)
	go c.dispatch(queue, done)
	return nil
}
func (c *NatiuClient) readLoop(client *native.Client, wire *packetConn, queue chan incoming, done chan struct{}) {
	defer close(done)
	defer func() {
		// Native Disconnect marks the client disconnected before writing its
		// packet. Let the caller finish that write before closing the wire.
		if !errors.Is(client.Err(), errShutdown) {
			wire.Close()
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
			c.subacks <- ack
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
	c.ops.Lock()
	defer c.ops.Unlock()
	client, wire := c.snapshot()
	if wire == nil {
		return nil
	}
	// The receive loop has a short deadline; do not interrupt a partially
	// received packet before sending the graceful DISCONNECT.
	var err error
	if client.IsConnected() {
		err = client.Disconnect(errShutdown)
	}
	wire.Close()
	if c.done != nil {
		<-c.done
	}
	c.mu.Lock()
	c.client = nil
	c.conn = nil
	c.mu.Unlock()
	return err
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
	flags, err := native.NewPublishFlags(native.QoS0, false, retain)
	if err != nil {
		return err
	}
	wire.SetWriteDeadline(time.Now().Add(10 * time.Second))
	// QoS0 is at-most-once: do not silently retry a potentially delivered packet.
	return client.PublishPayload(flags, native.VariablesPublish{TopicName: []byte(topic), PacketIdentifier: c.nextID()}, payload)
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
