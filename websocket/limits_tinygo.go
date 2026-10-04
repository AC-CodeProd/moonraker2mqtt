//go:build tinygo

package websocket

import "golang.org/x/net/websocket"

const sendQueueSize = 4
const maxPendingRequests = 8

func configureConnection(conn *websocket.Conn) { conn.MaxPayloadBytes = 16 * 1024 }
