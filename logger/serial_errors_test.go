package logger

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

type serialErrorReader interface{ Err() error }

func serialErrors(t *testing.T, log *SerialLogger) serialErrorReader {
	t.Helper()
	reader, ok := any(log).(serialErrorReader)
	if !ok {
		t.Fatal("serial output errors are not observable")
	}
	return reader
}

type serialFaultWriter struct {
	output   bytes.Buffer
	calls    int
	fail     bool
	fraction int
	err      error
}

func (w *serialFaultWriter) Write(p []byte) (int, error) {
	w.calls++
	n := len(p)
	if w.fail {
		n = len(p) * w.fraction / 2
	}
	written, err := w.output.Write(p[:n])
	if err != nil {
		return written, err
	}
	if w.fail {
		return written, w.err
	}
	return written, nil
}

func TestSerialLoggerRecordsOutputFailures(t *testing.T) {
	want := errors.New("serial output failure")
	for _, tc := range []struct {
		name     string
		fraction int
		err      error
	}{
		{"no progress", 0, want}, {"partial", 1, want}, {"full count with error", 2, want}, {"short write", 1, io.ErrShortWrite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer := &serialFaultWriter{fail: true, fraction: tc.fraction, err: tc.err}
			log := NewSerial(writer, DEBUG)
			var public Logger = log
			public.Error("value=%d", 7)
			if got := serialErrors(t, log).Err(); !errors.Is(got, tc.err) || writer.calls != 1 {
				t.Fatalf("error=%v calls=%d", got, writer.calls)
			}
		})
	}
}

func TestSerialLoggerFirstErrorPersistsWithoutDisablingWrites(t *testing.T) {
	first, second := errors.New("first output failure"), errors.New("second output failure")
	writer := &serialFaultWriter{fail: true, err: first}
	log := NewSerial(writer, DEBUG)
	reader := serialErrors(t, log)
	if reader.Err() != nil || writer.calls != 0 {
		t.Fatal("reading initial state wrote output")
	}
	log.Info("first")
	writer.err = second
	log.Warn("second")
	writer.fail = false
	log.Error("restored")
	if reader.Err() != first || writer.calls != 3 || writer.output.String() != "[ERROR] restored\n" {
		t.Fatalf("error=%v calls=%d output=%q", reader.Err(), writer.calls, writer.output.String())
	}
	if fresh := NewSerial(io.Discard, INFO); serialErrors(t, fresh).Err() != nil {
		t.Fatal("error state leaked across instances")
	}
}

func TestSerialLoggerFilteringDoesNotCreateOrClearOutputErrors(t *testing.T) {
	want := errors.New("filtered writer failure")
	writer := &serialFaultWriter{fail: true, err: want}
	log := NewSerial(writer, ERROR)
	reader := serialErrors(t, log)
	log.Debug("hidden")
	log.Info("hidden")
	log.Warn("hidden")
	if writer.calls != 0 || reader.Err() != nil {
		t.Fatal("filtered record touched output")
	}
	log.Error("visible")
	log.SetLevel(DEBUG)
	log.Debug("visible again")
	if writer.calls != 2 || reader.Err() != want || log.GetLevel() != DEBUG {
		t.Fatal("filter/level changed error state")
	}
}

func TestSerialLoggerConcurrentErrorObservation(t *testing.T) {
	const workers, messages = 8, 8
	for _, fail := range []bool{false, true} {
		want := errors.New("concurrent writer failure")
		writer := &serialFaultWriter{fail: fail, err: want}
		log := NewSerial(writer, DEBUG)
		reader := serialErrors(t, log)
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(worker int) {
				defer wg.Done()
				for j := 0; j < messages; j++ {
					log.Info("worker=%d event=%d", worker, j)
					if err := reader.Err(); err != nil && !errors.Is(err, want) {
						t.Errorf("unexpected error: %v", err)
					}
				}
			}(i)
		}
		wg.Wait()
		if writer.calls != workers*messages {
			t.Fatalf("unexpected retry or dropped write: %d", writer.calls)
		}
		if fail {
			if reader.Err() != want {
				t.Fatal("concurrent error lost")
			}
		} else if reader.Err() != nil || strings.Count(writer.output.String(), "\n") != workers*messages {
			t.Fatal("healthy concurrent output changed")
		}
	}
}
