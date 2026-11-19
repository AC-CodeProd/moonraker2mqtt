//go:build lowmem && natiu && !tinygo && !plan9

package bridge

import (
	"errors"
	"syscall"
	"testing"
)

func pipelinePeerErrno(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET)
}

func TestPipelinePeerErrnoClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{syscall.EPIPE, true}, {syscall.ECONNRESET, true}, {syscall.EIO, false},
	} {
		if got := pipelinePeerShutdown(tc.err); got != tc.want {
			t.Fatalf("shutdown(%v)=%t want %t", tc.err, got, tc.want)
		}
	}
}
