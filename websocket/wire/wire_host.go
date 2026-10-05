//go:build !tinygo && !lowmem

package wire

import ws "golang.org/x/net/websocket"

type Conn = ws.Conn

var NewConfig = ws.NewConfig
var DialConfig = ws.DialConfig
var JSON = ws.JSON
