//go:build tinygo

package websocket

import "net"

func newNativeDialOwner() (nativeDialOwner, error) { return net.NewOwnedDialer() }
