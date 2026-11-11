//go:build transporttest && !tinygo

package websocket

import (
	"bytes"
	"testing"
)

func TestTransportContinuationPreHandlerOpcode(t *testing.T) {
	old := TransportDiagnostics
	TransportDiagnostics = "true"
	defer func() { TransportDiagnostics = old }()
	var input, output bytes.Buffer
	input.Write([]byte{BinaryFrame, 1, 'x'})
	appendServerFrame(&input, ContinuationFrame, bytes.Repeat([]byte{'x'}, 16385))
	conn := newDrainTestConn(t, &input, &output)
	conn.MaxPayloadBytes = 16384
	var got []byte
	if Message.Receive(conn, &got) != nil {
		t.Fatal("initial frame")
	}
	if Message.Receive(conn, &got) != ErrFrameTooLarge {
		t.Fatal("continuation guard")
	}
	t.Log("REACHED raw-continuation-0 processed-binary-2")
}
