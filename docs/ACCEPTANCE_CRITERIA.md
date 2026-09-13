# MVP Acceptance Criteria

## Application lifecycle

- [ ] `v4lens` launches one resident tray application per user.
- [ ] Launching a second copy forwards activation to the existing instance and shows/raises the main window.
- [ ] `--background` starts without opening the main window.
- [ ] Closing the main window leaves the resident app running.
- [ ] Quit shuts down preview/device workers and releases resources cleanly.

## Device discovery/identity

- [ ] Connected V4L2 capture devices are enumerated without hardcoding `/dev/video0`.
- [ ] Persistent identity survives ordinary `/dev/videoN` renumbering.
- [ ] Profiles remain listed when a device is disconnected.
- [ ] A serial-less fallback does not silently match an arbitrary different `/dev/videoN`.
- [ ] Multiple physical devices can coexist in the main selector and tray.

## Controls

- [ ] Standard controls dynamically appear from device descriptors.
- [ ] Known controls are semantically grouped.
- [ ] Unknown safe controls remain accessible in Advanced/generic groups.
- [ ] Unsupported compound controls are visible as unsupported instead of silently omitted.
- [ ] Boolean/menu/integer/button/string/read-only types map to appropriate Fyne widgets.
- [ ] Integer sliders have an editable exact numeric field.
- [ ] Slider drag does not issue an ioctl for every mouse event.
- [ ] Slider writes are limited to about one newest value per 200 ms during continuous dragging.
- [ ] `OnChangeEnded` commits the final value immediately.
- [ ] Auto/manual changes correctly refresh dependent inactive/enabled controls.

## Profiles

- [ ] Profiles are YAML files under a persistent per-device config directory.
- [ ] Default profile can be chosen independently per physical device.
- [ ] Selecting a profile applies it immediately.
- [ ] Profile includes input + pixel format + resolution + FPS + V4L controls.
- [ ] Successful values remain applied if another value fails.
- [ ] Partial apply gives structured user-visible details.
- [ ] Effective state is read back enough to produce truthful dirty state.
- [ ] Save stores effective/current state.

## Format

- [ ] Tab label is `Format`.
- [ ] Input/FOURCC/resolution/FPS choices reflect real capabilities.
- [ ] Downstream choices are updated so unsupported combinations are not presented as ordinary valid choices.
- [ ] Format changes are staged until Apply.
- [ ] Revert restores currently active values in the form.
- [ ] External `EBUSY` is reported without disrupting the external stream.

## Preview

- [ ] Preview is a separate window.
- [ ] It supports MJPEG, YUYV/YUY2, UYVY, NV12, NV21 and I420.
- [ ] Unsupported FOURCC reports preview unavailability without blocking control/profile functions.
- [ ] Preview processing uses bounded queues/latest-frame behavior.
- [ ] UI refresh does not build an event backlog at 60 FPS input.
- [ ] If our format change requires restarting our stream, the window remains open with the last frame and `Reconfiguring format…` overlay.
- [ ] Preview resumes after successful restart.

## Tray / Preferences

- [ ] Tray has no redundant app-name header spacer.
- [ ] Tray exposes Open and Preview.
- [ ] With one usable device, Preview opens it directly; with multiple, Preview offers a device submenu.
- [ ] Each connected device has a profile submenu.
- [ ] Tray exposes `Start with system` and `Apply profile on start` checkboxes.
- [ ] Preferences exposes the same underlying settings plus logging controls.
- [ ] Autostart writes only `$XDG_CONFIG_HOME/autostart/v4lens.desktop` and does not create a visible user launcher under `$XDG_DATA_HOME/applications`.

## Logging / failure handling

- [ ] File logs go under `$XDG_STATE_HOME/v4lens/` with `~/.local/state` fallback.
- [ ] Supported log levels: debug/info/warn/error.
- [ ] Failure to open the log file does not prevent startup.
- [ ] Background default-profile partial failures can notify the user without repeated spam.
- [ ] Device disconnection/failure does not crash the tray app.

## Code quality / CI

- [ ] Strict golangci-lint v2 config is present.
- [ ] Woodpecker CI runs formatting/lint, vet, tests and build.
- [ ] `gofmt -w .`, `go test ./...`, `go vet ./...`, and `golangci-lint run ./...` pass.
- [ ] Baseline CI requires no real camera.
- [ ] Device/profile/app logic is testable via fakes.
- [ ] No package-manager/AUR implementation is included in this MVP task.
