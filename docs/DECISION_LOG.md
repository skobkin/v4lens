# Decision Log

## 2026-09-13 — D001 — Product identity

Use **V4Lens**. Repository/binary slug is `v4lens`; canonical Go module is `git.skobk.in/skobkin/v4lens`; Fyne application ID is `in.skobk.v4lens`.

## 2026-09-13 — D002 — Platform and toolkit

MVP is Linux-only, Go + Fyne 2.8.x, V4L2-first. Do not spend work on hypothetical cross-platform camera backends.

## 2026-09-13 — D003 — Main UI hierarchy

Device + Profile context stays visible above tabs. MVP tabs are `Image`, `PTZ`, `Format`, `Advanced`. `Advanced` starts as its own tab and can be reconsidered later without changing device/domain architecture.

## 2026-09-13 — D004 — Live numeric control UX

Integer controls use sliders plus exact numeric fields where sliders are useful. During drag, send newest live value at most roughly every 200 ms; final `OnChangeEnded` write is immediate.

## 2026-09-13 — D005 — Profiles

Profiles apply immediately when selected. Profiles include format + controls, are persisted per stable physical-device identity, and survive device absence. One default profile may be configured per physical device. Partial apply is expected behavior; do not roll back successful writes.

## 2026-09-13 — D006 — Preview

Preview is a separate window. During V4Lens-owned format reconfiguration, keep the last frame visible and overlay `Reconfiguring format…`. Mandatory MVP formats are MJPEG, YUYV/YUY2, UYVY, NV12, NV21 and I420. Unsupported preview FOURCC does not disable control.

## 2026-09-13 — D007 — Resident tray app

V4Lens is a resident tray application patterned after the author's other Fyne GUI tools. Multiple devices appear as separate tray submenus with profiles. Preview opens directly for one usable device and offers device choice when multiple are present.

## 2026-09-13 — D008 — Preferences / tray toggles

Use a separate Preferences window. `Start with system` and `Apply profile on start` are also exposed directly as tray checkboxes.

## 2026-09-13 — D009 — Desktop integration

MVP includes user autostart but not package-manager/AUR packaging. Future package-manager installation owns the visible launcher under `/usr/share/applications`; runtime autostart is a separate `$XDG_CONFIG_HOME/autostart/v4lens.desktop`. Do not create a user-visible runtime launcher under `$XDG_DATA_HOME/applications` that could shadow the package-owned launcher.

## 2026-09-13 — D010 — Logging

Logging/diagnostics are mandatory. File logs use XDG state storage (`$XDG_STATE_HOME`, default `~/.local/state`), not `~/.local/share`.

## 2026-09-13 — D011 — Post-MVP PTZ

Visual hardware PTZ in Preview is a mandatory product goal after MVP: drag controls real pan/tilt; mouse wheel controls real zoom where exposed by V4L2.

## 2026-09-13 — D012 — Non-goals

Do not plan or implement video recording, screenshots/capture-to-file, virtual camera, network remote control, plugin framework, cloud/profile synchronization or global hotkey support unless the product direction is explicitly changed later.

## 2026-09-13 — D013 — Phase 0 probe: decoder choice, go4vl scope, raw-ioctl struct discipline, fd ownership

Findings from the build-tagged Phase 0 probe against a real AVerMedia PW513 (uvcvideo, single-planar capture), 2026-09-13. Benchmarks: AMD Ryzen 9 5950X, 1080p frames captured from the device, `image.YCbCr` output, decode only (no blit).

**Decoder decision: pure-Go decoders for MVP; no libyuv, no Pion `pkg/frame` in the product build.**

| path (1920×1080) | time/frame | bytes/op | allocs/op | fps-equiv |
|---|---|---|---|---|
| pure-Go UYVY → YCbCr (422) | 2.35 ms | 4.15 MB | 2 | ≈425 |
| libyuv `UYVYToI420` (cgo) | 0.195 ms | 4.15 MB | 2 | ≈5100 |
| Pion `frame` UYVY (own cgo kernels) | 2.43 ms | 4.16 MB | 4 | ≈410 |
| pure-Go MJPEG (stdlib `image/jpeg`) | 20.9 ms | 3.15 MB | 7 | ≈48 |
| Pion MJPEG (also `image/jpeg` + DHT fixup) | 21.5 ms | 3.15 MB | 6 | ≈46 |
| pure-Go NV12 (synthetic padded) | 0.83 ms | 3.11 MB | 2 | ≈1200 |

Rationale: the advisory gate is "comfortably under ~33 ms/frame at 1080p"; every path passes, including MJPEG at ~21 ms against the ≤30 fps render ceiling (decode runs only while Preview is visible). Pion adds a dependency and cgo kernels yet is *slower* than the plain-Go kernel for packed YUV, and its MJPEG path is stdlib `image/jpeg` plus a DHT fixup — no gain. libyuv is 10.7× faster on raw YUV but raw-YUV conversion is not the bottleneck (MJPEG is), libyuv has no pkg-config file and no Alpine package (CI link problem), and MJPEG has no libyuv C entry point on this system anyway. The build-tagged cgo comparison adapter stays in `internal/probe` as evidence; revisit libyuv only if profiling on target hardware shows decode time mattering. Decoders output `*image.YCbCr` directly — no NRGBA conversion stage was added.

**go4vl v0.5.0 confirmed inadequate for the control tree.** `QueryAllExtControls` starts at `CTRL_CLASS_CODEC | NEXT_CTRL` and returned only the 9 camera-class entries — it **missed all 10 user-class controls** (brightness … backlight compensation). Its ext-control `Control` struct truncates int64 min/max/step/default to int32, and `SetControlValue` pre-queries and client-side rejects out-of-range values, defeating kernel clamping. Consequence (already in ARCHITECTURE): local `QUERY_EXT_CTRL` / `G_EXT_CTRLS` / `S_EXT_CTRLS` wrappers are the control path; go4vl is used only where the probe validated it (QUERYCAP, ENUM_FMT, frame sizes/intervals, MMAP streaming with `Frame.Release()`).

**Raw-ioctl struct discipline.** Go mirrors of `v4l2_query_ext_ctrl`, `v4l2_ext_control` (union at offset 12, no trailing pad), `v4l2_ext_controls`, `v4l2_format`, `v4l2_streamparm` are asserted at runtime against sizes **and decisive field offsets** computed from the in-tree headers via cgo (`offsetof`). This caught two real bugs: the probe's first control enumeration used a wrong `NEXT_CTRL` value (it is `0x80000000`, not the `GRABBED` bit — EINVAL otherwise), and a missing `elem_size` field (it precedes `elems`; sizes still matched 232, only offsets caught it). Flag bits verified against the real uapi: `DISABLED=0x1`, `GRABBED=0x2`, and this newer kernel reports `HAS_WHICH_MIN_MAX=0x1000` ("has-min-max" in v4l2-ctl) on all integer controls — the domain mapping must tolerate unknown flag bits. `NEXT_CTRL|NEXT_COMPOUND` (both, every iteration) enumerates 20 controls incl. both `CTRL_CLASS` pseudo-controls with faithful int64 metadata.

**Device facts (PW513/uvcvideo).** Raw capture format is UYVY (no YUYV): `S_FMT` with FOURCC=YUYV negotiated UYVY — `S_FMT` is a negotiation; the effective format must always be read back. MJPG reports `bytesperline=0` (compressed formats carry no stride); UYVY 1080p reports 3840 (tightly packed). Frame intervals are discrete lists per concrete format+size (e.g. 1080p: 1/60, 1/30, 1/15, 1/10; 4K: 1/15, 1/7, 1/3); `V4L2_CAP_TIMEPERFRAME` capability is set. Dependency pairs observed as `INACTIVE` flags: white-balance-temperature inactive while WB-auto is on; exposure-time-absolute inactive while auto-exposure is auto — the bounded-retry apply path covers exactly this.

**fd ownership / external owners.** While an fd was actively streaming (vb2 queue cycling), `S_FMT` from another fd returned **EBUSY** and the running stream was untouched; `TRY_FMT` still succeeded (validation is always available). With **no** stream active, `S_FMT` from any fd (including a short-lived one that then closed) mutated the device-global format — uvcvideo has no per-fd format isolation. This validates the plan: control fd stays panel-mode (`TRY_FMT` only), own-stream reconfiguration rides the stream fd, detached applies use a short-lived fd, and external-owner `EBUSY` is a real expected outcome to classify, not a guess.

**Decoder correctness discipline.** The libyuv cross-check exposed a pure-Go UYVY bug (Y read from the chroma byte phase — UYVY is `U Y0 V Y1`, a phase flip vs YUYV, not a chroma swap); after the fix the sampled |Y diff| on a real frame is 0. Synthetic planted-value phase tests (padded stride included) are now part of the probe and become mandatory golden tests for the Phase 5 decoders. Note the first YUYV-benchmarked capture negotiated UYVY — benchmark names say YUYV but the measured format is UYVY.
