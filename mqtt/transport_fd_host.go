//go:build natiu && !tinygo

package mqtt

import "net"

func diagnosticFD(conn net.Conn) int { return -1 }
