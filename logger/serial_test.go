package logger

import (
	"bytes"
	"strings"
	"testing"
)

func TestSerialLoggerFiltering(t *testing.T) {
	var out bytes.Buffer
	log := NewSerial(&out, INFO)
	log.Debug("hidden")
	log.Info("value %d", 7)
	log.Warn("warning")
	log.Error("error")
	if strings.Contains(out.String(), "hidden") || !strings.Contains(out.String(), "[INFO] value 7\n") {
		t.Fatal(out.String())
	}
	log.SetLevel(ERROR)
	if log.GetLevel() != ERROR {
		t.Fatal("level not updated")
	}
	out.Reset()
	log.Info("hidden")
	if out.Len() != 0 {
		t.Fatal("filter ignored")
	}
}
