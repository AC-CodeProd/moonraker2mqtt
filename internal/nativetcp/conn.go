// Package nativetcp adapts application-owned network streams to the public
// net.Conn interface. It does not replace or modify the Go/TinyGo net package.
package nativetcp

import (
	"context"
	"errors"
	"net"
)

// Owner retires native storage only after the protocol has joined its workers.
type Owner interface {
	DialContext(context.Context, string, string) (net.Conn, error)
	Retire() error
}

// NewOwner is installed by the platform during single-threaded radio setup,
// before protocol clients are created. No default net.Dial fallback is safe:
// it cannot provide generation interruption and native retirement guarantees.
var NewOwner func() (Owner, error)

func New() (Owner, error) {
	if NewOwner == nil {
		return nil, errors.New("application TCP owner unavailable")
	}
	return NewOwner()
}
