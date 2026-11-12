//go:build !tinygo && !lowmem

package websocket

type requestDecodeKind struct{}

func (*requestDecodeKind) setKlippyState(bool) {}
