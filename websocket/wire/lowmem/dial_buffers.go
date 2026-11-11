package websocket

import "bufio"

// DialBuffers owns exactly one 4096-byte Reader and Writer backing and their
// ReadWriter. It must remain strongly owned by one client, not a per-dial Config.
// The owner serializes use and joins ALL old I/O and cancellation before reuse.
// It must not be copied after Init. Conn (including its mutexes) is never reused.
type DialBuffers struct { pair *bufio.ReadWriter }

// Init reserves both backing buffers before the client's periodic activity.
// Repeated calls are allocation-free; Reset must never operate on zero buffers.
func (b *DialBuffers) Init() {
 if b.pair == nil {
  b.pair = bufio.NewReadWriter(bufio.NewReaderSize(nil, 4096), bufio.NewWriterSize(nil, 4096))
 }
}

// Release detaches old transport/proof references and clears buffered bytes and
// errors WITHOUT Flush or Close. Call only after the old users are fully joined.
// Never call after a successful HTTP101: Reader may contain the first frame.
func (b *DialBuffers) Release() {
 if b.pair != nil { b.pair.Reader.Reset(nil); b.pair.Writer.Reset(nil) }
}
