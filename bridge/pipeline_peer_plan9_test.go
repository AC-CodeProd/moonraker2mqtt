//go:build lowmem && natiu && !tinygo

package bridge

// Plan 9 has no POSIX EPIPE/ECONNRESET constants.
func pipelinePeerErrno(error) bool { return false }
