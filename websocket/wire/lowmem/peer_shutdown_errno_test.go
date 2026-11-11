//go:build !plan9

package websocket

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"testing"
)

func isTestPeerTransportShutdown(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET)
}

func TestPeerTransportShutdownClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"broken pipe", syscall.EPIPE, true},
		{"reset peer", syscall.ECONNRESET, true},
		{"wrapped peer", fmt.Errorf("write: %w", syscall.EPIPE), true},
		{"timeout", os.ErrDeadlineExceeded, false},
		{"short write", io.ErrShortWrite, false},
		{"other errno", syscall.EIO, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTestPeerShutdown(tc.err); got != tc.want {
				t.Fatalf("isTestPeerShutdown(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
