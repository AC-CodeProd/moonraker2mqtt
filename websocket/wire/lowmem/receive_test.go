package websocket

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"
)

// Split transport reads independently of WebSocket frame boundaries.
type shortReceiveReader struct{ io.Reader }

func (r shortReceiveReader) Read(p []byte) (int, error) {
	if len(p) > 7 {
		p = p[:7]
	}
	return r.Reader.Read(p)
}

func TestReceiveExactPayloadCapacity(t *testing.T) {
	for _, size := range []int{0, 125, 126, 4096, 16384, 16385} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			var input, output bytes.Buffer
			want := bytes.Repeat([]byte("x"), size)
			appendServerFrame(&input, BinaryFrame, want)
			appendServerFrame(&input, BinaryFrame, []byte("next"))
			conn := newHybiConn(newConfig(t, "/"), bufio.NewReadWriter(bufio.NewReader(shortReceiveReader{&input}), bufio.NewWriter(&output)), nil, nil)
			conn.MaxPayloadBytes = 16385
			var got []byte
			if err := Message.Receive(conn, &got); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) || len(got) != size || cap(got) != size {
				t.Fatalf("payload len=%d cap=%d, want %d", len(got), cap(got), size)
			}
			var next []byte
			if err := Message.Receive(conn, &next); err != nil || string(next) != "next" {
				t.Fatalf("following payload=%q err=%v", next, err)
			}
			if !bytes.Equal(got, want) {
				t.Fatal("previous codec slice overwritten by subsequent receive")
			}
			if len(got) > 0 {
				next[0] = '!'
				if !bytes.Equal(got, want) {
					t.Fatal("codec slices share backing storage")
				}
			}
		})
	}
}

func TestReceiveTruncatedPayload(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire []byte
		want error
	}{
		{"empty-stream", nil, io.EOF},
		{"truncated-header", []byte{0x82}, io.EOF},
		{"missing-payload", []byte{0x82, 3}, io.EOF},
		{"partial-payload", []byte{0x82, 3, 'x'}, io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			conn := newDrainTestConn(t, bytes.NewBuffer(tc.wire), &output)
			called := false
			codec := Codec{Unmarshal: func([]byte, byte, interface{}) error { called = true; return nil }}
			if err := codec.Receive(conn, nil); !errors.Is(err, tc.want) || called {
				t.Fatalf("error=%v want=%v unmarshal called=%v", err, tc.want, called)
			}
		})
	}
}

func TestReceiveFragmentedFramesPreservesUpstreamSemantics(t *testing.T) {
	// Upstream Receive decodes individual frames, not an assembled message.
	var input, output bytes.Buffer
	input.Write([]byte{BinaryFrame, 3, 'o', 'n', 'e'}) // FIN=0
	appendServerFrame(&input, PingFrame, []byte("ping"))
	appendServerFrame(&input, PongFrame, []byte("pong"))
	appendServerFrame(&input, ContinuationFrame, []byte("two"))
	conn := newDrainTestConn(t, &input, &output)
	for _, want := range []string{"one", "two"} {
		codec := Codec{Unmarshal: func(data []byte, opcode byte, _ interface{}) error {
			if string(data) != want || opcode != BinaryFrame || cap(data) != len(data) {
				t.Fatalf("data=%q opcode=%d cap=%d", data, opcode, cap(data))
			}
			return nil
		}}
		if err := codec.Receive(conn, nil); err != nil {
			t.Fatal(err)
		}
	}
	if output.Len() == 0 || output.Bytes()[0] != 0x80|PongFrame {
		t.Fatalf("missing pong: %x", output.Bytes())
	}
}

func TestReceiveDrainsPartiallyReadFrame(t *testing.T) {
	var input, output bytes.Buffer
	appendServerFrame(&input, BinaryFrame, bytes.Repeat([]byte("x"), 4096))
	appendServerFrame(&input, BinaryFrame, []byte("next"))
	conn := newDrainTestConn(t, &input, &output)
	var prefix [125]byte
	if n, err := conn.Read(prefix[:]); n != len(prefix) || err != nil {
		t.Fatalf("partial read n=%d err=%v", n, err)
	}
	var got []byte
	if err := Message.Receive(conn, &got); err != nil || string(got) != "next" || cap(got) != 4 {
		t.Fatalf("following payload=%q cap=%d err=%v", got, cap(got), err)
	}
}

// Non-hybi frames retain ReadAll's behavior, including unknown lengths.
type fallbackReceiveFrame struct{ io.Reader }

func (fallbackReceiveFrame) PayloadType() byte        { return BinaryFrame }
func (fallbackReceiveFrame) HeaderReader() io.Reader  { return nil }
func (fallbackReceiveFrame) TrailerReader() io.Reader { return nil }
func (fallbackReceiveFrame) Len() int                 { return 0 }

type receiveFactory struct{ frame frameReader }

func (f receiveFactory) NewFrameReader() (frameReader, error) { return f.frame, nil }

type passthroughReceiveHandler struct{}

func (passthroughReceiveHandler) HandleFrame(f frameReader) (frameReader, error) { return f, nil }
func (passthroughReceiveHandler) WriteClose(int) error                           { return nil }

func TestReceiveFallback(t *testing.T) {
	wantErr := errors.New("transport failure")
	for _, tc := range []struct {
		name   string
		reader io.Reader
		want   string
		err    error
	}{
		{"unknown-length", bytes.NewBufferString("fallback"), "fallback", nil},
		{"error", &errorDrainReader{wantErr}, "", wantErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &Conn{frameReaderFactory: receiveFactory{fallbackReceiveFrame{tc.reader}}, frameHandler: passthroughReceiveHandler{}, MaxPayloadBytes: 1}
			var got []byte
			if err := Message.Receive(conn, &got); !errors.Is(err, tc.err) || string(got) != tc.want {
				t.Fatalf("payload=%q err=%v", got, err)
			}
		})
	}
}

func TestReceiveKnownLengthTransportError(t *testing.T) {
	want := errors.New("transport failure")
	frame := &hybiFrameReader{reader: &errorDrainReader{want}, header: hybiFrameHeader{Length: 125, OpCode: BinaryFrame}}
	conn := &Conn{frameReaderFactory: receiveFactory{frame}, frameHandler: passthroughReceiveHandler{}}
	var got []byte
	if err := Message.Receive(conn, &got); !errors.Is(err, want) || got != nil {
		t.Fatalf("payload=%q err=%v", got, err)
	}
}
