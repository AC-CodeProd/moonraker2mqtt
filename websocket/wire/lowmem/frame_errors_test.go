package websocket

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"
)

type propagationWriter struct {
	n                  int
	writeErr, closeErr error
	closes             int
}

func (w *propagationWriter) Write([]byte) (int, error) { return w.n, w.writeErr }
func (w *propagationWriter) Close() error              { w.closes++; return w.closeErr }

type propagationWriterFactory struct {
	writer frameWriter
	err    error
	opcode byte
}

func (f *propagationWriterFactory) NewFrameWriter(opcode byte) (frameWriter, error) {
	f.opcode = opcode
	return f.writer, f.err
}

func TestFrameWriteErrorPropagation(t *testing.T) {
	writeErr := errors.New("injected payload write error")
	closeErr := errors.New("injected writer close error")
	factoryErr := errors.New("injected factory error")
	for _, api := range []string{"Write", "Send"} {
		t.Run(api, func(t *testing.T) {
			for _, tc := range []struct {
				name                           string
				n                              int
				writeErr, closeErr, factoryErr error
				want                           error
				closes                         int
			}{
				{"success", 4, nil, nil, nil, nil, 1},
				{"write", 2, writeErr, nil, nil, writeErr, 1},
				{"close", 4, nil, closeErr, nil, closeErr, 1},
				{"primary write wins", 2, writeErr, closeErr, nil, writeErr, 1},
				{"short write", 2, nil, nil, nil, io.ErrShortWrite, 1},
				{"short write wins", 2, nil, closeErr, nil, io.ErrShortWrite, 1},
				{"factory", 0, nil, nil, factoryErr, factoryErr, 0},
			} {
				t.Run(tc.name, func(t *testing.T) {
					w := &propagationWriter{n: tc.n, writeErr: tc.writeErr, closeErr: tc.closeErr}
					f := &propagationWriterFactory{writer: w, err: tc.factoryErr}
					ws := &Conn{PayloadType: TextFrame, frameWriterFactory: f}
					var err error
					if api == "Write" {
						var n int
						n, err = ws.Write([]byte("data"))
						if n != tc.n {
							t.Fatalf("Write count = %d, want %d", n, tc.n)
						}
					} else {
						cd := Codec{Marshal: func(interface{}) ([]byte, byte, error) { return []byte("data"), TextFrame, nil }}
						err = cd.Send(ws, nil)
					}
					if !errors.Is(err, tc.want) {
						t.Fatalf("error = %v, want %v", err, tc.want)
					}
					if w.closes != tc.closes || f.opcode != TextFrame {
						t.Fatalf("closes=%d opcode=%d, want closes=%d opcode=%d", w.closes, f.opcode, tc.closes, TextFrame)
					}
				})
			}
		})
	}
}

type propagationSink struct{ err error }

func (w propagationSink) Write([]byte) (int, error) { return 0, w.err }

func TestRealFrameFlushErrorPropagation(t *testing.T) {
	want := errors.New("injected transport flush error")
	for _, api := range []string{"Write", "Send"} {
		t.Run(api, func(t *testing.T) {
			buf := bufio.NewReadWriter(bufio.NewReader(bytes.NewReader(nil)), bufio.NewWriter(propagationSink{want}))
			ws := newHybiConn(&Config{}, buf, nil, nil)
			var err error
			if api == "Write" {
				_, err = ws.Write([]byte("data"))
			} else {
				err = Message.Send(ws, "data")
			}
			if !errors.Is(err, want) {
				t.Fatalf("real frame flush error = %v, want %v", err, want)
			}
		})
	}
}

type propagationFrame struct {
	trailer io.Reader
	payload []byte
}

func (f *propagationFrame) Read(b []byte) (int, error) { return copy(b, f.payload), io.EOF }
func (*propagationFrame) PayloadType() byte            { return TextFrame }
func (*propagationFrame) HeaderReader() io.Reader      { return nil }
func (f *propagationFrame) TrailerReader() io.Reader   { return f.trailer }
func (f *propagationFrame) Len() int                   { return len(f.payload) }

type propagationReaderFactory struct {
	calls int
	err   error
}

func (f *propagationReaderFactory) NewFrameReader() (frameReader, error) {
	f.calls++
	return nil, f.err
}

func TestReadTrailerDrainErrorPropagation(t *testing.T) {
	want := errors.New("injected trailer read error")
	next := errors.New("next frame boundary")
	for _, tc := range []struct {
		name         string
		trailer      io.Reader
		payload      []byte
		want         error
		n, nextCalls int
	}{
		{"drain error", &errorDrainReader{want}, nil, want, 0, 0},
		{"progress preserved", &errorDrainReader{want}, []byte("x"), want, 1, 0},
		{"no progress", &emptyDrainReader{}, nil, io.ErrNoProgress, 0, 0},
		{"successful drain", bytes.NewReader([]byte("trailer")), nil, next, 0, 1},
		{"no trailer", nil, nil, next, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := &propagationFrame{trailer: tc.trailer, payload: tc.payload}
			factory := &propagationReaderFactory{err: next}
			ws := &Conn{frameReader: frame, frameReaderFactory: factory}
			buffer := make([]byte, 8)
			n, err := ws.Read(buffer)
			if n != tc.n || !errors.Is(err, tc.want) || factory.calls != tc.nextCalls {
				t.Fatalf("Read=(%d,%v), next calls=%d; want (%d,%v), next calls=%d", n, err, factory.calls, tc.n, tc.want, tc.nextCalls)
			}
			if tc.nextCalls == 0 && ws.frameReader != frame {
				t.Fatal("failed trailer silently advanced the frame")
			}
			if r, ok := tc.trailer.(*emptyDrainReader); ok && r.calls != 100 {
				t.Fatalf("no-progress bound changed: %d", r.calls)
			}
			if n > 0 && !bytes.Equal(buffer[:n], tc.payload) {
				t.Fatal("payload changed")
			}
		})
	}
}
