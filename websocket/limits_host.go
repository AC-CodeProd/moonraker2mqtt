//go:build !tinygo

package websocket

import "golang.org/x/net/websocket"

const sendQueueSize = 100
const maxPendingRequests = 0                   // Preserve the hosted client's existing behavior.
func configureConnection(conn *websocket.Conn) {}
