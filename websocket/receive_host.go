//go:build !tinygo && !lowmem

package websocket

import websocket "moonraker2mqtt/websocket/wire"

func (c *WebSocketClient) receiveMessage(conn *websocket.Conn, message *WebSocketMessage) error {
	return receiveMessage(conn, message)
}

func receiveMessage(conn *websocket.Conn, message *WebSocketMessage) error {
	return websocket.JSON.Receive(conn, message)
}
