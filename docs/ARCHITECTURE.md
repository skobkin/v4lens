# V4Lens Architecture / Implementation Specification

## 1. Architecture goals

Keep Linux/V4L2 specifics explicit while preventing ioctl/library details from leaking into UI/profile logic.

Suggested package layout (adjust only when materially simpler):

```text
cmd/v4lens/             process bootstrap
internal/app/           orchestration and lifecycle
internal/domain/        normalized device/control/profile/value types
internal/v4l/           Linux V4L2 backend and library/ioctl adapter
internal/device/        discovery, persistent identity, hotplug lifecycle
internal/profile/       profile storage, dirty comparison, apply engine
internal/preview/       stream + decode + latest-frame pipeline
internal/config/        app preferences/config
internal/platform/      autostart, single-instance, activation, XDG paths
internal/logging/       slog manager and file lifecycle
internal/ui/            Fyne windows/widgets/view-model glue
internal/resources/     icon/resources
```

Do not create a generic cross-platform media framework.

## 2. Core domain types

The exact Go spelling is flexible, but the domain must represent at least:

### DeviceIdentity

- stable physical-device ID;
- human display name;
- optional USB VID/PID;
- optional serial;
- optional stable by-id identity;
- physical-path fallback when serial identity is unavailable;
- endpoint/index identity where one physical device exposes multiple `/dev/video*` nodes.

### ControlDescriptor

- numeric control ID;
- driver-provided name;
- type;
- class;
- min/max/step/default as applicable;
- menu items as applicable;
- flags (`READ_ONLY`, `INACTIVE`, `GRABBED`, `VOLATILE`, `UPDATE`, etc.);
- current value when available.

### FormatCapabilities

- inputs;
- pixel formats;
- resolutions/frame-size descriptors;
- frame intervals/FPS for the selected format/size.

### Profile

- schema version;
- profile name;
- stable device match metadata;
- desired input/format/size/frame interval;
- desired controls keyed primarily by numeric V4L2 control ID, retaining a human name for diagnostics.

### ApplyResult

Per requested setting, represent applied/skipped/failed and a reason. The top-level result distinguishes full success from partial success and total failure.

## 3. Persistent device identity

Never persist `/dev/videoN` as identity.

Recommended identity priority:

1. stable `/dev/v4l/by-id` identity / serial-backed udev identity when available;
2. USB serial + VID/PID + endpoint/index information from sysfs/udev;
3. `/dev/v4l/by-path` / physical bus path fallback when no serial exists;
4. last-resort stable-ish sysfs parent/path fingerprint, explicitly treated as weaker identity.

Group multiple V4L nodes belonging to the same physical hardware where practical. Maintain a separate endpoint identity/index inside the physical-device identity.

Persist enough match metadata inside profile files to verify that a directory/hash still refers to the intended hardware.

Identical serial-less devices may necessarily be distinguished by physical port/path. Moving such hardware to another port can change fallback identity; this is preferable to silently binding it to whatever currently became `/dev/video0`.

## 4. Config/profile storage

Use YAML for human-inspectable configuration.

Default locations:

```text
$XDG_CONFIG_HOME/v4lens/config.yaml
$XDG_CONFIG_HOME/v4lens/profiles/<stable-device-key>/<profile-name>.yaml
```

When `XDG_CONFIG_HOME` is unset, use `~/.config` semantics.

The stable-device directory name should be filesystem-safe and deterministic (a short hash/slug is fine), while the profile content stores the actual match metadata.

`config.yaml` stores application preferences and per-device default-profile references, not the full profile payloads.

Profiles are never removed merely because their device is disconnected.

Write config/profile files atomically (temporary file + rename where appropriate) and use restrictive user permissions for private runtime/config data.

## 5. Control discovery/rendering

Enumerate ordinary and extended controls dynamically. A curated table may classify well-known standard V4L2 control IDs into UI groups/order, but it must not define the entire supported control universe.

Rendering strategy:

- boolean → checkbox;
- menu/integer-menu → select;
- integer/integer64 with practical range → slider + numeric entry;
- very large/awkward integer range → numeric entry, add slider only if meaningful;
- button → button;
- string → entry;
- read-only/volatile → display/disabled form;
- unknown safe simple type → generic renderer;
- unsupported compound type → visible unsupported descriptor.

When a write may affect other controls (`UPDATE`, known auto/manual mode changes, or driver behavior observed after write), refresh descriptors/values so disabled states remain truthful.

## 6. Live control write path

Fyne callbacks update UI intent but device I/O happens off the UI thread.

For slider drag:

- maintain at most one newest pending value per control;
- write no more frequently than ~200 ms while movement continues;
- discard superseded intermediate values;
- final `OnChangeEnded` value bypasses the wait and is committed immediately;
- read back when the device may normalize/clamp the result.

Avoid concurrent writes to the same device/control that can reorder user intent. Device operations should have explicit serialization where required by V4L2/device behavior.

## 7. Profile apply engine

Applying a profile is best-effort and bounded.

Recommended sequence:

1. verify target stable device identity;
2. determine whether input/format/size/FPS differ;
3. if our Preview owns a stream and format changes are needed, enter frozen/reconfiguring state and stop our stream cleanly;
4. apply input + format + resolution + frame interval in capability-valid order;
5. restart Preview if it was ours and reconfiguration succeeded sufficiently to stream;
6. re-enumerate controls because format/input changes can change control availability/defaults;
7. apply profile controls;
8. when controls are inactive/grabbed because dependencies have not changed yet, refresh and retry pending controls for a small bounded number of passes after successful mode-changing writes;
9. read back relevant effective values;
10. produce `ApplyResult` and dirty-state comparison.

Do not loop indefinitely. Do not implement aggressive retry. Do not roll back successful settings because another setting failed.

If format application gets `EBUSY` from an external owner, record those format failures and continue attempting controls that may still be writable.

## 8. Preview architecture

Preview capture and UI rendering are separate concerns.

Requirements:

- capture/drain at device/source cadence so buffers are returned promptly;
- decode/render is bounded (target UI refresh ceiling around 30 FPS);
- no unbounded channel of frames;
- if decoder/UI is busy, drop stale frames and preserve the newest useful frame;
- never let 60 FPS input build a 60 FPS UI-event backlog;
- retain the last displayed `image.Image` for temporary frozen/reconfigure state.

Mandatory preview FOURCC coverage for MVP:

- MJPEG
- YUYV / YUY2
- UYVY
- NV12
- NV21
- I420

### Decoder implementation choice

Do not write all conversions from scratch before evaluating mature code.

Preferred investigation order:

1. thin `libyuv` adapter if it provides clean, efficient coverage without unacceptable packaging/build complexity;
2. `pion/mediadevices/pkg/frame` as a Go-oriented alternative/reference for MJPEG and common YUV formats;
3. custom conversion only for a concrete unsupported gap.

Perform an early implementation spike/benchmark for at least 1920×1080 MJPEG and one raw YUV mode. The architecture must keep the decoder behind a narrow interface so the choice can change without touching UI/profile/device layers.

H.264/H.265/AV1 decoding is outside MVP.

## 9. Device use / busy detection

V4L2 does not give a universal truthful `is in use` boolean. Do not derive one from `open()` alone.

Use operation results as authoritative evidence for `Busy/conflict`. Optional PipeWire/process probing may be added later as best-effort diagnostics, but must not claim comprehensive absence of external users.

## 10. Tray lifecycle and single instance

Resident application behavior:

- `v4lens` → start normally or forward activation/show-main request to the running instance;
- `v4lens --background` → start resident without initially showing the main window;
- one instance per user;
- closing main/Preview windows does not necessarily exit the resident app;
- explicit Quit shuts down preview/device workers, activation listener, log file and tray cleanly.

Adapt qBtRemoteGo's Unix `flock` and activation-forwarding pattern rather than creating a second unrelated mechanism.

## 11. Autostart and future desktop packaging

MVP implements user autostart, but **not package-manager/AUR packaging**.

Runtime autostart entry:

```text
$XDG_CONFIG_HOME/autostart/v4lens.desktop
```

It should launch the installed/current executable with `--background`.

Future package-managed visible launcher:

```text
/usr/share/applications/v4lens.desktop
/usr/share/icons/hicolor/scalable/apps/v4lens.svg
```

The application must not generate a visible `$XDG_DATA_HOME/applications/v4lens.desktop` launcher that could shadow the package-owned file. This follows the corrected qBtRemoteGo desktop-packaging pattern.

Package install scripts must not own/edit user autostart files.

## 12. Logging and diagnostics

Use `log/slog` with component fields.

Runtime state/log path:

```text
$XDG_STATE_HOME/v4lens/v4lens.log
```

If `XDG_STATE_HOME` is unset, default to:

```text
~/.local/state/v4lens/v4lens.log
```

`~/.local/share` is XDG data storage and is not the preferred location for logs.

Preferences:

- level: debug/info/warn/error;
- log to file toggle.

If file logging cannot open, continue with stdout/stderr logging and surface a warning; do not fail application startup solely because the log file is unavailable.

Log device IDs/paths needed for diagnostics but never log raw frame data. Keep messages useful for profile partial-apply diagnosis.

## 13. Notifications

Use desktop notifications for meaningful background failures such as a default profile being only partially applied. Avoid repeated notification spam for the same persistent failure during a reconnect/retry loop.

Interactive main-window actions should additionally show a concise in-app result with access to details.

## 14. Testability

Backend interfaces must permit fake devices in unit tests.

Unit-test at least:

- stable device identity matching/fallback;
- profile serialization;
- profile dirty comparison;
- partial apply aggregation;
- dependency retry after a control changes availability;
- slider coalescing/final-write semantics without real sleeping where practical;
- format capability filtering;
- preview latest-frame/drop behavior;
- autostart file generation;
- single-instance helpers.

V4L integration tests may use `v4l2loopback` behind a build tag. Baseline CI must run without real camera hardware.
