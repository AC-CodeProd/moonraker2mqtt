//go:build !tinygo && !lowmem

package websocket

import (
	"context"
	"time"
)

const reusableWorkers = false

type platformLifecycle struct{}

func (*WebSocketClient) connectReusable(context.Context) error { panic("unreachable") }
func (*WebSocketClient) disconnectReusable() error             { panic("unreachable") }
func (*WebSocketClient) requestReusable(context.Context, string, any, time.Duration, bool) (*WebSocketMessage, error) {
	panic("unreachable")
}

func (*WebSocketClient) initLifecycle() {}
