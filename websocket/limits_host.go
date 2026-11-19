//go:build !tinygo && !lowmem

package websocket

import websocket "moonraker2mqtt/websocket/wire"

const sendQueueSize = 100
const maxPendingRequests = 0                   // Preserve the hosted client's existing behavior.
func configureConnection(conn *websocket.Conn) {}
