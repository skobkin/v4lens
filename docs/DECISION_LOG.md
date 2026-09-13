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
