//go:build lowmem && !tinygo

package websocket

func newNativeDialOwner() (nativeDialOwner, error) { return nil, nil }
