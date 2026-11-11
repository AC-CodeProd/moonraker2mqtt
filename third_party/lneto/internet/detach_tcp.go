package internet

import "github.com/soypat/lneto/tcp"

// DetachTCP removes every callback/reference for this exact native identity,
// including unused tail capacity left by earlier compactions. The caller MUST
// hold the owning stack lock and join all callbacks before returning.
func (ps *StackPortsMACFiltered) DetachTCP(c *tcp.Conn) {
	h := &ps.sp.handlers
	id := c.ConnectionID()
	all := h.nodes[:cap(h.nodes)]
	n := 0
	for i := range h.nodes {
		if h.nodes[i].connID != id {
			all[n] = h.nodes[i]
			n++
		}
	}
	clear(all[n:])
	h.nodes = all[:n]
	h.encapsIdx = 0
}
