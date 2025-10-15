package websocket

import (
	"context"
)

type StatusListener interface {
	OnStateChanged(state string)
	OnNotification(method string, params any)
	OnException(err error)
}

type DataHandler interface {
	ProcessDataMessage(message any) bool
}

type Client interface {
	Connect(ctx context.Context) error
	Disconnect() error
	IsConnected() bool
	GetState() string
	Request(ctx context.Context, method string, params any) (*WebSocketResponse, error)
	RegisterDataHandler(handler DataHandler)
	UnregisterDataHandler(handler DataHandler)
}
