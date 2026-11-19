//go:build lowmem && !tinygo && !plan9

package websocket

import (
	"errors"
	"syscall"
	"testing"
)

func isBufioPeerErrno(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET)
}

func TestBufioPeerErrnoClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{{syscall.EPIPE, true}, {syscall.ECONNRESET, true}, {syscall.EIO, false}} {
		if got := isBufioPeerShutdown(tc.err); got != tc.want {
			t.Fatalf("shutdown(%v)=%t want %t", tc.err, got, tc.want)
		}
	}
}
