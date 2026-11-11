//go:build tinygo || lowmem

package websocket

import (
	websocket "moonraker2mqtt/websocket/wire"
	"sync"
)

// The firmware has one Moonraker stream. Static reservation is present before
// Wi-Fi initialization, not a late 16 KiB heap allocation after the handshake.
// The mutex covers receive + decode; it also prevents aliasing if a previous
// read loop overlaps shutdown/reconnect. Multiple MCU streams serialize here.
var moonrakerReceiveStorage struct {
	sync.Mutex
	payload [16 * 1024]byte
}

func receiveMessage(conn *websocket.Conn, message *WebSocketMessage) error {
	moonrakerReceiveStorage.Lock()
	defer moonrakerReceiveStorage.Unlock()
	// Only this concrete, freshly zeroed schema is allowed here. Its any fields
	// decode to owned maps/slices/strings; it has no custom UnmarshalJSON method.
	// Generic Message/custom codecs still receive independent owned storage.
	return websocket.JSON.ReceiveBorrowed(conn, message, moonrakerReceiveStorage.payload[:])
}
