//go:build !tinygo && !lowmem

package websocket

type requestDecodeKind struct{}

func (*WebSocketRequest) setKlippyState(bool) {}
