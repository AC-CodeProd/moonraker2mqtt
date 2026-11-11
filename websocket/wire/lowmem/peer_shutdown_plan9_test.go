package websocket

// Plan 9 has no POSIX EPIPE/ECONNRESET constants. Keep only the portable
// EOF/net.ErrClosed cases from isTestPeerShutdown on this platform.
func isTestPeerTransportShutdown(error) bool { return false }
