//go:build tinygo || lowmem

package bridge

// lowmem exercises the MCU session policy offline without the physical radio.
const endSessionOnDisconnect = true
