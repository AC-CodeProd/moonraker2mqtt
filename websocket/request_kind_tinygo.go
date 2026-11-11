//go:build tinygo || lowmem

package websocket

type requestDecodeKind struct{ klippyState bool }

func (r *WebSocketRequest) setKlippyState(typed bool) { r.klippyState = typed }
