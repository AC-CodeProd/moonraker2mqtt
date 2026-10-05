//go:build !tinygo && !lowmem

package bridge

// Hosted builds retain in-process protocol reconnection.
const endSessionOnDisconnect = false
