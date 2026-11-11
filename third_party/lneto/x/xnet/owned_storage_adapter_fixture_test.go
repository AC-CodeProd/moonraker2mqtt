//go:build native_storage_fixture

package xnet

import (
	"context"
	"github.com/soypat/lneto/tcp"
)

func fixtureSocketOwner() func(context.Context) (context.Context, func(*StackAsync, *tcp.Conn) error) {
	s := new(tcp.DialStorage)
	s.Init()
	return func(ctx context.Context) (context.Context, func(*StackAsync, *tcp.Conn) error) {
		a := NewOwnedDial(s)
		return a.Context(ctx), func(_ *StackAsync, _ *tcp.Conn) error { return a.Retire() }
	}
}
