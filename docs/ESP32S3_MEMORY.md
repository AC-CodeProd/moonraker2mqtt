# ESP32-S3 memory failure: bounded WebSocket draining

## Observed cause

A read-only qualification firmware loaded the saved settings, obtained DHCP,
connected MQTT and Moonraker simultaneously, and published the initial information.
It then stopped with `fatal error: out of memory`.

An additional diagnostic run, using a private instrumentation-only copy of the
TinyGo 0.42.0 allocator, measured the **failing allocation**, after GC:

- requested: 8192 bytes; rounded allocation including allocator overhead: 8208 bytes;
- total free memory: 58176 bytes; largest contiguous range: 5056 bytes;
- caller: windowed Xtensa return address `0x820a0524`, normalized to `0x420a0524`;
- the matching ELF disassembly identifies `io.init$1`, the `io.blackHolePool.New`
  callback allocating an 8192-byte scratch buffer for `io.Discard.ReadFrom`.

This is a demonstrated fragmentation failure, not evidence that every byte of RAM
was in use. The old WebSocket codec calls `io.Copy(io.Discard, previousFrame)`
before reading the next frame, including when the previous frame is already exhausted.

## Correction

- Normal hosted builds retain `golang.org/x/net/websocket` v0.46.0.
- TinyGo uses a small, licensed local copy of that pinned WebSocket package,
  selected by `websocket/wire`. Its three discard paths use a **256-byte buffer
  stored in each connection**, accessed while the existing receive lock is held.
- The replacement directly reads into that buffer; it does not invoke
  `io.Copy`, `io.Discard.ReadFrom`, or their 8 KiB pooled allocation.
- The `lowmem` build tag lets hosted tests exercise the same transport and firmware
  queue limits without importing the physical radio.
- No forced GC, PSRAM initialization, linker heap enlargement, or runtime patch
  is required by the fix. The allocator patch was diagnostic-only, outside this
  repository; the corrected firmware uses the original TinyGo runtime.

`platform/esp32s3/memory_tinygo.go` is optional counters-only instrumentation,
enabled with `-X moonraker2mqtt/platform/esp32s3.MemoryDiagnostics=true`.
It introduces no goroutine and defaults to disabled. Do not treat its counters as
precise live-object measurements: the TinyGo heap counter includes garbage until GC.

## Verification and limits

On October 5, 2026, the corrected read-only diagnostic image completed a
300.13-second real MQTT/Moonraker capture without a fatal error, panic, reset,
or reported periodic publication error. Initial connectivity and publication
passed. The trace recorded 147 completed GC cycles and continued until the end
of the capture. The image was verified by exact flash readback; both reserved
settings sectors and all sectors outside the image remained unchanged. The saved
settings were also read back unchanged after execution.

The same image subsequently passed a **full one-hour stability window** on
October 5, 2026, from 10:42:01 to 11:42:01 Europe/Paris (3600.09 seconds after
initial publication). The image was verified against flash before execution.
The capture recorded 3033 session memory samples, 1811 completed GC cycles,
no fatal error, panic, unexpected restart or reported periodic publication error.
Memory counters continued until the end; the result is not merely an hour of
host-side waiting. The reserved settings were read back byte-for-byte unchanged
before and after the run. This final read left the device stopped in the ROM
bootloader, with the corrected read-only image still installed.

HeapAlloc ranged from 217728 to 276880 bytes across samples; the final sample
reported 243760 bytes allocated and 33120 bytes idle. One sample reported zero
idle bytes before later reclamation. Thus the observed workload passed, but RAM
headroom remains tight; do not infer spare contiguous space, absence of all leaks,
or safety of larger JSON payloads from total-free counters alone.

A subsequent requested three-hour run on October 5, 2026 started its stability
window at 17:38:29 and failed after only 23.31 seconds, with another
`fatal error: out of memory`. The image hash matched the one-hour-qualified
candidate, and settings were again read back unchanged. The device was stopped
in the ROM bootloader after verification. The one-hour result remains historical
evidence, but it does not establish general memory robustness. The allocation
responsible for this later fatal has not been localized. A separate read-only
Moonraker probe at 17:40 reported `printing`; no printer control request was sent.
Printing during the failed 23-second capture itself was not independently recorded,
so the failure must not be attributed to printing without more evidence. Private
failed-run evidence: `/home/ia/moonraker2mqtt-hardware/oom/soak-3h-print-20261005T173751/`.

This is a one-hour qualification of the existing read/status workload, **not**
printing-load, injected-disconnect/reconnect, cold-power-cycle or multi-day
qualification. Other large allocations, JSON payloads, failure/reconnection paths,
and the previously observed ROM SHA warning remain separate concerns. Commands
and settings writes remained inhibited throughout. Private hour-test evidence:
`/home/ia/moonraker2mqtt-hardware/oom/soak-1h-20261005T104141/`.

Local private evidence (not for publication; may contain secrets):
`/home/ia/moonraker2mqtt-hardware/oom/{allocation-probe,bounded-drain}/`.
See `websocket/wire/lowmem/README.md` for provenance and the upstream-rebase policy.

## Measured reconnect-stack failure and MCU session recovery

A separate matching-image allocator probe at
`/home/ia/moonraker2mqtt-hardware/oom/ping-allocation-probe/firmware.elf`
localized another fragmentation failure after GC:

- requested 8192 bytes, rounded to 8208 bytes;
- total free 62368 bytes, largest contiguous range only 3632 bytes;
- caller `0x8208f0c1`, normalized to `0x4208f0c1`;
- matching disassembly identifies `internal/task.start`,
  `task_stack.go:39` (`initialize`), loading `0x2000` and calling
  `runtime.alloc` at `0x4208f0be`.

The last bridge warning was `Moonraker disconnected, attempting reconnection...`
(the former `bridge/app.go:181-185` path). A new goroutine-stack allocation during
reconnection is therefore demonstrated; which reconnect worker requested that
stack is **not** established. This is distinct from the earlier measured
`io.Discard` buffer allocation. Neither lowering stack size nor changing
runtime/PSRAM configuration is part of this correction.

TinyGo builds now terminate an active bridge session when periodic monitoring
observes MQTT or Moonraker disconnected, **before either reconnect Connect call**.
The `lowmem` tag exercises this MCU policy offline. Monitoring runs synchronously
inside the MCU session runner: its error returns through `bridge.Run`, including
Moonraker then MQTT deferred cleanup, rather than just stopping a ticker and
leaving Run blocked on a healthy Wi-Fi context. Normal hosted builds retain their
existing reconnect branches, cooldown, and context-driven shutdown.

The existing ESP32 supervisor was inspected, not replaced:
`platform/esp32s3/runtime.go` passes `app.Run` through `network.Session`, with an
additional MQTT cleanup defer for partial connects. `network.Session` waits for
the callback to return. The supervisor still resets on shutdown timeout and,
following a normal session return, performs its bounded five-second backoff,
invalidates pending RTC save data, and resets. It does not reuse the old radio
stack or clients. Command/qualification safeguards are unchanged.

Offline adapter regression tests drop MQTT, Moonraker, and both after initial
publication. Under MCU policy they require one Connect per protocol, a protocol
error while the parent context remains live, and both cleanup defers plus callback
return before the supervisor continuation point. Hosted tests require the existing
second Connect and cancellation behavior; healthy cancellation is also covered.
The MCU cases were observed failing before the policy change and passing after it.
These are mocked-protocol/session tests, **not a physical reset qualification**.

Controlled reset means recovery **by reboot**, with interruption of the session.
It is not multi-hour operation without lost connections and does not prove that
other allocation paths cannot OOM. Loss detection occurs at the polling interval;
a request already in progress may delay detection, and the existing supervisor
shutdown-timeout reset remains necessary because every transport worker is not
joined by Disconnect. Initial WebSocket AutoReconnect remains a separate path;
this change does not eliminate every possible worker allocation during startup.

### Hardware result for the session-recovery candidate

On October 5, 2026, candidate SHA-256
`d57fd065807f863295b74d8ffa63a534bb03b54feefabec135104645407d8b62`
completed 1800.05 seconds after both connections and initial publication, from
21:00:01 to 21:30:01 Europe/Paris. It used the original TinyGo 0.42.0 allocator,
with read-only qualification and memory counters enabled. The capture recorded
1530 memory samples, zero fatal/panic, zero unexpected restart, and zero periodic
publication errors. No session-recovery event occurred, so this run validates
30 minutes of uninterrupted traffic, **not the reset/recovery branch on hardware**.

Exact image verification and pre/post stored-settings equality passed. The final
sample reported 236896 allocated and 39984 idle bytes; these are heap counters,
not a guarantee of contiguous allocation headroom. The postrun check left the
board in the ROM bootloader with this candidate installed. Printer commands and
settings writes remained inhibited. Printing load, injected disconnections,
repeated controlled recovery and multi-hour stability remain unqualified.
Private evidence: `/home/ia/moonraker2mqtt-hardware/oom/session-recovery-candidate/`.

Regression commands (offline dependency cache):

```sh
export GOPROXY=off GOSUMDB=off
go test ./...
go test -tags=natiu ./...
go test -tags=lowmem,natiu ./...
go test -count=20 ./websocket/wire/lowmem
go vet ./...
go vet -tags=lowmem,natiu ./...
```
