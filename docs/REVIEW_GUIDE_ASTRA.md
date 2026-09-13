# Review Guide for Astra / Codex

Review V4Lens against the specs, not against personal UI preferences.

## Highest-priority review areas

### 1. Device identity correctness

Reject implementations that persist `/dev/videoN` as the durable identity. Check identical-device/serial-less fallback behavior and multi-node physical devices.

### 2. V4L2 semantics

Look for:

- incorrect assumptions that a control is always writable;
- failure to refresh dynamic flags/dependencies;
- invalid format/FPS combinations;
- code that treats opening a device as proof of exclusive ownership;
- code that attempts to stop/reconfigure another process's stream;
- unbounded retries around `EBUSY`.

### 3. Profile apply behavior

Verify:

- format then controls ordering where appropriate;
- no global rollback;
- partial result is preserved and surfaced;
- inactive controls can be retried after dependency changes without infinite loops;
- effective-state readback drives dirty status;
- disconnected profiles are preserved.

### 4. Concurrency/resource lifecycle

Inspect for:

- blocking V4L/device I/O in Fyne callbacks;
- Fyne calls from background goroutines without `fyne.Do`/`DoAndWait`;
- goroutine leaks after Preview close/device disconnect/app Quit;
- V4L buffers not released promptly;
- frame queues that grow at source FPS;
- reordered/stale slider writes after `OnChangeEnded`;
- races between profile apply and manual control edits.

### 5. Preview performance

Require bounded latest-frame behavior. A 60 FPS camera must not enqueue 60 GUI refreshes per second indefinitely. Check raw-frame copies and decoder placement for avoidable high-bandwidth work.

### 6. Generic control support

Check that unfamiliar controls do not disappear merely because no curated mapping exists. Compound/unsafe controls may be unsupported, but the UI should say so.

### 7. Desktop integration

Confirm:

- one per-user instance;
- activation forwarding works;
- autostart uses XDG config autostart and `--background`;
- runtime does not create a visible `$XDG_DATA_HOME/applications/v4lens.desktop` launcher;
- package-manager/AUR work did not leak into MVP scope.

### 8. Storage/logging

Confirm:

- YAML profile/config writes are atomic enough to avoid easy corruption;
- logs use `$XDG_STATE_HOME` / `~/.local/state`, not `~/.local/share`;
- file logging failure degrades gracefully;
- profile filenames/paths are sanitized.

## Scope review

Flag scope creep involving recording, screenshots, virtual camera, remote server, plugin framework, cloud sync or hotkeys.

Visual hardware PTZ is explicitly post-MVP; do not fail MVP because it is absent.

## Required reviewer output

Classify findings as:

- **BLOCKER** — correctness/data-loss/resource/safety/architecture violation;
- **MAJOR** — important spec/UX/runtime deviation;
- **MINOR** — maintainability or polish issue;
- **QUESTION** — ambiguity requiring user/product decision.

Prefer concrete file/function references and explain the failure mode rather than only stating a preference.
