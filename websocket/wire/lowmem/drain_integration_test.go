package websocket

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
)

func appendServerFrame(w *bytes.Buffer, opcode byte, payload []byte) {
	w.WriteByte(0x80 | opcode)
	if len(payload) < 126 {
		w.WriteByte(byte(len(payload)))
	} else {
		w.WriteByte(126)
		var length [2]byte
		binary.BigEndian.PutUint16(length[:], uint16(len(payload)))
		w.Write(length[:])
	}
	w.Write(payload)
}

func newDrainTestConn(t *testing.T, input *bytes.Buffer, output *bytes.Buffer) *Conn {
	return newHybiConn(newConfig(t, "/"), bufio.NewReadWriter(bufio.NewReader(input), bufio.NewWriter(output)), nil, nil)
}

func TestCodecRepeatedReceiveUsesBoundedDrain(t *testing.T) {
	var input, output bytes.Buffer
	for id := 1; id <= 1000; id++ {
		appendServerFrame(&input, TextFrame, []byte(fmt.Sprintf(`{"id":%d,"result":{"klippy_state":"ready"}}`, id)))
	}
	conn := newDrainTestConn(t, &input, &output)
	for id := 1; id <= 1000; id++ {
		var got struct {
			ID     int
			Result struct {
				KlippyState string `json:"klippy_state"`
			}
		}
		if err := JSON.Receive(conn, &got); err != nil {
			t.Fatalf("frame %d: %v", id, err)
		}
		if got.ID != id || got.Result.KlippyState != "ready" {
			t.Fatalf("frame %d: %+v", id, got)
		}
	}
}

func TestCodecOversizeFrameDrainedBeforeFollowingFrame(t *testing.T) {
	var input, output bytes.Buffer
	appendServerFrame(&input, TextFrame, bytes.Repeat([]byte("x"), 16*1024+1))
	appendServerFrame(&input, TextFrame, []byte(`{"id":42}`))
	conn := newDrainTestConn(t, &input, &output)
	conn.MaxPayloadBytes = 16 * 1024
	var got struct{ ID int }
	if err := JSON.Receive(conn, &got); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversize error=%v", err)
	}
	if err := JSON.Receive(conn, &got); err != nil || got.ID != 42 {
		t.Fatalf("following frame: id=%d error=%v", got.ID, err)
	}
}

func TestCodecPingDrainPreservesFollowingJSONFrame(t *testing.T) {
	var input, output bytes.Buffer
	appendServerFrame(&input, PingFrame, []byte("ping"))
	appendServerFrame(&input, TextFrame, []byte(`{"id":42}`))
	conn := newDrainTestConn(t, &input, &output)
	var got struct{ ID int }
	if err := JSON.Receive(conn, &got); err != nil || got.ID != 42 {
		t.Fatalf("frame after ping: id=%d error=%v", got.ID, err)
	}
	if output.Len() == 0 || output.Bytes()[0] != 0x80|PongFrame {
		t.Fatalf("pong not returned: %x", output.Bytes())
	}
}
