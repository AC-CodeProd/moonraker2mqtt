# Bounded WebSocket frame draining

Local copy of `golang.org/x/net/websocket` **v0.46.0**, BSD license in LICENSE.
The normal hosted build still uses upstream. TinyGo and the `lowmem` test tag
use this package through `../wire_tinygo.go`.

Changes versus the pinned upstream source:
- Remove the canonical import comment from websocket.go.
- Add one 256-byte discard buffer to each Conn.
- Replace the three `io.Copy(io.Discard, ...)` frame/trailer/control drains
  with `Conn.drainFrame`; add a no-progress guard.
- In Codec.Receive, allocate exactly the checked hybi payload length and use
  `io.ReadFull`, instead of growing `io.ReadAll` buffers. Keep `io.ReadAll` for
  other frame readers. Allocate per receive because codecs can retain slices.
- Add opt-in `Codec.ReceiveBorrowed`: a caller-owned reservation under the receive
  lock, no per-frame payload allocation, with an explicit no-retained-alias contract.
  Generic `Receive`/`Message` retain their independently owned storage.
- Keep upstream tests; add drain/allocation, exact-capacity and borrowed receive regressions.

Known-length truncated payloads now fail with `io.EOF` (no payload bytes) or
`io.ErrUnexpectedEOF` (partial payload), without invoking the codec. Frame limits,
control handling and upstream per-frame fragmentation semantics are unchanged.
The hosted wire adapter still imports upstream and is unaffected.

The MCU Moonraker client alone uses `ReceiveBorrowed` with a statically reserved
16 KiB payload and a mutex covering receive + JSON decode. This is reserved in
static RAM before startup/radio initialization, not allocated on a fragmented
heap after connection. Only the freshly zeroed `WebSocketMessage` schema is
allowed: standard JSON owns decoded maps/slices/strings and there are no custom
unmarshalers. Asynchronous handlers therefore retain decoded values, not the
wire buffer. Multiple MCU streams serialize on the same reservation; the
firmware is designed for one Moonraker stream, not concurrent independent clients.
Unknown-length readers cannot use the bounded borrowed API. The configured
16 KiB frame limit and upstream per-frame fragmentation behavior are unchanged.

Generic codecs still need an independently owned contiguous payload allocation.
JSON/maps/strings, tasks and radio/network allocations can still exhaust the heap.
Static reservation consumes 16 KiB of heap headroom; compiler figures and local
tests are not a peak-memory budget or hardware OOM qualification.

Why: the exact ESP32-S3 fatal allocation was 8192 bytes from `io.init$1`,
the New function of `io.blackHolePool`. There were 58176 free bytes after GC
but the largest contiguous range was 5056 bytes. io.Discard.ReadFrom obtains
its 8 KiB buffer before even discovering that the previous frame is exhausted.

This removes that large transient request. It does not claim to fix all
fragmentation, low-memory paths, or every protocol behavior inherited from upstream.
On dependency upgrades, rebase these small changes and run upstream tests,
lowmem integration tests, TinyGo builds and a read-only hardware soak.
