//go:build tinygo && esp32s3

package esp32s3

import (
	"context"
	"fmt"
	"github.com/soypat/lneto/x/xnet"
	"machine"
	"moonraker2mqtt/platform/esp32s3/setup"
	"moonraker2mqtt/platform/esp32s3/store"
	"net"
	"net/netip"
	"tinygo.org/x/espradio"
	link "tinygo.org/x/espradio/netlink"
)

// Deliberately no shared/default password. Supply a unique 12+ character
// WPA2 password at build time and distribute it outside tracked sources.
var SetupPassword string
var SetupSSID = "Moonraker-Setup"

// ForceSetup is a permanent build flag, not a one-shot latch. Saving will still
// return to setup until rebuilt/reinstalled with false. Entering setup never
// erases settings; the separate confirmed repair can reset only the two slots.
var ForceSetup = "false"

func runPortal(radio *radioLink, savedErr error) error {
	if QualificationReadOnly != "false" {
		return fmt.Errorf("setup writes disabled in qualification image")
	}
	if len(SetupPassword) < 12 || len(SetupPassword) > 63 {
		return fmt.Errorf("setup requires a unique WPA2 password (12-63 characters)")
	}
	if err := radio.NetConnectAP(link.APConnectParams{APConfig: espradio.APConfig{SSID: SetupSSID, Password: SetupPassword, Channel: 6, AuthOpen: false}, EnableDHCPServer: true, MaxTCPPorts: 2, MaxUDPPorts: 1, PassivePeers: 4}); err != nil {
		return err
	}
	// With the RF source enabled, the ESP32-S3 RNG supplies the session token.
	token := ""
	for i := 0; i < 4; i++ {
		n, err := machine.GetRNG()
		if err != nil {
			return err
		}
		token += fmt.Sprintf("%08x", n)
	}
	// Espradio's documented direct-stack listener avoids its stdlib fd bug.
	sock, err := radio.StackGo().SocketNetip(context.Background(), "tcp4", xnet.AF_INET, xnet.SOCK_STREAM, netip.AddrPortFrom(netip.MustParseAddr("192.168.4.1"), 80), netip.AddrPort{})
	if err != nil {
		return err
	}
	listener, ok := sock.(net.Listener)
	if !ok {
		return fmt.Errorf("setup listener unavailable")
	}
	defer listener.Close()
	println("SETUP: WPA2 AP active; open http://192.168.4.1 (no automatic captive DNS)")
	recovery := store.RecoveryRequired(savedErr)
	// Decode errors on a valid record can be replaced by normal Save. Hardware
	// I/O/readback failures disable staging; they must not authorize a reset.
	p := setup.Portal{Token: token, Page: setup.Page, Save: stagePending,
		Recovery: recovery, Unavailable: savedErr == ErrBootFlash || savedErr == store.ErrVerify, Repair: stageRepair, Cancel: cancelPending}
	for {
		c, err := listener.Accept()
		if err != nil {
			return err
		}
		if p.Handle(c) {
			rebootSave()
		}
	}
}
