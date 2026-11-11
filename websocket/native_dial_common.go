//go:build tinygo || lowmem

package websocket

import (
	"context"
	"net"
)

type nativeDialOwner interface {
	DialContext(context.Context, string, string) (net.Conn, error)
	Retire() error
}
