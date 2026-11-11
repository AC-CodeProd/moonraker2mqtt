package websocket

import (
	"errors"
	"net"
	"sync"
)

// Only the Moonraker bounded dial owns this wrapper, never MQTT or global netdev.
// Stream wrappers are generation-specific; the first-party native owner retains
// its Conn/buffers. All workers join before native retirement/reuse.
type generationTransport struct {
	net.Conn
	interrupt func() error
	once      sync.Once
	closeErr  error
}

func (c *generationTransport) Close() error {
	c.once.Do(func() { _ = c.interrupt(); c.closeErr = c.Conn.Close() })
	return c.closeErr
}
func ownGenerationTransport(c net.Conn) (net.Conn, error) {
	if p, ok := c.(interface{ GenerationInterrupt() (func() error, error) }); ok {
		interrupt, err := p.GenerationInterrupt()
		if err != nil {
			return c, err
		}
		return &generationTransport{Conn: c, interrupt: interrupt}, nil
	}
	if requireNativeGeneration {
		return c, errors.New("native generation interruption required")
	}
	return c, nil
}
