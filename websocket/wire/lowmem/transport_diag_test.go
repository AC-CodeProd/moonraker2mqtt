//go:build transporttest && !tinygo

package websocket

import (
	"bytes"
	"errors"
	"testing"
)

func TestTransportReject16KiBBoundary(t *testing.T) {
	old := TransportDiagnostics
	TransportDiagnostics = "true"
	defer func() { TransportDiagnostics = old }()
	for _, size := range []int{16384, 16385} {
		var input, output bytes.Buffer
		appendServerFrame(&input, BinaryFrame, bytes.Repeat([]byte{'x'}, size))
		conn := newDrainTestConn(t, &input, &output)
		conn.MaxPayloadBytes = 16384
		called := false
		codec := Codec{Unmarshal: func(data []byte, opcode byte, _ interface{}) error {
			called = true
			if len(data) != size {
				t.Fatal("length changed")
			}
			return nil
		}}
		err := codec.Receive(conn, nil)
		if size == 16384 {
			if err != nil || !called {
				t.Fatal("16KiB rejected", err)
			}
		} else {
			if !errors.Is(err, ErrFrameTooLarge) || called {
				t.Fatal("oversize accepted", err)
			}
		}
		t.Logf("REACHED parsed-frame-size=%d", size)
	}
}
