package setup

import (
	"net"
	"os"
	"time"

	"github.com/soypat/lneto/tcp"
)

// xnet's accepted net.Conn exposes the real buffered connection through this
// method. Capture its empty transmit capacity BEFORE writing a response.
// FreeOutput counts BOTH unsent and sent-but-unacknowledged bytes in pinned
// lneto v0.3.2; Flush alone only empties the unsent queue and is insufficient.
func responseCapacity(c net.Conn) int {
	if lc, ok := c.(interface{ LnetoConn() *tcp.Conn }); ok {
		return lc.LnetoConn().FreeOutput()
	}
	return 0 // synchronous transports (host tests) do not expose a TCP queue
}

func awaitResponse(c net.Conn, capacity int, deadline time.Time) error {
	lc, ok := c.(interface{ LnetoConn() *tcp.Conn })
	if !ok {
		return nil
	}
	conn := lc.LnetoConn()
	for {
		if !time.Now().Before(deadline) {
			return os.ErrDeadlineExceeded
		}
		if conn.State().IsClosed() {
			return net.ErrClosed
		}
		if conn.FreeOutput() == capacity {
			return nil
		}
		// Yield to espradio's independent handleStack/RecvAndSend goroutine. This
		// is bounded state polling, not an assumed transmission-completion delay.
		time.Sleep(time.Millisecond)
	}
}
