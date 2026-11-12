package websocket

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"
)

type hybiErrorSink struct {
	bytes.Buffer
	err   error
	calls int
}

func (w *hybiErrorSink) Write(p []byte) (int, error) {
	w.calls++
	if w.err != nil {
		return 0, w.err
	}
	return w.Buffer.Write(p)
}

func TestHybiWriteErrorsPreservePayloadCount(t *testing.T) {
	want := errors.New("injected transport write failure")
	const bufferSize = 4096
	for _, masked := range []bool{false, true} {
		for _, api := range []string{"frame", "Conn.Write", "Message.Send"} {
			for _, phase := range []string{"header", "payload", "flush", "success"} {
				t.Run(fmt.Sprintf("masked=%t/%s/%s", masked, api, phase), func(t *testing.T) {
					sink := &hybiErrorSink{}
					if phase != "success" {
						sink.err = want
					}
					bw := bufio.NewWriterSize(sink, bufferSize)
					payload := bytes.Repeat([]byte("x"), 2*bufferSize)
					if phase == "flush" {
						payload = []byte("data")
					}
					before := bytes.Clone(payload)
					headerBytes := 4 // 16-bit extended payload length.
					if masked {
						headerBytes += 4
					}
					wantN := len(payload)
					switch phase {
					case "header":
						// Force a real bufio flush during header assembly.
						if n, err := bw.Write(make([]byte, bufferSize-1)); err != nil || n != bufferSize-1 {
							t.Fatalf("prime buffer: n=%d err=%v", n, err)
						}
						wantN = 0
					case "payload":
						// Count accepted payload bytes, never header bytes or peer ACKs.
						wantN = bufferSize - headerBytes
					}
					factory := hybiFrameWriterFactory{Writer: bw, needMaskingKey: masked}
					var n int
					var err error
					switch api {
					case "frame":
						var w frameWriter
						w, err = factory.NewFrameWriter(TextFrame)
						if err != nil {
							t.Fatal(err)
						}
						n, err = w.Write(payload)
						if closeErr := w.Close(); closeErr != nil {
							t.Errorf("close frame: %v", closeErr)
						}
					case "Conn.Write":
						ws := &Conn{PayloadType: TextFrame, frameWriterFactory: factory}
						n, err = ws.Write(payload)
					case "Message.Send":
						ws := &Conn{frameWriterFactory: factory}
						err = Message.Send(ws, string(payload))
					}
					if phase == "success" {
						if err != nil {
							t.Fatal(err)
						}
						reader := hybiFrameReaderFactory{Reader: bufio.NewReader(bytes.NewReader(sink.Bytes()))}
						frame, e := reader.NewFrameReader()
						if e != nil {
							t.Fatal(e)
						}
						decoded, e := io.ReadAll(frame)
						if e != nil || !bytes.Equal(decoded, payload) || frame.PayloadType() != TextFrame {
							t.Fatalf("encoded frame changed: bytes=%d error=%v", len(decoded), e)
						}
					} else {
						if !errors.Is(err, want) || sink.calls != 1 {
							t.Fatalf("error=%v writes=%d, want original error and one failed write", err, sink.calls)
						}
					}
					if api != "Message.Send" && n != wantN {
						t.Fatalf("payload count=%d, want %d", n, wantN)
					}
					if !bytes.Equal(payload, before) {
						t.Fatal("caller payload mutated")
					}
				})
			}
		}
	}
}
