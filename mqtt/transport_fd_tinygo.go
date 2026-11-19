//go:build tinygo

package mqtt

import "net"

// Use the selected dependency's explicit identity API, not TCPConn's layout.
// Unsupported transports and owned handles remain explicit -1. A numeric fd
// alone is not a globally unique identity or proof that the stream is live.
//
//go:noinline
func diagnosticFD(conn net.Conn) int {
	c, ok := conn.(interface{ DiagnosticFD() int })
	if !ok || c == nil {
		return -1
	}
	return c.DiagnosticFD()
}
