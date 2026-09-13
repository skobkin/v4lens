# V4Lens Product Specification

## 1. Product goal

V4Lens is a Linux desktop utility for configuring V4L2 video devices quickly and safely during real use: video calls, streaming, capture workflows and similar situations.

The application should improve on tools such as qv4l2 and cameractrls by combining:

- fast access to the currently relevant device and profile;
- sensible semantic grouping for known controls;
- generic access to controls V4Lens does not recognize;
- exact values next to convenient sliders;
- named per-device profiles including both ordinary controls and video format;
- resident tray access;
- a preview window that cooperates with format reconfiguration;
- graceful degradation when another application already uses the device.

V4Lens is not webcam-only. Capture cards and other V4L2 video-capture devices are valid targets.

## 2. Primary workflows

### Tune a camera during a call

1. Open V4Lens from the tray/application launcher.
2. Select the relevant device from the persistent top selector.
3. Open Preview if desired.
4. Adjust image/PTZ controls live.
5. Save the modified state into the current profile or Save As a new profile.

### Switch scene/profile quickly

1. Select a profile in the main window or tray submenu.
2. V4Lens applies it immediately.
3. Settings that can be changed while the device is in use should still apply.
4. Settings that cannot be changed because the device is busy are reported as partial failures.
5. V4Lens reads back relevant actual state and shows whether the device matches the selected saved profile.

### Apply defaults after login

When enabled, the resident app starts with the desktop session and applies each connected device's configured default profile. Device reconnect handling is part of the resident-device lifecycle; failures are non-fatal and must not create notification spam.

### Change format while Preview is open

1. Stage a different input/pixel-format/resolution/FPS combination in `Format`.
2. Apply it.
3. If V4Lens owns the preview stream, retain the last frame, overlay `Reconfiguring format…`, stop/reconfigure/restart the stream, then resume rendering in the same Preview window.
4. If an external process prevents reconfiguration, leave that process alone and report the failure.

## 3. Device state semantics

Use only truthful states. MVP may expose states such as:

- `Available` — device is present and accessible enough for discovery/control operations;
- `Previewing` — V4Lens itself owns an active preview stream;
- `Busy` / `Conflict detected` — a concrete attempted operation returned a busy/conflict condition;
- `Disconnected` — the selected persistent device is currently absent.

Do not claim `Idle` merely because V4Lens cannot detect another process. PipeWire-only visibility cannot prove that a V4L2 device is unused by direct-access clients.

## 4. Profiles

A profile contains the desired state for one persistent physical device:

- input;
- pixel format;
- resolution;
- frame interval/FPS;
- supported ordinary and extended V4L2 control values.

Profile selection applies immediately.

One profile per physical device may be marked default. Profiles remain available when the device is disconnected.

Profile apply is best-effort and produces structured results:

- applied;
- skipped because unavailable/unsupported;
- failed because busy;
- failed because rejected/invalid;
- other error.

There is no global transaction and no rollback of successful writes.

## 5. MVP success definition

V4Lens is successful at MVP when it is materially more convenient than qv4l2 for repeated real-world webcam/capture-card configuration while still exposing unfamiliar V4L2 controls instead of assuming one camera model.
