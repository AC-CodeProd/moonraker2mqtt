//go:build tinygo || natiu

package mqtt

import (
	"errors"
	"net"
	"time"

	native "github.com/soypat/natiu-mqtt"
)

// After exclusive Connect, only mqttRXWorker may call Client methods (including
// getters and the error callbacks inside HandleNext/StartSubscribe/Disconnect).
// Public observers read a first-party snapshot under mu. QoS0 payloads still
// stream directly under packetConn.mu, without touching native Client state.
type clientStatus struct {
	connected    bool
	awaitingPing bool
	err          error
}
type clientControl struct {
	topic    string
	id       uint16
	shutdown bool
	deadline time.Time
}

func (c *NatiuClient) publishClientStatus(client *native.Client) {
	status := clientStatus{connected: client.IsConnected(), awaitingPing: client.AwaitingPingresp(), err: client.Err()}
	c.mu.Lock()
	if c.client == client {
		c.status = status
	}
	c.mu.Unlock()
}
func (c *NatiuClient) observedStatus() clientStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Enqueue and idle-read publication use the same lock: either the reader sees
// the queued control before entering Read, or enqueue sees idleRead and wakes
// exactly that first-header-byte read. Never inspect RX-owned packetBytes here.
func (wire *packetConn) enqueueControl(cmd clientControl) error {
	wire.stopMu.Lock()
	defer wire.stopMu.Unlock()
	if wire.closed {
		return net.ErrClosed
	}
	select {
	case wire.controls <- cmd:
	default:
		return errors.New("MQTT control already pending")
	}
	if wire.idleRead {
		if err := wire.Conn.SetReadDeadline(time.Now()); err != nil {
			wire.controlErr = err // owner cannot dequeue before this failure is visible
			return err
		}
	}
	return nil
}

// ops permits one public control at a time. Queues are durable capacity one;
// retireMQTT joins the old owner before draining/rebinding them. On any caller
// timeout/wake failure, interrupt and join before returning (no orphan command).
func (c *NatiuClient) requestControl(wire *packetConn, cmd clientControl, timer *time.Timer) error {
	if err := wire.enqueueControl(cmd); err != nil {
		c.abortControl(wire)
		return err
	}
	select {
	case err := <-c.controlReplies:
		return err
	case <-c.done:
		select {
		case err := <-c.controlReplies:
			return err
		default:
		}
		c.drainControls()
		return errors.New("MQTT disconnected during control")
	case <-timer.C:
		c.abortControl(wire)
		return errors.New("MQTT control timeout")
	}
}
func (c *NatiuClient) abortControl(wire *packetConn) {
	_ = wire.closeCause(9)
	<-c.done
	c.drainControls()
}
func (c *NatiuClient) drainControls() {
	for {
		select {
		case <-c.controls:
		default:
			goto replies
		}
	}
replies:
	for {
		select {
		case <-c.controlReplies:
		default:
			return
		}
	}
}

// Called only by the RX owner, between complete HandleNext calls, with neither
// native RX nor TX mutex held. Deadline is anchored at public operation entry.
func (c *NatiuClient) runControl(client *native.Client, wire *packetConn) {
	wire.stopMu.Lock()
	var cmd clientControl
	select {
	case cmd = <-wire.controls:
	default:
		wire.stopMu.Unlock()
		return
	}
	err := wire.controlErr
	if err == nil && wire.closed {
		err = net.ErrClosed
	}
	wire.stopMu.Unlock()
	if err == nil && !time.Now().Before(cmd.deadline) {
		err = errors.New("MQTT control timeout")
	}
	fatal := err != nil
	// Preserve the pre-native setter failure contract: no native state or
	// subscription token is changed, so a rejected setter can be retried.
	if err == nil && !cmd.shutdown {
		err = wire.SetWriteDeadline(cmd.deadline)
	}
	if err == nil {
		fatal = true
		wire.mu.Lock()
		wire.controlDeadline = cmd.deadline
		wire.mu.Unlock()
		if cmd.shutdown {
			c.mu.Lock()
			c.status.connected = false
			c.status.awaitingPing = false
			c.mu.Unlock()
			err = client.Disconnect(errShutdown)
		} else {
			wire.stopMu.Lock()
			exhausted := wire.subRequests >= subscriptionBudget
			if !exhausted {
				wire.subRequests++
				wire.subPending = true
				wire.subID = cmd.id
			}
			wire.stopMu.Unlock()
			if exhausted {
				err = errors.New("MQTT subscription budget exhausted; reconnect required")
				_ = wire.closeCause(9)
				_ = client.Disconnect(err)
			} else {
				err = client.StartSubscribe(native.VariablesSubscribe{PacketIdentifier: cmd.id, TopicFilters: []native.SubscribeRequest{{TopicFilter: []byte(cmd.topic), QoS: native.QoS0}}})
			}
		}
		wire.mu.Lock()
		wire.controlDeadline = time.Time{}
		wire.mu.Unlock()
	}
	if err != nil && fatal {
		_ = wire.closeCause(9)
	}
	c.publishClientStatus(client)
	c.controlReplies <- err
}
