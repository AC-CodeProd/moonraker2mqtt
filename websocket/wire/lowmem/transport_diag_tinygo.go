//go:build tinygo || transporttest

package websocket

var TransportDiagnostics = "false"

//go:noinline
func observeFrameReject(hf *hybiFrameReader, limit int, borrowed bool, rawOpcode byte, parsedHeaderBytes int) {
	if TransportDiagnostics != "true" {
		return
	}
	// HeaderReader consumes no bytes here: only the already parsed metadata.
	println("TWS2:", hf.header.Length, limit, rawOpcode, hf.header.OpCode, hf.header.Fin, hf.header.Rsv[0], hf.header.Rsv[1], hf.header.Rsv[2], hf.header.MaskingKey != nil, parsedHeaderBytes, 1, borrowed)
}

func diagnosticHeader(frame frameReader) (byte, int) {
	if TransportDiagnostics == "true" {
		if hf, ok := frame.(*hybiFrameReader); ok && hf.header.data != nil {
			return hf.header.OpCode, hf.header.data.Len()
		}
	}
	return 0, 0
}
