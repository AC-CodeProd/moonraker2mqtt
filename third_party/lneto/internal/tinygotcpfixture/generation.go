package tinygotcpfixture

import "errors"

func (c *TCPConn) GenerationInterrupt() (func() error, error) {
	if c.owned != nil {
		return c.owned.GenerationInterrupt()
	}
	d, ok := netdev.(interface {
		GenerationInterrupt(int) (func() error, error)
	})
	if !ok {
		return nil, errors.New("generation interruption unsupported")
	}
	return d.GenerationInterrupt(c.fd)
}
