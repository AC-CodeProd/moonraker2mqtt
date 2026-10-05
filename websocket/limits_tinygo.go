//go:build tinygo || lowmem

package websocket

import websocket "moonraker2mqtt/websocket/wire"

const sendQueueSize = 4
const maxPendingRequests = 8

func configureConnection(conn *websocket.Conn) { conn.MaxPayloadBytes = 16 * 1024 }
