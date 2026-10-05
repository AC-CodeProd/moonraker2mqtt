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
- Keep upstream tests; add drain/allocation and exact-capacity receive regressions.

Known-length truncated payloads now fail with `io.EOF` (no payload bytes) or
`io.ErrUnexpectedEOF` (partial payload), without invoking the codec. Frame limits,
control handling and upstream per-frame fragmentation semantics are unchanged.
The hosted wire adapter still imports upstream and is unaffected.

This removes receive-buffer growth, not JSON allocations or the need for one
contiguous payload allocation. Host tests do not establish MCU OOM recovery;
hardware qualification is still required.

Why: the exact ESP32-S3 fatal allocation was 8192 bytes from `io.init$1`,
the New function of `io.blackHolePool`. There were 58176 free bytes after GC
but the largest contiguous range was 5056 bytes. io.Discard.ReadFrom obtains
its 8 KiB buffer before even discovering that the previous frame is exhausted.

This removes that large transient request. It does not claim to fix all
fragmentation, low-memory paths, or every protocol behavior inherited from upstream.
On dependency upgrades, rebase these small changes and run upstream tests,
lowmem integration tests, TinyGo builds and a read-only hardware soak.
