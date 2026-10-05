//go:build tinygo || lowmem

package wire

import ws "moonraker2mqtt/websocket/wire/lowmem"

type Conn = ws.Conn

var NewConfig = ws.NewConfig
var DialConfig = ws.DialConfig
var JSON = ws.JSON
