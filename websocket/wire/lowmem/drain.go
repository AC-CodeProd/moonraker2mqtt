package websocket

import "io"

// drainFrame runs under Conn.rio and never obtains a pooled scratch buffer.
// Even an exhausted reader makes io.Copy(io.Discard, reader) obtain 8 KiB
// before its first read. TinyGo cannot reliably reuse that sync.Pool buffer.
func (ws *Conn) drainFrame(r io.Reader) error {
	empty := 0
	for {
		n, err := r.Read(ws.discardBuf[:])
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if n == 0 {
			empty++
			if empty >= 100 {
				return io.ErrNoProgress
			}
		} else {
			empty = 0
		}
	}
}
