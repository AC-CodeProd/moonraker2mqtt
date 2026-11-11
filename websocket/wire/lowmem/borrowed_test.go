package websocket

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"
)

func TestBorrowedReceiveUsesReservedStorage(t *testing.T) {
	for _, size := range []int{0, 125, 126, 4096, 13456, 16384} {
		var input, output bytes.Buffer
		appendServerFrame(&input, BinaryFrame, bytes.Repeat([]byte{'x'}, size))
		conn := newHybiConn(newConfig(t, "/"), bufio.NewReadWriter(bufio.NewReader(shortReceiveReader{&input}), bufio.NewWriter(&output)), nil, nil)
		conn.MaxPayloadBytes = 16384
		var reserved [16384]byte
		codec := Codec{Unmarshal: func(data []byte, _ byte, _ interface{}) error {
			if len(data) != size || cap(data) != size {
				t.Fatalf("len/cap=%d/%d want %d", len(data), cap(data), size)
			}
			if size > 0 && &data[0] != &reserved[0] {
				t.Fatal("per-frame allocation instead of reserved storage")
			}
			return nil
		}}
		if err := codec.ReceiveBorrowed(conn, nil, reserved[:]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBorrowedReceiveOwnedJSONSurvivesReuse(t *testing.T) {
	var input, output bytes.Buffer
	appendServerFrame(&input, TextFrame, []byte(`{"method":"original","params":{"text":"saved"},"result":["retained"],"error":{"message":"error"},"raw":{"kept":"raw"}}`))
	appendServerFrame(&input, TextFrame, bytes.Repeat([]byte{' '}, 200))
	conn := newDrainTestConn(t, &input, &output)
	var reserve [16384]byte
	var got struct {
		Method string
		Params any
		Result any
		Error  struct{ Message string }
		Raw    json.RawMessage
	}
	if err := JSON.ReceiveBorrowed(conn, &got, reserve[:]); err != nil {
		t.Fatal(err)
	}
	ignore := Codec{Unmarshal: func([]byte, byte, interface{}) error { return nil }}
	if err := ignore.ReceiveBorrowed(conn, nil, reserve[:]); err != nil {
		t.Fatal(err)
	}
	if string(got.Raw) != `{"kept":"raw"}` || got.Method != "original" || got.Params.(map[string]any)["text"] != "saved" || got.Result.([]any)[0] != "retained" || got.Error.Message != "error" {
		t.Fatalf("retained JSON overwritten: %#v", got)
	}
}

func TestBorrowedReceiveErrorsAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		wire []byte
		want error
	}{
		{nil, io.EOF}, {[]byte{0x82}, io.EOF}, {[]byte{0x82, 3}, io.EOF}, {[]byte{0x82, 3, 'x'}, io.ErrUnexpectedEOF},
	} {
		var output bytes.Buffer
		conn := newDrainTestConn(t, bytes.NewBuffer(tc.wire), &output)
		called := false
		codec := Codec{Unmarshal: func([]byte, byte, interface{}) error { called = true; return nil }}
		var reserve [16384]byte
		if err := codec.ReceiveBorrowed(conn, nil, reserve[:]); !errors.Is(err, tc.want) || called {
			t.Fatalf("err=%v want=%v called=%v", err, tc.want, called)
		}
	}
	var input, output bytes.Buffer
	appendServerFrame(&input, BinaryFrame, bytes.Repeat([]byte{'x'}, 16385))
	appendServerFrame(&input, PingFrame, []byte("ping"))
	appendServerFrame(&input, PongFrame, []byte("pong"))
	input.Write([]byte{BinaryFrame, 3, 'o', 'n', 'e'})
	appendServerFrame(&input, ContinuationFrame, []byte("two"))
	conn := newDrainTestConn(t, &input, &output)
	conn.MaxPayloadBytes = 16384
	var reserve [16384]byte
	codec := Codec{Unmarshal: func(data []byte, _ byte, v interface{}) error { *v.(*string) = string(data); return nil }}
	var got string
	if err := codec.ReceiveBorrowed(conn, &got, reserve[:]); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatal(err)
	}
	for _, want := range []string{"one", "two"} {
		if err := codec.ReceiveBorrowed(conn, &got, reserve[:]); err != nil || got != want {
			t.Fatalf("got=%q err=%v", got, err)
		}
	}
	if output.Len() == 0 || output.Bytes()[0] != 0x80|PongFrame {
		t.Fatal("missing pong")
	}
}

func TestBorrowedReceiveDecodeErrorAndBufferMismatch(t *testing.T) {
	var input, output bytes.Buffer
	appendServerFrame(&input, BinaryFrame, []byte("first"))
	appendServerFrame(&input, BinaryFrame, []byte("second"))
	conn := newDrainTestConn(t, &input, &output)
	var reserve [16]byte
	sentinel := errors.New("decode")
	codec := Codec{Unmarshal: func([]byte, byte, interface{}) error { return sentinel }}
	if err := codec.ReceiveBorrowed(conn, nil, reserve[:]); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	codec.Unmarshal = func(data []byte, _ byte, _ interface{}) error {
		if string(data) != "second" {
			t.Fatal(string(data))
		}
		return nil
	}
	if err := codec.ReceiveBorrowed(conn, nil, reserve[:]); err != nil {
		t.Fatal(err)
	}
	var in, out bytes.Buffer
	appendServerFrame(&in, BinaryFrame, []byte("large"))
	conn = newDrainTestConn(t, &in, &out)
	if err := codec.ReceiveBorrowed(conn, nil, reserve[:1]); !errors.Is(err, ErrReceiveBufferTooSmall) {
		t.Fatal(err)
	}
}

func TestBorrowedReceiveFallbackAndTransportError(t *testing.T) {
	sentinel := errors.New("transport")
	for _, frame := range []frameReader{
		fallbackReceiveFrame{bytes.NewReader([]byte("unknown"))},
		&hybiFrameReader{reader: &errorDrainReader{sentinel}, header: hybiFrameHeader{Length: 13456, OpCode: BinaryFrame}},
	} {
		conn := &Conn{frameReaderFactory: receiveFactory{frame}, frameHandler: passthroughReceiveHandler{}}
		var reserve [16384]byte
		called := false
		codec := Codec{Unmarshal: func([]byte, byte, interface{}) error { called = true; return nil }}
		want := sentinel
		if _, ok := frame.(fallbackReceiveFrame); ok {
			want = ErrNotSupported
		}
		if err := codec.ReceiveBorrowed(conn, nil, reserve[:]); !errors.Is(err, want) || called {
			t.Fatalf("err=%v called=%v", err, called)
		}
	}
}

func TestBorrowedReceiveNoLargePayloadAllocation(t *testing.T) {
	var input, output bytes.Buffer
	appendServerFrame(&input, BinaryFrame, bytes.Repeat([]byte{'x'}, 13456))
	wire := input.Bytes()
	var transport bytes.Reader
	reader := bufio.NewReader(&transport)
	conn := newHybiConn(newConfig(t, "/"), bufio.NewReadWriter(reader, bufio.NewWriter(&output)), nil, nil)
	conn.MaxPayloadBytes = 16384
	var reserve [16384]byte
	codec := Codec{Unmarshal: func([]byte, byte, interface{}) error { return nil }}
	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			transport.Reset(wire)
			reader.Reset(&transport)
			if err := codec.ReceiveBorrowed(conn, nil, reserve[:]); err != nil {
				b.Fatal(err)
			}
		}
	})
	t.Logf("after reservation: %d B/op, %d allocations/op (frame metadata, not payload)", result.AllocedBytesPerOp(), result.AllocsPerOp())
	if result.AllocedBytesPerOp() >= 1024 {
		t.Fatalf("large per-receive allocation: %d B/op", result.AllocedBytesPerOp())
	}
}
