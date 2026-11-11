package xnet

import "net"

// GenerationInterrupt captures a capability, never a mutable file descriptor.
// Hold mu during capture so Close cannot delete this fd before identity binds.
// Accepted/pooled connections and unqualified adapters reject the opt-in.
func (s *StackBerkeley) GenerationInterrupt(fd int) (func() error, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.getConn(fd)
	if !c.isvalid() {
		return nil, net.ErrClosed
	}
	active, ok := c.sock.(tcpconn)
	if !ok || active.interruptEpoch == 0 || active.interruptOwner == nil {
		return nil, net.ErrClosed
	}
	interrupt, err := active.Conn.ClaimGenerationInterrupt(active.interruptEpoch)
	if err != nil {
		return nil, err
	}
	return func() error {
		// Same ordering as DialTCP4 and every Ingress/Egress: stack -> Conn.
		// internet.node.IsInvalid dereferences ConnectionID under the stack lock,
		// not Conn.mu. Holding only Conn.mu would leave a native-generation race.
		active.interruptOwner.mu.Lock()
		defer active.interruptOwner.mu.Unlock()
		return interrupt()
	}, nil
}
