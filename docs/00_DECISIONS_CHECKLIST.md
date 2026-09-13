# Final decisions checklist

All product-level questions discussed before implementation are considered resolved for MVP.

- [x] Product name: **V4Lens**.
- [x] Canonical repository: `git.skobk.in/skobkin/v4lens`; GitHub mirror under the same username.
- [x] Binary/repository slug: `v4lens`.
- [x] Fyne application ID: `in.skobk.v4lens`.
- [x] Linux-only, Go + Fyne 2.8.x, V4L2-first.
- [x] Resident tray application; single-instance activation behavior patterned after qBtRemoteGo.
- [x] Main UI keeps Device/Camera + Profile context always visible at the top.
- [x] MVP tabs: `Image`, `PTZ`, `Format`, `Advanced`.
- [x] `Advanced` starts as a separate tab.
- [x] Separate Preview window.
- [x] Preview freezes last frame and overlays `Reconfiguring format…` while our own stream is restarted.
- [x] Numeric sliders also have exact-value fields.
- [x] Live slider writes: coalesced/throttled at about 200 ms, with immediate final `OnChangeEnded` write.
- [x] Profile selection applies immediately.
- [x] Profile application is best-effort/partial; successful settings are not rolled back because another setting failed.
- [x] Actual device state is read back when required to keep UI and dirty-state truthful.
- [x] Profiles are stored separately under the config directory and bound to stable physical-device identity, never `/dev/videoN` alone.
- [x] A default profile is selected per physical device.
- [x] Multiple connected devices appear separately in the tray, each with its profile submenu.
- [x] Tray Preview opens immediately when exactly one usable device exists; with multiple devices it becomes a submenu.
- [x] Tray duplicates `Start with system` and `Apply profile on start` toggles.
- [x] Separate Preferences window.
- [x] Format settings include input, pixel format, resolution and FPS.
- [x] Mandatory MVP preview formats: MJPEG, YUYV/YUY2, UYVY, NV12, NV21, I420.
- [x] H.264/H.265 and other heavier compressed decoders are outside MVP.
- [x] An unsupported preview format does not disable device control.
- [x] Mandatory post-MVP goal: visual PTZ in Preview (drag = pan/tilt, wheel = zoom where hardware exposes corresponding V4L2 controls).
- [x] Video recording, capture-to-file/screenshots, virtual camera, network/remote control, plugin system and cloud/profile sync are not planned product goals.
- [x] Hotkeys are not a planned product goal.
- [x] File logging and diagnostics are mandatory.
- [x] Runtime logs use XDG state storage (`$XDG_STATE_HOME`, default `~/.local/state`), not XDG data storage.
- [x] Package-manager/AUR packaging is outside MVP.
- [x] Baseline Woodpecker CI is in MVP; reuse/adapt the author's existing Fyne GUI CI patterns.
- [x] Future packaging must use a static visible launcher installed by the package and a separate runtime autostart entry; the application must not create a user-visible launcher that shadows the package-managed desktop file.
- [x] Latest agreed UI mockup is included at `assets/v4lens-ui-concept.png`.
