//go:build tinygo && esp32s3

package esp32s3

import (
	"context"
	"errors"
	"github.com/soypat/lneto/x/xnet"
	"moonraker2mqtt/internal/nativetcp"
	"moonraker2mqtt/platform/esp32s3/network"
	"net"
	"strconv"
)

type radioDialStorage struct {
	radio *radioLink
	owner *xnet.NativeDialOwner
}

// newNativeDialOwner uses the application adapter and the selected lneto stack.
// The stock TinyGo netdev backend remains untouched for setup HTTP traffic.
func (n *radioLink) newNativeDialOwner() (nativetcp.Owner, error) {
	if !n.connected() || n.StackBerkeley == nil {
		return nil, network.ErrDown
	}
	return &radioDialStorage{radio: n, owner: xnet.NewNativeDialOwner(n.goStack)}, nil
}
func (d *radioDialStorage) DialContext(ctx context.Context, networkName, address string) (net.Conn, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !d.radio.connected() {
		return nil, network.ErrDown
	}
	host, portText, e := net.SplitHostPort(address)
	if e != nil {
		return nil, e
	}
	port, e := strconv.Atoi(portText)
	if e != nil || port < 1 || port > 65535 {
		return nil, errors.New("invalid owned TCP port")
	}
	ip, e := d.radio.GetHostByName(host)
	if e != nil {
		return nil, e
	}
	if !ip.Is4() {
		return nil, errors.New("owned native dial requires IPv4")
	}
	remote := &net.TCPAddr{IP: ip.AsSlice(), Port: port}
	stream, err := d.owner.DialContext(ctx, networkName, remote)
	if err != nil {
		return nil, err
	}
	return xnet.BindStream(stream, remote), nil
}
func (d *radioDialStorage) Retire() error { return d.owner.Retire() }
