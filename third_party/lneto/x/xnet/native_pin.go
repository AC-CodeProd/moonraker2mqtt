package xnet

import (
	"github.com/soypat/lneto/tcp"
	"net"
)

func pinNativeSocket(c socket[net.Conn]) (*tcp.Conn, error) {
	if c.isvalid() {
		if n, ok := c.sock.(interface{ LnetoConn() *tcp.Conn }); ok {
			native := n.LnetoConn()
			pinned, err := native.PinNativeCall()
			if err != nil {
				return nil, err
			}
			if pinned {
				return native, nil
			}
		}
	}
	return nil, nil
}
