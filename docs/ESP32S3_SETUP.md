# ESP32-S3 setup candidate / candidat de configuration

## Outcome / résultat

The sections and artifact table below retain the **earlier setup-candidate
baseline**, not the current device state. Since that baseline, persisted real
settings and Wi-Fi-only diagnostic evidence exist; those settings must be
preserved and treated as production-sensitive. This lifecycle correction is
compile/test-only: no serial access, flashing, device traffic, commit or push.
See the radio lifecycle section for the new local builds and outstanding gates.

Earlier baseline:

TinyGo-only application, with a small C/assembly ROM boundary compiled by
TinyGo (not an IDF/Arduino firmware or second processor). Portable tests and
real S3 builds pass. The device's proposed settings sectors were read **without
flash writing** and contain 8192 erased bytes. New candidate firmware has NOT
been installed and neither RTC retention nor physical programming is qualified.
Linux application sources and dependency behavior are unchanged. No commit/push.

## Practical architecture

1. Boot: read pending RTC staging, accept only software-system-reset reason 3,
   validate CRC/length/settings/operation, invalidate pending before any write.
2. If pending exists, save to inactive NOR sector, verify body, program separate
   commit word, verify again. Do not erase corrupt/unsupported records silently.
   An explicitly confirmed repair instead scans first, refuses healthy/blank or
   unreadable storage, erases/verifies ONLY slots 0 and 1, then initializes a new
   generation-1 record. A failed operation stops immediately; staging is already
   invalidated and cannot automatically retry.
3. Load the latest committed valid record. Prefer it to optional link-time settings.
4. Seal ALL backend operations for the remainder of that boot before creating
   radio. Start either STA bridge or password-protected AP + bounded HTTP page.
5. Portal POST validates/stages only; queue HTTP 202, wait for the complete TCP
   response to be acknowledged, close socket, then software reset. ACK drain is
   bounded to 2 seconds and the whole request/write/drain to 8 seconds. Any
   write/short-write/drain/close failure cancels the RTC publish marker and does
   NOT reset; retry requires a new authenticated, explicitly confirmed request.
   Flash writes occur at step 2 of the next boot, never radio-live.

The pinned xnet listener exposes `LnetoConn()`. Its `Write` only queues bytes,
`Close` only initiates TCP close, and `Flush` checks **unsent** bytes only, not
sent-but-unacknowledged bytes. The portal captures the empty `FreeOutput` capacity
before any write and waits until it is restored (both queues ACK-drained), while
checking connection state and the hard deadline. The 1ms polling yield lets
the adapter's separately started `RecvAndSend` goroutine continue;
it is not a fixed delay treated as proof of delivery. An ACK proves TCP receipt,
not browser rendering or durable flash. HTTP 202 remains a staging-only message;
boot result and cold reload still require verification.

FR : le portail attend les ACK de toute la réponse TCP avant le redémarrage
(2 secondes maximum, requête complète limitée à 8 secondes). Une écriture courte,
une erreur, une déconnexion ou un délai dépassé annule le marqueur RTC et interdit
le redémarrage automatique. Il faut soumettre une nouvelle requête authentifiée
et, pour la réparation, confirmer à nouveau l’effacement. Un ACK ne prouve ni
l’affichage dans le navigateur ni l’enregistrement durable.

**New geometry solution:** reserve `[0x1fe000, 0x200000)` inside the ROM's
2MiB legacy limit, while explicitly declaring the actual 4MiB in the image.
No adjustment of ROM chip-size pointers or copied C3 driver is needed.
The upper 2MiB remain untouched; linker/image length assertions exclude the two
sectors from firmware. This is a board-specific layout, not an IDF partition
layout. The original backup and connected board both had these sectors erased.

RTC slow-memory staging is `[0x50001000, 0x50002000)`, 4096 bytes, NOLOAD.
ELF proves it lies outside `_sbss.._ebss`, heap/GC globals and loaded image
segments. TinyGo startup clears BSS only; inspected pinned espradio startup does
not clear the RTC staging region. Actual ROM/reset retention still needs the
scoped device test. A power loss before durable commit can lose a pending form;
the previous committed slot is retained. An initial interrupted save with both
slots invalid requires explicit recovery, not automatic evidence destruction.

## Explicit recovery and build controls

Normal `/settings` saves refuse corrupt, unsupported or ambiguous records. In
recovery mode the page requires replacement settings, an unchecked-by-default
two-slot erase checkbox, and a second browser confirmation. `/repair` requires
the same Host/Origin/session token, bounded validated JSON, and exact
`X-Setup-Repair: erase-two-settings-slots` confirmation; it never writes flash
while radio is live. The RTC format covers both operation and payload with CRC,
rejects unknown operations, and accepts only software-system reset. Changing a
valid save operation into repair without updating its CRC is rejected. This is
corruption detection, not cryptographic authentication of physical RTC memory.
Only explicitly confirmed repair discards unsupported versions. Preserve/read
both sectors first if their evidence matters. An interrupted repair is inherently
destructive and can lose both old records; no rollback promise is made. Backend
I/O/readback failure is not repair authorization and returns unavailable.

`ForceSetup=true` is permanent link-time behavior, not a consumed latch. Saving
does not restore bridge mode. Rebuild/reinstall with `ForceSetup=false` or no
override to resume the bridge, preserving `[0x1fe000,0x200000)` during replacement.

Both Make firmware rules honor `TINYGO_TARGET`. The default local target gets
generated local paths; explicit custom targets pass through. Overrides must
preserve the exact custom linker, RTC/settings reservations and extra IRAM
vectors, and remain compatible with this 4MiB board and guarded driver. Generic
S3 targets are not a substitute. Docker paths refer to the container filesystem.

## Cache-off boundary

- Guards: exact XMC JEDEC `0x464016`, S3 rev0.2 read-only eFuse fields, known
  ROM table identities and all ten null flash callbacks, supported legacy
  geometry/page/sector/block sizes, security disabled, core1 held reset, no
  valid SPIRAM mappings in the full 512-entry MMU table.
- Buffers are aligned internal DRAM. Only ROM calls and checked IRAM literals
  occur inside the 178-byte cache-off wrapper. The S3 cache-FSM wait implements
  the official CACHE_SUSPEND_WAITI workaround before flash operations.
- Temporary IRAM VECBASE delegates window spill/underflow and AllocaCause to
  TinyGo IRAM routines; every other exception/high-priority interrupt resets
  through MMIO, with no printf/Go/IROM exception dependency. Normal espradio
  handler is restored only after both caches resume.
- This closes the *visible* handler dependency and guards observed ROM defaults;
  it is not a physical erase/program, brownout, watchdog or universal-ROM safety
  certificate. ROM/FSM busy waits remain potential hardware-fault hangs.

Sources checked: pinned TinyGo 0.42.0 startup/linker/vectors, pinned espradio
`9a37b24578da`; Espressif ESP-IDF v5.4.2 `components/esp_rom/patches/
esp_rom_cache_esp32s2_esp32s3.c`, `components/soc/esp32s3/register/soc/efuse_reg.h`,
`soc.h` and the exact S3 rev0 ROM ELF previously checksum-audited in the hardware
workspace. No gridpixel implementation was copied.

## Actual evidence

### Buffered-response/reset fix (current candidate)

- Pre-fix control-flow overlay and real pinned `tcp.Conn` reproduced **both**
  save and repair returning `accepted=true` with **446 bytes still unsent**.
  After the fix, fragmented responses and partial ACKs cannot authorize reset;
  even a successful real `Conn.Flush()` cannot unblock until the final ACK.
- Real-stack regression tests cover unsent timeout, sent-but-unacknowledged
  timeout, peer RST, write error, short write, close error, RTC cancellation,
  explicit retry and one-shot handoff for both normal save and confirmed repair.
  Timeout cases complete in about 2 seconds, not an indefinite wait.
- `go test ./...`: **107** passing test/subtest events; matching natiu suite:
  **114**. Both vets, Linux build/version, three target-rule Python tests and
  `git diff --check` pass. No Linux source modifications for this correction.
  Race-detector attempt is blocked by this host's ThreadSanitizer VMA mismatch
  (`Found 47 - Supported 48`), not a test result; native compatible CI is needed.
- TinyGo Docker **0.42.0** real builds, reserved-layout S3 target:
  unconfigured flash **1,298,395**, static RAM **158,636**;
  configured (commands enabled) flash **1,298,855**, static RAM **158,756**.
  Both ELF IRAM/vector/ROM-literal/RTC audits and bounded 4MiB image checks pass.
- New artifacts: `build/response-race/{unconfigured,configured}.elf` and `.bin`,
  separate ELF/image maps, build logs, test JSONL, `artifacts.json`, and
  `{unconfigured,configured}-audit/report.json`. Images respectively:
  **1,298,496** / **1,298,960** bytes; SHA256:
  `0d6fd38c92c7d57d800967d2e87ab9280fcada03b49fc02e5c8ccb7fef2e6ab5`,
  `c80ad4aed0121b761a94ce9724bd14aecf93cf32711b785701e0670f75b7d68f`.
  `reproduce_before.py` + `regression-before.log` preserve the failing overlay
  evidence; no existing historical artifacts were overwritten.
- No hardware/serial/flash/eFuse access, commit or push. TCP ACK tests use the
  real buffered stack with a simulated peer; radio/device/retention/flash-write
  acceptance remains unqualified and requires the separate authorized scope.

### Previous specification-review baseline (historical artifacts)

- `go test ./...`: 93 passing tests; `go test -tags=natiu ./...`: 100 passing.
  Both matching `go vet` commands, `make build`, Linux `-version`, three Python
  target-rule regression checks and `git diff --check` pass.
- Recovery host tests cover corrupt/unsupported/ambiguous normal-save refusal,
  explicit authenticated confirmation, RTC operation/CRC/reset/replay validation,
  radio-live no-access, boot-only two-slot repair and cold reload. Store tests
  refuse unconfirmed/healthy/blank/oversize repair without destructive access,
  inject failure at every backend operation, check erase readback and scoped
  partial-repair interruption. These are simulations, not device qualification.
- Browser mock verifies unchecked confirmation/cancel send no POST, confirmed
  repair sends `/repair` with the exact header and no extra JSON field, normal
  mode sends `/settings`, and inaccessible storage disables submission.
- TinyGo Docker 0.42.0: unconfigured flash **1,297,447**, static RAM **158,620**;
  configured (commands enabled) flash **1,297,899**, static RAM **158,740**.
  Both new ELFs pass the IRAM/vector/ROM-literal/RTC layout audit, and both new
  images pass the bounded 4MiB header/image check. No C/assembly driver changes.
- `firmware-local` default and explicit override recipes both actually compile
  and produce the same image hash. No local TinyGo is installed: a test-only CLI
  adapter invokes the pinned Docker compiler with matching absolute paths.
  Running the whole Make rule inside the stock container initially failed
  because that image lacks Python; host Make/Python plus the adapter passes.
- Current artifacts: `build/spec-review/{unconfigured,configured}.elf` and
  `.bin`, separate maps and `{unconfigured,configured}-audit/report.json`.
  Images are 1,297,552 and 1,298,000 bytes respectively; SHA256:
  `273e2f0efb7e5215f31a9fca10858703f9dec7e0062b8ac88f2e69ff58b48068`,
  `782340966e85f21843b846939c980031ea8e42ab13a524865017432e83d669d2`.
  Diagnostic-only passwords and documentation addresses; not deployable secrets.
  `make firmware` regenerated its generic `build/setup.map`; the older named
  ELFs/images/audit reports below were not replaced and remain historical.

### Earlier candidate baseline (historical artifacts)

- `go test ./...`, `go vet ./...`, `go test -tags=natiu ./mqtt`, Linux binary
  `-version` smoke test: pass.
- Settings, malformed HTTP/framing/Host/Origin/token checks, payload/header limits,
  secret non-reflection, pending CRC/reset/replay and NOR torn-write tests: pass.
  A simulated portal → software reboot → commit → cold boot test forbids all
  backend accesses while simulated radio is live. This is host simulation.
- Browser preview: 390px mobile / 1100px desktop, no horizontal overflow,
  no external resources, commands unchecked; UI POST shape exercised against
  an explicit browser mock. Not an AP-network test.
- S3 target compile: portal+bridge flash **1,294,415**, static RAM **158,604**;
  configured bridge with commands flash **1,294,723**, static RAM **158,700**.
- `tools/audit_settings.py`: wrapper/vector placement, 18 IRAM literal loads,
  ROM direct-call targets and RTC NOLOAD/image/BSS separation checked against
  both final ELFs. Reports and disassembly: `build/setup-audit/` and
  `build/setup-configured-audit/`.
- `tools/settings_image.py` and esptool image-info: 4MiB DIO S3 image, valid
  checksum/appended digest, four loaded segments, no RTC staging segment.
- Actual read-only preflight: `build/setup-preflight-settings.bin`, 8192 bytes,
  all `0xff`. No eFuse, flash erase or flash program was performed.

Artifacts (ignored by Git; may contain link-time secrets):

| Image | Bytes | SHA256 |
|---|---:|---|
| `build/setup-4mb.bin` | 1,294,512 | `ac4c5d25a65e3e3cc1dafab8e97d1bf9fa5ae02e7455500256c7d10e8afb1643` |
| `build/setup-configured-4mb.bin` | 1,294,816 | `2ed64e9fccf802297b9e03779b4f76a8e750d87a3829e60d6fd3277bc0429d50` |

Both example builds use a clearly diagnostic setup password, not a deployable
secret. The configured image uses documentation-only addresses `192.0.2.10`
and `192.0.2.20`, never the production broker/printer.

## Radio lifecycle correction / correction du cycle radio

The exact pinned espradio `Esplink.NetConnect` calls non-idempotent `Enable` on
 every retry and hard-codes one active TCP port. The local adapter keeps the
 pinned radio/lneto implementation, but initializes once, retries association
 and fresh DHCP, and reserves two active TCP ports plus two UDP ports. Its
 target wraps the existing STA event setter without changing upstream state;
 a retained IPv4 address is not evidence of a live association. Stack shutdown
 joins the packet pump before clearing the driver's singleton RX closure.
 Initialization/shutdown failures latch a reset-required state rather than
 start an overlapping pump. Boot stage messages precede settings load and
 flash sealing, and never print credentials.

After six pre-bridge failures, recovery enters the authenticated setup AP
without erasing records. During a bridge session, association loss cancels the
runner and allows ten seconds for cleanup. Shared WebSocket workers are not
fully joinable, so **post-bridge recovery resets instead of replacing the
socket table in place**, even when the runner returns normally. Pending RTC
markers are cancelled before this unrelated reset; durable settings remain.
This deliberately does not claim transparent in-boot bridge reconnection.

Verification artifacts are in ignored `build/radio-lifecycle/`:

- `go test ./...`: 124 passing test/subtest events; `go vet ./...` passes.
- `go test -tags=natiu ./mqtt`: 7 passing tests; tagged vet passes.
- Network regressions pass 20 repetitions, including two simultaneous Berkeley
  sockets exchanging distinct bidirectional payloads over real pinned lneto
  TCP/ARP/IP in memory, retries at each stage, stale DHCP/link loss, pump join,
  cancellation and bounded shutdown. Lifecycle fakes are not radio tests.
- Race execution was attempted but cannot start here: ThreadSanitizer reports
  `unsupported VMA range (Found 47 - Supported 48)`. No race pass is claimed.
- TinyGo 0.42.0 builds both ELF and image for the settings-reserving S3 target:

  | Build | Compiler flash bytes | Static RAM bytes | Final image bytes |
  |---|---:|---:|---:|
  | Unconfigured/setup | 1,301,683 | 158,756 | 1,301,776 |
  | Configured/commands enabled | 1,302,043 | 158,876 | 1,302,144 |

  Both use dummy setup secrets; configured endpoint addresses are documentation
  addresses only. No firmware is run by these builds. Exact hashes are in
  `artifacts.json`; no artifact is authorized for installation.
- Both ELF audits pass the existing IRAM cache-off ROM closure, temporary vector
  and RTC `NOLOAD` checks; both images remain below reserved settings
  `[0x1fe000,0x200000)`, have four loaded segments and a corrected 4MiB header
  with rebuilt digest. These are static checks, not physical qualification.
- The Linux static build and `--version` work. Target-rule Python tests pass;
  `tinygo list -target=... -deps` excludes Paho/Gorilla/YAML/dotenv, while the
  host dependency graph excludes the MCU radio/lneto/natiu graph.

FR : initialisation unique, reprises association/DHCP, deux sockets TCP actifs,
arrêt de la pompe avant remplacement et détection réelle de perte d’association.
Après le bridge, la reprise passe par un reset logiciel, car les workers
WebSocket ne sont pas tous joignables. Paramètres durables préservés, marqueur
RTC en attente annulé, aucun secret dans les nouveaux messages de démarrage.
Les deux builds et audits statiques passent ; le détecteur de courses ne peut
pas démarrer sur cet hôte. Aucun accès série, flashage ni trafic vers les
équipements n’a été effectué pour cette correction. La reprise radio réelle,
le bridge complet et les essais prolongés restent à qualifier dans un cadre
isolé, autorisé après revue ; ne pas utiliser les paramètres réels enregistrés
comme une configuration de test.

## Pending authorization / autorisation requise

The sequence below was proposed for the earlier dummy-settings baseline.
It is not authorization to boot a bridge with the real settings now persisted;
review the isolated firmware and obtain the exact expanded scope first.

Request permission for **only**: back up/read the candidate image range and the
two specified sectors; install the unconfigured candidate at image offset zero
using image-covered sectors only; verify exact flash readback; connect an
isolated test client to AP/DHCP; submit dummy configuration to exercise RTC
handoff and writes confined to `[0x1fe000,0x200000)`; read back both slots;
software-reset and cold power-cycle retention; verify normal bridge mode against
isolated local dummy MQTT/Moonraker endpoints (commands remain off). Preserve
backup/restore capability. No full erase, eFuse programming or production
printer/broker access. Physical writes must wait for that explicit scope.

The independent existing ROM SHA comparison warning remains unresolved and
must be recorded during the test. A valid image digest does not prove the ROM
boot warning has disappeared. Do not use that warning to stall portal testing,
and do not describe this new path as runtime-proven before the scoped test.

En français : le candidat est compilé et testé sur hôte, prêt à un essai autorisé.
La sauvegarde en flash et la rétention RTC sur cette carte restent précisément
les vérifications suivantes, pas des résultats déjà obtenus.
