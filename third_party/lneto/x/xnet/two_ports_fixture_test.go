//go:build native_storage_fixture

package xnet

import (
	"github.com/soypat/lneto/tcp"
	"testing"
)

func newTCPStacksTwoPorts(t testing.TB, randSeed int64, mtu int) (s1, s2 *StackAsync, c1, c2 *tcp.Conn) {
	s1, s2 = new(StackAsync), new(StackAsync)
	c1, c2 = new(tcp.Conn), new(tcp.Conn)
	byte1 := byte(randSeed)/4 - 1
	err := s1.Reset(StackConfig{
		Hostname:          "Stack1",
		RandSeed:          randSeed,
		StaticAddress4:    [4]byte{10, 0, 0, byte1},
		MaxActiveTCPPorts: 2,
		HardwareAddress:   [6]byte{0xbe, 0xef, 0, 0, 0, byte1},
		MTU:               uint16(mtu),
		ICMPQueueLimit:    2,
	})
	if err != nil {
		t.Fatal(err)
	}

	byte2 := byte1 + 1
	err = s2.Reset(StackConfig{
		Hostname:          "Stack2",
		RandSeed:          ^randSeed,
		StaticAddress4:    [4]byte{10, 0, 0, byte2},
		MaxActiveTCPPorts: 2,
		HardwareAddress:   [6]byte{0xbe, 0xef, 0, 0, 0, byte2},
		MTU:               uint16(mtu),
		ICMPQueueLimit:    2,
	})
	if err != nil {
		t.Fatal(err)
	}
	s1.SetGatewayHardwareAddr(s2.HardwareAddr())
	s2.SetGatewayHardwareAddr(s1.HardwareAddr())
	buf := make([]byte, mtu*4)
	err = c1.Configure(tcp.ConnConfig{
		RxBuf:             buf[:mtu],
		TxBuf:             buf[mtu : mtu*2],
		TxPacketQueueSize: 4,
		RWBackoff:         backoffYield,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = c2.Configure(tcp.ConnConfig{
		RxBuf:             buf[2*mtu : 3*mtu],
		TxBuf:             buf[3*mtu : 4*mtu],
		TxPacketQueueSize: 4,
		RWBackoff:         backoffYield,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s1, s2, c1, c2
}
