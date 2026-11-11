// Private source-projection fixture: selected TinyGo methods verbatim.
// Not linked in MCU firmware; generated spans authenticated separately.
package tinygotcpfixture
import("net";"time";"io")
type Addr = net.Addr
type TCPAddr = net.TCPAddr
type OpError = net.OpError
type OwnedTCPHandle interface{Recv([]byte,time.Time)(int,error);Send([]byte,time.Time)(int,error);Close()error;GenerationInterrupt()(func()error,error)}
type Device interface { Recv(int,[]byte,int,time.Time)(int,error);Send(int,[]byte,int,time.Time)(int,error);Close(int)error }
var netdev Device
func Bind(d Device, fd int)*TCPConn {netdev=d;return &TCPConn{fd:fd,net:"tcp4"}}
func BindOwned(h OwnedTCPHandle)*TCPConn{return &TCPConn{owned:h,net:"tcp4"}}
type TCPConn struct {
	owned         OwnedTCPHandle
	fd            int
	net           string
	laddr         *TCPAddr
	raddr         *TCPAddr
	readDeadline  time.Time
	writeDeadline time.Time
}

func (c *TCPConn) Read(b []byte) (int, error) {
	var n int
	var err error
	if c.owned != nil {
		n, err = c.owned.Recv(b, c.readDeadline)
	} else {
		n, err = netdev.Recv(c.fd, b, 0, c.readDeadline)
	}
	// Turn the -1 socket error into 0 and let err speak for error
	if n < 0 {
		n = 0
	}
	if err != nil && err != io.EOF {
		err = &OpError{Op: "read", Net: c.net, Source: c.laddr, Addr: c.raddr, Err: err}
	}
	return n, err
}

func (c *TCPConn) Write(b []byte) (int, error) {
	var n int
	var err error
	if c.owned != nil {
		n, err = c.owned.Send(b, c.writeDeadline)
	} else {
		n, err = netdev.Send(c.fd, b, 0, c.writeDeadline)
	}
	// Turn the -1 socket error into 0 and let err speak for error
	if n < 0 {
		n = 0
	}
	if err != nil {
		err = &OpError{Op: "write", Net: c.net, Source: c.laddr, Addr: c.raddr, Err: err}
	}
	return n, err
}

func (c *TCPConn) Close() error {
	if c.owned != nil {
		return c.owned.Close()
	}
	return netdev.Close(c.fd)
}

func (c *TCPConn) SetDeadline(t time.Time) error {
	c.readDeadline = t
	c.writeDeadline = t
	return nil
}

func (c *TCPConn) SetReadDeadline(t time.Time) error {
	c.readDeadline = t
	return nil
}

func (c *TCPConn) SetWriteDeadline(t time.Time) error {
	c.writeDeadline = t
	return nil
}

func (c *TCPConn) LocalAddr() Addr {
	return c.laddr
}

func (c *TCPConn) RemoteAddr() Addr {
	return c.raddr
}
