# MVP Scope

## In MVP

- Linux-only desktop application in Go + Fyne 2.8.x.
- Resident tray lifecycle.
- Single-instance activation forwarding.
- V4L2 device discovery and reconnect/disconnect lifecycle.
- Persistent device identity not based on `/dev/videoN`.
- Dynamic ordinary + extended V4L2 control discovery.
- Curated semantic grouping for common controls plus generic fallback rendering.
- Live control editing.
- Slider + exact numeric input.
- ~200 ms coalesced live slider writes plus immediate final write.
- `Image`, `PTZ`, `Format`, `Advanced` tabs.
- Profiles stored per persistent device.
- Default profile per physical device.
- Immediate profile application.
- Partial-apply result and no rollback.
- Format profile state: input, FOURCC, resolution, FPS.
- Separate Preview window.
- Own-preview stop/reconfigure/restart with frozen last frame and overlay.
- Preview support: MJPEG, YUYV/YUY2, UYVY, NV12, NV21, I420.
- Graceful `Preview unavailable for <FOURCC>` for other formats.
- Tray profile switching for multiple devices.
- `Start with system` and `Apply profile on start` in tray + Preferences.
- Preferences window.
- File logging + log levels + XDG state path.
- Meaningful background partial-apply notifications without spam.
- Baseline Woodpecker CI: format/lint/vet/test/build.
- Fake backend unit tests; optional tagged V4L integration tests.

## Explicitly outside MVP, but plausible future work

- Mandatory next product milestone: visual hardware PTZ in Preview.
- Richer/best-effort external camera-use detection.
- Reconnect-behavior refinements beyond a correct basic device lifecycle.
- Crop/compose/selection UI and other uncommon format-plane settings if real use justifies them.
- Vendor-specific extension modules only when concrete hardware requires behavior not expressible by normal V4L controls.

## Not planned product goals

Do not add these speculatively:

- video recording;
- screenshot/capture-to-file workflow;
- virtual-camera output;
- network/remote-control server;
- plugin marketplace/framework;
- cloud/profile sync;
- global hotkey system.

## Packaging boundary

AUR/package-manager packaging is outside MVP. Keep source layout packaging-friendly and follow the future desktop-file constraints in `ARCHITECTURE.md`, but do not spend the MVP implementation task creating PKGBUILD/AUR publication workflows.
