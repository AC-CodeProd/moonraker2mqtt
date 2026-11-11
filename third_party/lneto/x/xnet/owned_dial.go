package xnet

import (
	"context"
	"github.com/soypat/lneto/tcp"
	"net"
	"sync"
	"time"
)

type ownedDialKey struct{}

// OwnedDial is an explicit one-attempt capability. Keep it until all app I/O,
// dial and cancellation workers have joined, then call Retire. Not for listeners.
type OwnedDial struct {
	mu                sync.Mutex
	storage           *tcp.DialStorage
	conn              *tcp.Conn
	stack             *StackAsync
	token             uint64
	acquired, retired bool
	retireDone        chan struct{}
	retireErr         error
	detachFD          func(*tcp.Conn) error // Private one-stream Berkeley table only.
}

func NewOwnedDial(s *tcp.DialStorage) *OwnedDial {
	return &OwnedDial{storage: s, retireDone: make(chan struct{})}
}
func (a *OwnedDial) Context(ctx context.Context) context.Context {
	return context.WithValue(ctx, ownedDialKey{}, a)
}
func (a *OwnedDial) acquire(s *StackAsync, backoff func(uint) time.Duration) (*tcp.Conn, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.acquired || a.retired || a.storage == nil {
		return nil, net.ErrClosed
	}
	c, token, err := a.storage.Acquire(backoff)
	if err != nil {
		return nil, err
	}
	a.conn = c
	a.token = token
	a.stack = s
	a.acquired = true
	return c, nil
}
func (a *OwnedDial) Retire() (result error) {
	a.mu.Lock()
	if a.retired {
		a.mu.Unlock()
		return net.ErrClosed
	}
	a.retired = true
	defer func() { a.mu.Lock(); a.retireErr = result; close(a.retireDone); a.mu.Unlock() }()
	if !a.acquired {
		a.mu.Unlock()
		return nil
	}
	c, s, token, storage := a.conn, a.stack, a.token, a.storage
	a.mu.Unlock()
	return storage.Retire(c, token, func() error {
		s.mu.Lock()
		c.Abort()
		s.tcps.DetachTCP(c)
		s.mu.Unlock()
		if a.detachFD != nil {
			return a.detachFD(c)
		}
		return nil
	})
}
