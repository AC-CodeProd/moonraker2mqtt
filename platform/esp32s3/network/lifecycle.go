// Package network holds the portable, single-owner radio lifecycle. Tests use
// fakes; only the ESP32-S3 adapter invokes the pinned espradio implementation.
package network

import (
	"context"
	"errors"
	"time"
)

var (
	ErrEnable    = errors.New("radio initialization failed; reset required")
	ErrStart     = errors.New("radio start failed")
	ErrAssociate = errors.New("Wi-Fi association failed")
	ErrStack     = errors.New("network stack initialization failed")
	ErrDHCP      = errors.New("DHCP acquisition failed")
	ErrDown      = errors.New("Wi-Fi link down")
	ErrShutdown  = errors.New("network shutdown incomplete; reset required")
)

// Device operations are synchronous and must be bounded by the implementation.
// DropStack must join its pump before Stop or a replacement stack is permitted.
// Callers must finish bridge/client shutdown before Disconnect.
type Device interface {
	Enable() error
	Start() error
	Associate(ssid, password string) error
	OpenStack() error
	DHCP() error
	Associated() bool
	DropStack() error
	Stop() error
}

type Lifecycle struct {
	Device                                  Device
	enableAttempted, enabled, active, ready bool
	fault                                   error
}

// Enable is attempted only once, including after partial initialization failure.
func (l *Lifecycle) Enable() error {
	if l.fault != nil {
		return l.fault
	}
	if !l.enableAttempted {
		l.enableAttempted = true
		if l.Device.Enable() != nil {
			l.fault = ErrEnable
			return l.fault
		}
		l.enabled = true
	}
	if !l.enabled {
		return ErrEnable
	}
	return nil
}

func (l *Lifecycle) Connected() bool { return l.ready && l.Device.Associated() }

func (l *Lifecycle) Connect(ssid, password string) error {
	if l.Connected() {
		return nil
	}
	if err := l.Disconnect(); err != nil {
		return err
	}
	if err := l.Enable(); err != nil {
		return err
	}
	l.active = true // Even partial Start failure must be quiesced before retry.
	stage := ErrStart
	if l.Device.Start() == nil {
		stage = ErrAssociate
		if l.Device.Associate(ssid, password) == nil {
			stage = ErrStack
			if l.Device.OpenStack() == nil {
				stage = ErrDHCP
				if l.Device.DHCP() == nil {
					if l.Device.Associated() {
						l.ready = true
						return nil
					}
					stage = ErrDown
				}
			}
		}
	}
	if err := l.Disconnect(); err != nil {
		return err
	}
	// Never expose driver error strings (which may contain credentials/endpoints).
	return stage
}

func (l *Lifecycle) Disconnect() error {
	l.ready = false
	if l.fault != nil {
		return l.fault
	}
	if !l.active {
		return nil
	}
	if l.Device.DropStack() != nil || l.Device.Stop() != nil {
		l.fault = ErrShutdown
		return l.fault
	}
	l.active = false
	return nil
}

// Backoff caps repeated failures without busy looping or modifying settings.
func Backoff(failures int) time.Duration {
	switch failures {
	case 0, 1:
		return 5 * time.Second
	case 2:
		return 10 * time.Second
	case 3:
		return 20 * time.Second
	default:
		return 30 * time.Second
	}
}

// Session cancels on link loss and waits for client cleanup. A timed-out runner
// must NOT be followed by a new radio/bridge session: the caller must reset.
// poll and grace are explicit to allow isolated fast host tests.
func Session(parent context.Context, healthy func() bool, run func(context.Context) error, poll, grace time.Duration) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if parent.Err() != nil {
				return parent.Err()
			}
			return err
		case <-parent.Done():
			cancel()
			select {
			case <-done:
				return parent.Err()
			case <-time.After(grace):
				return ErrShutdown
			}
		case <-ticker.C:
			if !healthy() {
				cancel()
				select {
				case <-done:
					return ErrDown
				case <-time.After(grace):
					return ErrShutdown
				}
			}
		}
	}
}
