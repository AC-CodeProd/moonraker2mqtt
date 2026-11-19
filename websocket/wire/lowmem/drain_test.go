package websocket

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

type boundedDrainReader struct{ remaining, largestRead int }

func (r *boundedDrainReader) Read(p []byte) (int, error) {
	if len(p) > r.largestRead {
		r.largestRead = len(p)
	}
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > r.remaining {
		n = r.remaining
	}
	r.remaining -= n
	return n, nil
}

type emptyDrainReader struct{ calls int }

func (r *emptyDrainReader) Read([]byte) (int, error) { r.calls++; return 0, nil }

type errorDrainReader struct{ err error }

func (r *errorDrainReader) Read([]byte) (int, error) { return 0, r.err }

func TestDrainReusesBoundedBufferWithoutAllocation(t *testing.T) {
	conn := new(Conn)
	r := &boundedDrainReader{}
	allocs := testing.AllocsPerRun(1000, func() {
		r.remaining = 16 * 1024
		if err := conn.drainFrame(r); err != nil {
			panic(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("drain allocated %g objects", allocs)
	}
	if r.remaining != 0 || r.largestRead != 256 {
		t.Fatalf("remaining=%d largest read=%d", r.remaining, r.largestRead)
	}
}

func TestDrainAlreadyExhaustedDoesNotAllocate(t *testing.T) {
	conn := new(Conn)
	r := bytes.NewReader(nil)
	if allocs := testing.AllocsPerRun(1000, func() {
		if err := conn.drainFrame(r); err != nil {
			panic(err)
		}
	}); allocs != 0 {
		t.Fatalf("empty drain allocated %g objects", allocs)
	}
}

func TestDrainErrorsAndNoProgress(t *testing.T) {
	conn := new(Conn)
	want := errors.New("broken reader")
	if err := conn.drainFrame(&errorDrainReader{want}); !errors.Is(err, want) {
		t.Fatalf("error=%v", err)
	}
	r := new(emptyDrainReader)
	if err := conn.drainFrame(r); !errors.Is(err, io.ErrNoProgress) || r.calls != 100 {
		t.Fatalf("error=%v reads=%d", err, r.calls)
	}
}
