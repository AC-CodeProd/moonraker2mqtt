//go:build !tinygo && !transporttest

package websocket

func observeFrameReject(hf *hybiFrameReader, limit int, borrowed bool, rawOpcode byte, parsedHeaderBytes int) {
}
func diagnosticHeader(frame frameReader) (byte, int) { return 0, 0 }
