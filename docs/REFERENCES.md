# Direct implementation references

These are references, not authority. Copy/adapt proven infrastructure where it fits V4Lens; do not copy unrelated product behavior.

## Author's Go/Fyne projects

### Strict golangci-lint v2

Start with the stricter current pattern (includes `nlreturn`):

- aimon: https://github.com/skobkin/aimon/blob/master/.golangci.yml
- meshgo: https://github.com/skobkin/meshgo/blob/master/.golangci.yml
- qBtRemoteGo comparison: https://github.com/skobkin/qBtRemoteGo/blob/master/.golangci.yml

Only add V4Lens-specific exclusions when there is a concrete reason. Prefer local `#nosec` justification for unavoidable low-level V4L/CGO operations over broad package exclusions.

### Woodpecker CI

- meshgo Fyne/Linux CI: https://github.com/skobkin/meshgo/blob/master/.woodpecker/ci.yaml
- qBtRemoteGo CI: https://github.com/skobkin/qBtRemoteGo/blob/master/.woodpecker/ci.yaml

Adapt build dependencies to the final V4L/decoder choice. Baseline CI should run lint/vet/test/build without physical camera hardware.

### GoReleaser — future release reference, not MVP packaging scope

- qBtRemoteGo: https://github.com/skobkin/qBtRemoteGo/blob/master/.goreleaser.yml
- aimon: https://github.com/skobkin/aimon/blob/master/.goreleaser.yml

### MIT license

- qBtRemoteGo: https://github.com/skobkin/qBtRemoteGo/blob/master/LICENSE
- aimon: https://github.com/skobkin/aimon/blob/master/LICENSE

### AGENTS.md style / authoritative specs

- aimon: https://github.com/skobkin/aimon/blob/master/AGENTS.md
- qBtRemoteGo: https://github.com/skobkin/qBtRemoteGo/blob/master/AGENTS.md
- meshgo: https://github.com/skobkin/meshgo/blob/master/AGENTS.md

The aimon file is especially useful as a reference for Fyne UI-thread rules and authoritative-document precedence.

### Logging

- qBtRemoteGo `slog` manager: https://github.com/skobkin/qBtRemoteGo/blob/master/internal/logging/logging.go
- qBtRemoteGo UI logging settings usage: https://github.com/skobkin/qBtRemoteGo/blob/master/internal/ui/ui.go

Adapt the manager, but use V4Lens XDG **state** path for log files.

### Single instance + activation

- common interface/helpers: https://github.com/skobkin/qBtRemoteGo/blob/master/internal/platform/single_instance.go
- Unix flock implementation: https://github.com/skobkin/qBtRemoteGo/blob/master/internal/platform/single_instance_unix.go
- activation forwarding/server: https://github.com/skobkin/qBtRemoteGo/blob/master/internal/platform/activation.go
- main bootstrap behavior: https://github.com/skobkin/qBtRemoteGo/blob/master/cmd/qbtremotego/main.go

V4Lens is Linux-only, so Windows-specific branches are unnecessary in MVP.

### Autostart and corrected desktop-file separation

Use the release-preparation branch where qBtRemoteGo fixed visible-launcher/runtime-handler separation:

- Linux integration/autostart: https://github.com/skobkin/qBtRemoteGo/blob/fix/linux-desktop-packaging/internal/platform/integration_linux.go
- static visible launcher: https://github.com/skobkin/qBtRemoteGo/blob/fix/linux-desktop-packaging/packaging/linux/qbtremotego.desktop
- packaging/desktop integration rationale: https://github.com/skobkin/qBtRemoteGo/blob/fix/linux-desktop-packaging/packaging/linux/README.md

For V4Lens MVP only the runtime autostart part is implemented. Future packaging owns the visible launcher.

### Fyne/tray/lifecycle patterns

- aimon/meshgo-style Fyne bootstrap reference: https://github.com/skobkin/meshgo/blob/master/cmd/gui/main.go
- meshgo tray: https://github.com/skobkin/meshgo/blob/master/internal/ui/tray.go
- meshgo UI lifecycle: https://github.com/skobkin/meshgo/blob/master/internal/ui/lifecycle.go
- meshgo app bootstrap: https://github.com/skobkin/meshgo/blob/master/internal/ui/app_bootstrap.go

## V4L2 / camera-control references

### go4vl

Repository:

- https://github.com/vladimirvivien/go4vl

Useful source files:

- ordinary controls: https://github.com/vladimirvivien/go4vl/blob/main/v4l2/control.go
- extended controls: https://github.com/vladimirvivien/go4vl/blob/main/v4l2/ext_controls.go
- events implementation/reference: https://github.com/vladimirvivien/go4vl/blob/main/v4l2/events.go
- project README/API examples: https://github.com/vladimirvivien/go4vl/blob/main/README.md

Do not couple V4Lens domain/UI directly to go4vl types; keep an adapter boundary so missing/problematic ioctls can be implemented locally if needed.

### cameractrls

- repository: https://github.com/soyersoyer/cameractrls
- README/features: https://github.com/soyersoyer/cameractrls/blob/main/README.md
- semantic grouping/vendor-control reference: https://github.com/soyersoyer/cameractrls/blob/main/cameractrls.py

Use for control taxonomy and hardware-behavior inspiration, not as a UI to clone.

### Pion media frame decoders

- format/decoder map: https://github.com/pion/mediadevices/blob/master/pkg/frame/decode.go
- MJPEG decoder: https://github.com/pion/mediadevices/blob/master/pkg/frame/compressed.go
- YUV CGO path: https://github.com/pion/mediadevices/blob/master/pkg/frame/yuv_cgo.go
- no-CGO YUV path: https://github.com/pion/mediadevices/blob/master/pkg/frame/yuv_nocgo.go

This is a candidate/reference if a thin libyuv integration is not the best implementation trade-off.

## UI references

- qv4l2 screenshots supplied during design discussion: functional reference only; do not clone its tab/grouping behavior.
- agreed V4Lens concept bundled here: `assets/v4lens-ui-concept.png`.

Text specs override the generated mockup when they conflict.
