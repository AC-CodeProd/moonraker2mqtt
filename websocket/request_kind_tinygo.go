//go:build tinygo || lowmem

package websocket

type requestDecodeKind struct{ klippyState bool }

func (r *requestDecodeKind) setKlippyState(typed bool) { r.klippyState = typed }
