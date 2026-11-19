//go:build tinygo

package websocket

import "moonraker2mqtt/internal/nativetcp"

func newNativeDialOwner() (nativeDialOwner, error) { return nativetcp.New() }
