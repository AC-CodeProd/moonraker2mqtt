//go:build lowmem && !tinygo

package websocket

// Plan 9 has no POSIX EPIPE/ECONNRESET constants.
func isBufioPeerErrno(error) bool { return false }
