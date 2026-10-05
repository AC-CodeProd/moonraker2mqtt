//go:build tinygo && esp32s3

package esp32s3

/*
int bridge_radio_associated(void);
*/
import "C"

import (
	"errors"
	"net"
	"net/netip"
	"time"

	"github.com/soypat/lneto/x/xnet"
	"moonraker2mqtt/platform/esp32s3/network"
	"tinygo.org/x/drivers/netdev"
	nl "tinygo.org/x/drivers/netlink"
	"tinygo.org/x/espradio"
	link "tinygo.org/x/espradio/netlink"
)

// radioLink deliberately replaces only Esplink's lifecycle/stack construction.
// Socket operations remain pinned lneto's Berkeley implementation. No upstream
// source fork, credentials logging, flash I/O, or changes to hosted transports.
type radioLink struct {
	*xnet.StackBerkeley
	life               network.Lifecycle
	stack              *espradio.Stack
	device             *espradio.NetDev
	goStack            xnet.StackGo
	stopPump, pumpDone chan struct{}
	notify             func(nl.Event)
}

var _ netdev.Netdever = (*radioLink)(nil)

func newRadioLink() *radioLink {
	n := &radioLink{}
	n.life.Device = (*radioDriver)(n)
	return n
}

func (n *radioLink) NetConnect(p *nl.ConnectParams) error {
	if p == nil || len(p.Ssid) == 0 {
		return nl.ErrMissingSSID
	}
	err := n.life.Connect(p.Ssid, p.Passphrase)
	if err == nil && n.notify != nil {
		n.notify(nl.EventNetUp)
	}
	return err
}
func (n *radioLink) NetDisconnect() { _ = n.disconnect() }
func (n *radioLink) disconnect() error {
	err := n.life.Disconnect()
	if n.notify != nil {
		n.notify(nl.EventNetDown)
	}
	return err
}
func (n *radioLink) NetNotify(cb func(nl.Event)) { n.notify = cb }
func (n *radioLink) connected() bool             { return n.life.Connected() }
func (n *radioLink) StackGo() xnet.StackGo       { return n.goStack }
func (n *radioLink) GetHardwareAddr() (net.HardwareAddr, error) {
	if n.stack == nil {
		return nil, network.ErrDown
	}
	mac := n.stack.LnetoStack().HardwareAddr()
	return mac[:], nil
}
func (n *radioLink) Addr() (netip.Addr, error) {
	if !n.connected() || n.stack == nil {
		return netip.Addr{}, network.ErrDown
	}
	return netip.AddrFrom4(n.stack.LnetoStack().Addr4()), nil
}
func (n *radioLink) GetHostByName(name string) (netip.Addr, error) {
	// A DHCP address retained after association loss is not a usable network.
	if !n.connected() || n.stack == nil {
		return netip.Addr{}, network.ErrDown
	}
	if ip, err := netip.ParseAddr(name); err == nil {
		return ip, nil
	}
	ips, err := n.stack.LnetoStack().StackRetrying(network.BackoffPoll).DoLookupIP(name, 5*time.Second, 3)
	if err != nil || len(ips) == 0 {
		return netip.Addr{}, errors.New("DNS lookup failed")
	}
	return ips[0], nil
}
func (n *radioLink) Connect(fd int, host string, addr netip.AddrPort) error {
	if !n.connected() || n.StackBerkeley == nil {
		return network.ErrDown
	}
	if (!addr.Addr().IsValid() || addr.Addr().IsUnspecified()) && host != "" {
		ip, err := n.GetHostByName(host)
		if err != nil {
			return err
		}
		addr = netip.AddrPortFrom(ip, addr.Port())
	}
	return n.StackBerkeley.Connect(fd, host, addr)
}

type radioDriver radioLink

func (d *radioDriver) Enable() error {
	println("RADIO: enable begin")
	err := espradio.Enable(espradio.Config{Logging: espradio.LogLevelNone})
	if err == nil {
		println("RADIO: enable complete")
	}
	return err
}
func (d *radioDriver) Start() error {
	println("RADIO: station start begin")
	err := espradio.Start()
	if err == nil {
		println("RADIO: station start complete")
	}
	return err
}
func (d *radioDriver) Associate(ssid, password string) error {
	println("RADIO: association begin")
	err := espradio.Connect(espradio.STAConfig{SSID: ssid, Password: password})
	if err == nil {
		println("RADIO: association complete")
	} else if MemoryDiagnostics == "true" {
		// Numeric diagnostics only: never print credentials or err.Error().
		if code, ok := err.(espradio.Error); ok {
			println("RADIO: connect error code=", int32(code))
		} else {
			println("RADIO: connect error type=other")
		}
	}
	return err
}
func (d *radioDriver) Associated() bool { return C.bridge_radio_associated() != 0 }
func (d *radioDriver) OpenStack() error {
	nd, err := espradio.StartNetDev()
	if err != nil {
		return err
	}
	return (*radioLink)(d).openStack(nd, espradio.StackConfig{
		Hostname: "moonraker2mqtt", MaxTCPPorts: network.TCPPorts, MaxUDPPorts: network.UDPPorts, PassivePeers: network.PassivePeers,
	})
}
func (n *radioLink) openStack(nd *espradio.NetDev, cfg espradio.StackConfig) error {
	stack, err := espradio.NewStack(nd, cfg)
	if err != nil {
		return err
	}
	n.stack, n.device = stack, nd
	n.goStack = network.GoStack(stack.LnetoStack())
	n.StackBerkeley = xnet.NewBerkeleyStack(n.goStack.Socket)
	stop, done := make(chan struct{}), make(chan struct{})
	n.stopPump, n.pumpDone = stop, done
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			sent, received, _ := stack.RecvAndSend()
			if sent == 0 && received == 0 {
				time.Sleep(network.PollInterval)
			}
		}
	}()
	return nil
}
func (d *radioDriver) DHCP() error {
	println("RADIO: DHCP begin")
	_, err := d.stack.SetupWithDHCP(espradio.DHCPConfig{})
	if err == nil {
		println("RADIO: DHCP complete")
	}
	return err
}
func (d *radioDriver) DropStack() error {
	if d.stopPump == nil {
		return nil
	}
	close(d.stopPump)
	select {
	case <-d.pumpDone:
		// NetDev is a pinned-driver singleton. Release its closure only after
		// the pump joins, so it cannot retain or dispatch into the old stack.
		d.device.SetEthRecvHandler(nil)
		d.device = nil
		d.stopPump, d.pumpDone = nil, nil
		d.stack = nil
		d.StackBerkeley = nil
		d.goStack = xnet.StackGo{}
		return nil
	case <-time.After(2 * time.Second):
		return network.ErrShutdown
	}
}
func (d *radioDriver) Stop() error { return espradio.Stop() }

// Recover into the authenticated setup AP without calling Enable again. Only
// invoked before bridge clients exist or after joined shutdown; settings remain
// sealed, and changing them still uses the existing reboot-only RTC handoff.
func (n *radioLink) NetConnectAP(p link.APConnectParams) error {
	if err := n.disconnect(); err != nil {
		return err
	}
	if err := n.life.Enable(); err != nil {
		return err
	}
	println("RADIO: setup AP start begin")
	if espradio.StartAP(p.APConfig) != nil {
		return errors.New("setup AP start failed; reset required")
	}
	nd, err := espradio.StartNetDevAP()
	if err != nil {
		return errors.New("setup AP netdev failed")
	}
	addr := netip.MustParseAddr("192.168.4.1")
	if err := n.openStack(nd, espradio.StackConfig{
		Hostname: "moonraker-setup", StaticAddress: addr, StaticSubnet: netip.PrefixFrom(addr, 24),
		MaxTCPPorts: network.TCPPorts, MaxUDPPorts: network.UDPPorts, PassivePeers: network.PassivePeers, AcceptBroadcast4: true,
	}); err != nil {
		return errors.New("setup AP stack failed")
	}
	if p.EnableDHCPServer {
		if err := n.stack.SetupWithDHCPServer(netip.PrefixFrom(addr, 24)); err != nil {
			return errors.New("setup DHCP server failed")
		}
	}
	println("RADIO: setup AP ready")
	return nil
}
