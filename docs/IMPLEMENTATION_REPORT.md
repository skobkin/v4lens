# V4Lens MVP — Implementation Report (Phase 7)

Implementation of the V4Lens MVP per `AGENT_TASK.md`, following `DECISION_LOG` >
`UI_UX_SPEC` > `ARCHITECTURE` > `PRODUCT_SPEC` > `MVP_SCOPE` > `ACCEPTANCE_CRITERIA`.
Eight commits, one per AGENT_TASK.md phase. All development-time quality gates
(`gofmt -l .`, `go test ./...`, `go vet ./...`, `golangci-lint run ./...`,
`go build ./...`) are green at commit time. Real-device verification was run
against a local UVC camera (AVerMedia PW513, uvcvideo, single-planar); all
machine-specific identifiers are redacted below — synthetic values only
(e.g. serial `TEST-PW513-0001`, key `usb-<vendor>_<model>_<synthetic>-<hash>`).

## 1. Architecture summary

The implemented package layout matches ARCHITECTURE §1:

```
cmd/v4lens/            bootstrap: flags, single-instance lock, activation server, wiring
internal/app/          orchestration + typed event bus + startup/quit + apply-on-start
internal/domain/       normalized types (ControlValue, FrameLayout, CaptureConfig, ...)
internal/v4l/          the ONLY go4vl importer; local ioctl wrappers for the control path
internal/device/       Handle/Stream/Session seams, sysfs-first identity, scanner, fakes
internal/device/fakebackend/  scriptable fake backend (flags, deps, busy, MPLANE, strides)
internal/profile/      YAML store, apply engine (exclusive mutations), dirty diff
internal/preview/      decoder registry + 6 decoders + latest-frame pipeline + manager
internal/config/       YAML preferences
internal/platform/     XDG paths, flock single-instance, activation server, autostart
internal/logging/      slog manager (XDG state file, 0600, stderr degradation)
internal/ui/           Fyne windows/tabs/renderers + Fyne-free viewmodels
internal/resources/    tray icon
internal/probe/        build-tagged (`probe`, `integration`) real-device probes/tests
```

Key architectural points as implemented:

- **Identity is sysfs-first, never `/dev/videoN`.** Enumeration walks
  `/sys/class/video4linux`, classifies each node (QUERYCAP via the backend),
  groups all endpoints of one physical device, and resolves the persistent key
  by-id → serial+VID/PID → by-path → sysfs fingerprint. Profiles match on the
  identity key and survive disconnection.
- **fd-ownership model (D013).** The long-lived control fd stays in panel mode
  (controls, capability queries, G_* reads, TRY_FMT only; it never mutates
  capture configuration, S_INPUT is never issued for discovery). Capture
  transactions always run on the fd that owns the stream: with no own stream a
  short-lived detached fd performs S_INPUT/S_FMT/S_PARM + read-back and closes;
  with an own preview the pipeline suspends (releases the stream fd, emits
  `Frozen` with the retained last frame) and the reopened stream fd applies the
  whole transaction at open.
- **Session lanes.** One worker goroutine per device executes short ops;
  profile applies and format applies run as exclusive multi-step mutations;
  manual/coalesced writes queue behind them and can never interleave mid-apply.
  MODIFY_LAYOUT-flagged controls route through the mutation path.
- **Preview pipeline.** Capture goroutine copies each MMAP frame
  (copy-then-Release), decodes into a 3-slot publication ring (a slot is only
  refilled three publications later; MJPEG allocates fresh immutable frames),
  publishes the latest slot via atomic swap, and a render ticker (~30 fps)
  republishes on sequence change. A generation counter (`gen`) invalidated by
  both suspend and stop keeps superseded loops from emitting stale events.
- **Reconnect semantics.** Devices present at first scan are initial discovery
  (apply-on-start only). A device seen connected, then gone, then back gets
  exactly one identity-matched default-profile apply, with per-key cooldown on
  failure notifications.
- **Truthful values everywhere.** Writes return `WriteResult{Accepted,
  Effective}`; the requested value is never disguised as read-back. Applied
  capture state is always the driver-read-back effective config. Save persists
  effective (read-back) state and only profile-managed controls.

## 2. Decoder & backend choices (measured)

Full comparison in DECISION_LOG D013 (Phase 0). Outcome: **pure-Go decoders,
no libyuv, no Pion `pkg/frame`** in the product build. libyuv is ~10× faster on
raw YUV but raw YUV is not the bottleneck (MJPEG is), has no pkg-config/Alpine
story, and has no MJPEG entry point; Pion was slower than plain Go for packed
YUV and adds a cgo dependency for nothing.

Live re-measurement on the real device during Phase 7 (`TestRealPreviewPerf1080`,
native non-race build, decode only):

| format (1920×1080 live) | mean decode/frame | decode errors | verdict vs ~33 ms budget |
|---|---|---|---|
| MJPG | 16.6 ms | 0 | comfortable (≈60 fps headroom) |
| UYVY | 2.2–2.3 ms | 0 | comfortable (≈430 fps headroom) |

(Under `-race` the same decodes measure 10–30× slower; the wall-time gate is
therefore asserted in native runs only, via `raceEnabled` build-tag constants.)

go4vl v0.5.0 is used only where the Phase 0 probe validated it (QUERYCAP,
ENUM_FMT, frame sizes/intervals, MMAP streaming). The control tree uses local
`QUERY_EXT_CTRL`/`G_EXT_CTRLS`/`S_EXT_CTRLS` ioctl wrappers with runtime-asserted
struct layouts, because go4vl misses user-class controls and truncates int64
metadata (D013).

## 3. Tests run

- **Unit (every commit):** `go test ./...` — 11 packages with tests; also
  `go test -race ./...` clean. Coverage highlights: identity chain incl.
  dual-node grouping on a synthetic PW513-shaped fake sysfs tree; coalescer
  newest-wins/200 ms/Commit/Flush/Reset with injected clock; session mutation
  exclusivity (manual write can never interleave); store YAML round-trips with
  preserved value types and exact rational intervals; apply engine full/partial/
  EBUSY-continue/bounded-retry; format cascade incl. stepwise/continuous;
  preview pipeline latest-slot/immutable-publication/frozen-retention/
  unsupported/stop semantics (race-oriented); decoders golden pixels incl.
  driver-stride/padding variants; platform XDG/lock/activation/autostart;
  tray menu structure via the Fyne test driver.
- **Integration (build tag `integration`, dev machine):**
  - v4l2loopback-based external-owner EBUSY scenario (Phase 2–6 suites).
  - `internal/probe` real-device suite (below).
  - `internal/ui` real-device GUI sequence (`V4LENS_GUI=1`): drives the real
    Fyne UI against the real camera — live preview, format apply with preview
    open (Frozen → resume), deterministic canvas captures.
- **Real-device suite** (`go test -tags integration -run TestReal ./internal/probe/`),
  all passing (race and native runs):

| test | what it verified on hardware |
|---|---|
| `TestRealIdentityAndNodeGrouping` | by-id identity key (never a node path), capture+metadata nodes grouped under one identity, stable across discovery passes |
| `TestRealControlTree` | 18 controls enumerated with faithful int64 min/max/step/default; INACTIVE flags visible; read-back within advertised ranges |
| `TestRealControlDependencyUnlock` | disabling WB-auto cleared INACTIVE on WB-temperature (bounded retry), in-range write accepted with exact read-back (6400 → 6400), ordered restore |
| `TestRealPreviewPerf1080` | MJPG + UYVY live 1080p capture+decode, 0 errors, decode-time gates (numbers above), device state restored |
| `TestRealExternalOwnerObservation` | while `v4l2-ctl --stream-mmap` streamed: detached reconfigure → **EBUSY** (recorded, device untouched); own stream open → **EBUSY**; external owner finished undisturbed |
| `TestRealProfileApplyWithOwnPreview` | full orchestration: exclusive mutation → preview **Frozen** (frame retained) → detached configure → preview **resumed** with the new layout → effective read-back matches → original format restored |
| `TestRealReconnectOneShotApply` | **SKIP (honest):** soft-unplug needs write access to the USB `authorized` sysfs file (root); the physical-replug equivalent was exercised interactively during development: removal → Disconnected → reappear → exactly one default-profile apply. Recorded as integration-verified, not scripted-real-device-verified |

- **GUI verification on the live desktop** (computer-assisted, plus scripted
  sequence): main window renders context bar (device select with the real
  device, profile select, Save/Preview/overflow), four tabs, semantic groups
  (Exposure / Image / White Balance) with faithful values, slider+entry pairs,
  menu selects with driver labels ("Auto Mode", "50 Hz"), INACTIVE hint
  ("inactive — enable its prerequisite control first") on gated controls, footer
  showing the identity key. Preview window: live camera frames with status line
  `● Previewing · MJPG · 1280×720 · 30 fps`. Lifecycle: closing the main window
  leaves the process resident; a second launch exits 0 with "activation request
  accepted" and the first instance raises its window; Quit/SIGTERM shuts down
  cleanly (single "ui exited" log line).

## 4. Defects found & fixed during verification

1. **Data race in `v4l.Stream.Stop`** (found by `-race` in the real-device
   suite): Stop closed the frame/error channels while the consume goroutine
   could still be sending. Fixed with sender-owned closure — `consume()` closes
   both channels on exit; Stop cancels the context, closes the device, and
   waits for consume. Race-clean under repeated `-race` runs.
2. **UI missed initial discovery** (found by launching the real GUI): the
   window subscribed to the event bus *after* `app.Start()` had already run
   initial discovery, so `DevicesChanged` was never seen (the scanner only
   publishes on later changes) and the UI stayed at "No device". Fixed by
   starting the application layer after the UI subscription and pulling the
   current device list once after subscribing.
3. **Stale preview events after suspend** (found by the real-device apply
   test): the render loop did not know its generation and could emit a
   superseded `Streaming` event after the `Frozen` one. Fixed with a
   generation check before publishing; the apply test now identifies the
   resumed generation by layout.
4. **Main-window close quit the whole app** (acceptance walkthrough):
   close now hides the window and releases streaming lanes while the process
   stays resident in the tray; only the tray Quit action (or SIGTERM) exits.

Phase 7 also produced new recorded device knowledge (D014): the camera's
`auto_exposure` menu advertises 0..3 but accepts only the two QUERYMENU-enumerated
items (0 Auto, 2 Shutter Priority; 1/3 return EINVAL), and `exposure_time_absolute`
stays INACTIVE even in Shutter Priority on this firmware — the WB pair is the
classic dependency-unlock case. QUERYMENU-based menu expansion in the backend is
therefore load-bearing for the UI (it offers only accepted values).

## 5. ACCEPTANCE_CRITERIA matrix

Legend: **U** unit-tested · **I** integration-tested (`integration` tag) ·
**R** real-device-verified · **—** still-unverified.

### Application lifecycle
| criterion | status |
|---|---|
| one resident tray application per user | U (flock contention) · R (launch, tray, lock) |
| second copy forwards activation, window raised | U · R (exit 0, "activation request accepted", window reappeared) |
| `--background` starts without main window | R (Phase 6 smoke: 8 s windowless, alive) |
| closing main window leaves resident app | R (Alt+F4 → process alive) — fixed this phase |
| quit shuts down workers/resources cleanly | R (Quit/SIGTERM → single "ui exited", clean teardown) |

### Device discovery/identity
| criterion | status |
|---|---|
| enumeration without hardcoding `/dev/video0` | U (fake sysfs) · R (discovery of the real dual-node camera) |
| persistent identity survives `/dev/videoN` renumbering | U (identity chain, node re-resolution) — replug renumbering not scripted on real HW |
| profiles listed while device disconnected | U |
| serial-less fallback never matches arbitrary node | U (fingerprint/by-path fallback tests) |
| multiple physical devices coexist in selector/tray | U (fake backend multi-device) — single real camera on this machine |

### Controls
| criterion | status |
|---|---|
| dynamic controls from device descriptors | R (18 real controls incl. camera class) |
| semantic grouping of known controls | R (Exposure/Image/White Balance groups on screen) |
| unknown safe controls in Advanced | U (semantics table fallback, fake backend) |
| compound controls visible as unsupported | U (fake compound descriptors) |
| bool/menu/integer/button/string/read-only → widgets | U · R (bool, menu, integer, read-only on real device) |
| integer sliders have exact numeric field | R (bidirectional slider+entry on screen) |
| no ioctl per mouse event; ~1 write/200 ms newest-wins | U (coalescer + injected clock) |
| `OnChangeEnded` commits immediately | U |
| auto/manual refreshes dependent inactive/enabled controls | U · R (WB pair unlock on device; INACTIVE hint shown) |

### Profiles
| criterion | status |
|---|---|
| YAML under persistent per-device dir | U (atomic write, perms, layout) |
| per-device default profile | U |
| selecting profile applies immediately | U · R (apply from UI path) |
| profile = input+format+resolution+FPS+controls | U (type-preserving YAML, exact rational interval) |
| successful values remain applied if another fails | U (fakebackend per-id failures) |
| structured partial-apply details | U (ApplyResult → dialog/notification) |
| effective read-back → truthful dirty state | U |
| Save stores effective state | U |

### Format
| criterion | status |
|---|---|
| tab label `Format` | U (Fyne test driver) · R (visible) |
| choices reflect real capabilities | R (real format/size/interval trees) |
| unsupported combinations not valid choices | U (cascade + lazy intervals) |
| staged until Apply; Revert restores active | U (viewmodel) |
| external EBUSY reported without disrupting | U (fakebackend) · R (v4l2-ctl owner: EBUSY recorded, owner finished) |

### Preview
| criterion | status |
|---|---|
| separate window | R |
| MJPEG, YUYV/YUY2, UYVY, NV12, NV21, I420 | U (golden pixels incl. stride variants) · R (MJPEG, UYVY live; YUYV not offered by this camera; NV12/NV21/I420 unit-level only) |
| unsupported FOURCC reports, blocks nothing | U (H264 → Unsupported, stream never opened) |
| bounded queues / latest-frame behavior | U (drop-oldest, latest-slot, immutability race test) |
| no UI backlog at 60 FPS input | U |
| own-restart keeps window + last frame + overlay | U · R (Frozen event, retained frame, overlay rendered; live pixel capture of the transient overlay not obtained — see §6) |
| preview resumes after restart | R (resumed with the new effective layout, both scripted and in the GUI) |

### Tray / Preferences
| criterion | status |
|---|---|
| no app-name header spacer | U (menu structure test) |
| Open + Preview exposed | U |
| one device → direct; many → submenu | U (0/1/n cases) |
| per-device profile submenu | U |
| `Start with system` / `Apply profile on start` checkboxes | U |
| Preferences mirrors settings + logging | U (preferences window tests) |
| autostart only `$XDG_CONFIG_HOME/autostart/v4lens.desktop` | U (content, perms, never under XDG data) |

### Logging / failure handling
| criterion | status |
|---|---|
| file logs under `$XDG_STATE_HOME/v4lens/` | R (log file observed in use) |
| levels debug/info/warn/error | U |
| log failure does not prevent startup | U (stderr degradation) |
| background partial failures notify without spam | U (cooldown) |
| disconnect/failure does not crash the tray app | U (scanner/session tests) · R (device held by external owner during tests, no crash) |

### Code quality / CI
| criterion | status |
|---|---|
| strict golangci-lint v2 config | present, 0 issues |
| Woodpecker CI fmt/lint/vet/test/build | pipeline committed (observed only on push, per plan) |
| gofmt/vet/test/lint pass | R (all green at commit time) |
| baseline CI needs no real camera | U (fakebackend-only suite runs anywhere) |
| logic testable via fakes | U |
| no packaging/AUR in MVP | confirmed |

## 6. Known limitations & honestly-unverified points

- **Frozen-overlay pixel capture.** The transient overlay (~0.3 s on this
  hardware) was verified by state events and unit/UI-driver tests; automated
  pixel capture failed for tooling reasons: Fyne `Canvas.Capture()` reads back
  black for compositor-occluded windows on this KWin/Wayland setup, and the
  remote-screenshot latency exceeds the overlay duration. A human watching
  `V4LENS_GUI=1 go test -tags integration ./internal/ui/` sees it; a manual
  observation is recorded as the remaining unverified pixel.
- **Reconnect one-shot apply** is covered by unit tests (fake scanner
  lifecycle) and was exercised interactively (physical replug); the scripted
  soft-unplug test skips without root (`/sys/.../authorized` unwritable) and
  is left for a root/prepared run.
- **`/dev/videoN` renumbering** after a replug is covered at unit level
  (identity re-resolution); no second camera existed to force a real renumber.
- **NV12/NV21/I420/YUYV decoders** are golden-tested but not exercised against
  real hardware (the available camera offers MJPG/UYVY only).
- **Woodpecker CI** has not been executed — per the approved plan, the pipeline
  is observed only if/when the repository is pushed.
- **Tray icon visibility** on this KDE session was not pixel-verified (system
  tray area not resolvable by the automation); the `desktop.App` tray code path
  runs without error and the menu structure is unit-tested.
- **MJPEG decode at 1080p (~16–21 ms)** is within budget but dominates the CPU
  cost of preview; if a future target device is slower, the D013 libyuv
  re-evaluation clause applies.
- uvcvideo has no per-fd format isolation (D013): a detached reconfigure
  mutates the device-global format. The architecture's cooperation rules
  (suspend own preview; record external EBUSY) are the mitigation and were
  verified on hardware.

## 7. Decision Log additions

- **D014 (this phase):** real-device verification findings — sender-owned
  stream-channel closure (race fix), UI subscribe-before-start ordering,
  generation-checked render publication, close-hides-not-quits; PW513
  `auto_exposure` menu accepts only enumerated items (QUERYMENU holes are
  authoritative for the UI); exposure stays firmware-gated even in Shutter
  Priority on this firmware; live 1080p decode re-measurements (MJPG 16.6 ms,
  UYVY 2.2 ms).
