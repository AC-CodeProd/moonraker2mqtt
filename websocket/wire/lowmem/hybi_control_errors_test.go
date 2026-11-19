package websocket

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/http"
	"testing"
)

func TestControlWriterErrorPropagation(t *testing.T) {
	writeErr, closeErr := errors.New("control write error"), errors.New("control close error")
	for _, api := range []string{"close", "pong"} {
		t.Run(api, func(t *testing.T) {
			for _, tc := range []struct {
				name                     string
				n                        int
				writeErr, closeErr, want error
			}{
				{"success", 2, nil, nil, nil},
				{"write", 1, writeErr, nil, writeErr},
				{"close", 2, nil, closeErr, closeErr},
				{"primary write wins", 1, writeErr, closeErr, writeErr},
				{"short write", 1, nil, nil, io.ErrShortWrite},
				{"short write wins", 1, nil, closeErr, io.ErrShortWrite},
			} {
				t.Run(tc.name, func(t *testing.T) {
					w := &propagationWriter{n: tc.n, writeErr: tc.writeErr, closeErr: tc.closeErr}
					f := &propagationWriterFactory{writer: w}
					h := &hybiFrameHandler{conn: &Conn{frameWriterFactory: f}}
					var err error
					wantOpcode := byte(CloseFrame)
					if api == "close" {
						err = h.WriteClose(closeStatusNormal)
					} else {
						wantOpcode = PongFrame
						var n int
						n, err = h.WritePong([]byte("ok"))
						if n != tc.n {
							t.Fatalf("pong count=%d want=%d", n, tc.n)
						}
					}
					if !errors.Is(err, tc.want) || w.closes != 1 || f.opcode != wantOpcode {
						t.Fatalf("error=%v closes=%d opcode=%d; want error=%v, one close, opcode=%d", err, w.closes, f.opcode, tc.want, wantOpcode)
					}
				})
			}
		})
	}
}

func TestProtocolRejectionPropagatesCloseError(t *testing.T) {
	want := errors.New("protocol close failed")
	for _, server := range []bool{false, true} {
		name := "client"
		if server {
			name = "server"
		}
		t.Run(name, func(t *testing.T) {
			for _, fail := range []bool{false, true} {
				caseName := "close succeeds"
				if fail {
					caseName = "close fails"
				}
				t.Run(caseName, func(t *testing.T) {
					w := &propagationWriter{n: 2}
					if fail {
						w.writeErr = want
						w.n = 0
					}
					f := &propagationWriterFactory{writer: w}
					ws := &Conn{frameWriterFactory: f}
					frame := &hybiFrameReader{header: hybiFrameHeader{OpCode: TextFrame}}
					if server {
						ws.request = &http.Request{}
					} else {
						frame.header.MaskingKey = []byte{1, 2, 3, 4}
					}
					h := &hybiFrameHandler{conn: ws}
					got, err := h.HandleFrame(frame)
					expected := io.EOF
					if fail {
						expected = want
					}
					if got != nil || !errors.Is(err, expected) || f.opcode != CloseFrame || w.closes != 1 {
						t.Fatalf("frame=%v error=%v opcode=%d closes=%d, want rejection with %v", got, err, f.opcode, w.closes, expected)
					}
				})
			}
		})
	}
}

func TestProtocolRejectionRealFlushError(t *testing.T) {
	want := errors.New("real protocol-close flush failed")
	for _, server := range []bool{false, true} {
		var request *http.Request
		frame := &hybiFrameReader{header: hybiFrameHeader{OpCode: TextFrame}}
		if server {
			request = &http.Request{}
		} else {
			frame.header.MaskingKey = []byte{1, 2, 3, 4}
		}
		buf := bufio.NewReadWriter(bufio.NewReader(bytes.NewReader(nil)), bufio.NewWriter(propagationSink{want}))
		ws := newHybiConn(&Config{}, buf, nil, request)
		got, err := ws.HandleFrame(frame)
		if got != nil || !errors.Is(err, want) {
			t.Fatalf("server=%t frame=%v error=%v, want original flush error", server, got, err)
		}
	}
}

type controlDrainReader struct {
	first  bool
	err    error
	drains int
}

func (r *controlDrainReader) Read([]byte) (int, error) {
	if !r.first {
		r.first = true
		return 0, io.EOF
	}
	r.drains++
	return 0, r.err
}

type controlReadFactory struct {
	frame frameReader
	calls int
	next  error
}

func (f *controlReadFactory) NewFrameReader() (frameReader, error) {
	f.calls++
	if f.calls == 1 {
		return f.frame, nil
	}
	return nil, f.next
}

func TestControlDrainErrorsStopBeforeReplyAndNextFrame(t *testing.T) {
	drainErr, next := errors.New("control drain failed"), errors.New("next frame boundary")
	for _, opcode := range []byte{PingFrame, PongFrame} {
		name := "ping"
		if opcode == PongFrame {
			name = "pong"
		}
		t.Run(name, func(t *testing.T) {
			for _, api := range []string{"handler", "Conn.Read", "Message.Receive"} {
				t.Run(api, func(t *testing.T) {
					for _, phase := range []string{"drain error", "no progress", "success"} {
						t.Run(phase, func(t *testing.T) {
							r := &controlDrainReader{}
							if phase == "drain error" {
								r.err = drainErr
							}
							var body io.Reader = r
							if phase == "success" {
								body = bytes.NewReader(nil)
							}
							frame := &hybiFrameReader{reader: body, header: hybiFrameHeader{OpCode: opcode, data: bytes.NewBuffer([]byte{0x80 | opcode, 0})}}
							w := &propagationWriter{n: 0}
							writer := &propagationWriterFactory{writer: w}
							reader := &controlReadFactory{frame: frame, next: next}
							ws := &Conn{frameWriterFactory: writer, frameReaderFactory: reader}
							h := &hybiFrameHandler{conn: ws}
							ws.frameHandler = h
							var err error
							switch api {
							case "handler":
								_, err = h.HandleFrame(frame)
							case "Conn.Read":
								_, err = ws.Read(make([]byte, 8))
							case "Message.Receive":
								var data []byte
								err = Message.Receive(ws, &data)
							}
							want := drainErr
							if phase == "no progress" {
								want = io.ErrNoProgress
							}
							wantCalls, wantCloses := 1, 0
							if phase == "success" {
								want, wantCalls = next, 2
								if opcode == PingFrame {
									wantCloses = 1
								}
							}
							if api == "handler" {
								wantCalls = 0
								if phase == "success" {
									want = nil
								}
							}
							if !errors.Is(err, want) || reader.calls != wantCalls || w.closes != wantCloses || frame.header.data.Len() != 0 {
								t.Fatalf("error=%v next calls=%d replies=%d header=%d; want error=%v calls=%d replies=%d empty header", err, reader.calls, w.closes, frame.header.data.Len(), want, wantCalls, wantCloses)
							}
							if phase == "no progress" && r.drains != 100 {
								t.Fatalf("drain bound=%d, want 100", r.drains)
							}
						})
					}
				})
			}
		})
	}
}
