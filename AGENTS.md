# AGENTS.md

## Project intent

V4Lens is a small Linux desktop utility for V4L2 cameras and capture devices. It should make common camera controls easier to use than qv4l2 while preserving access to unfamiliar or vendor-specific controls instead of hiding them.

## Authoritative documents

Read these before making changes. Precedence is:

1. `docs/DECISION_LOG.md` — later dated decisions override earlier documents where they explicitly conflict. Record every new product-level decision here.
2. `docs/UI_UX_SPEC.md` — user-facing behavior and UI contract.
3. `docs/ARCHITECTURE.md` — architecture and runtime contract.
4. `docs/PRODUCT_SPEC.md` — product intent.
5. `docs/MVP_SCOPE.md` — scope boundary between MVP and post-MVP goals.
6. `docs/REFERENCES.md` — references only; references never override V4Lens specs.

The approved mockup at `assets/v4lens-ui-concept.png` is a visual reference. Text specs win on conflicts.

## Non-negotiable constraints

- Linux-only.
- Go + Fyne 2.8.x.
- V4L2-first. Do not create a cross-platform camera abstraction merely for hypothetical future portability.
- Keep V4L2/ioctl/library details behind an internal backend boundary; UI and profile code consume normalized domain types.
- Main process is a resident tray application.
- One application instance per user. A later launch activates the existing instance.
- Never identify a persistent camera by `/dev/videoN` alone.
- Profiles are bound to stable device identity and survive disconnects/reboots/device-node renumbering.
- Control changes in the main UI are live; format changes are staged and explicitly applied.
- Partial profile application is a normal result, not an all-or-nothing failure. Do not roll back settings that applied successfully.
- Never stop, reconfigure or otherwise interfere with a video stream owned by another application.
- If our own preview must stop for format reconfiguration, keep the Preview window open, retain the last frame and show a transient reconfiguration overlay.
- Unsupported preview formats must not block camera control or profile application.
- No unbounded frame or UI-update queues. Stale preview frames must be dropped.
- Do not perform device I/O synchronously in a Fyne callback.
- Any Fyne API call made from an application-created goroutine must be scheduled via `fyne.Do` / `fyne.DoAndWait` as appropriate.
- Unknown controls must not silently disappear. Render safe generic controls when possible; explicitly mark unsupported compound/unsafe controls otherwise.
- Do not invent exact camera-busy/idle status. Only show states supported by reliable evidence.
- Code comments must be in English.

## UI rules

- The persistent top context area contains device selection, device state, profile selection, default marker, Save, Preview and overflow actions.
- Tabs: `Image`, `PTZ`, `Format`, `Advanced`.
- `Advanced` is a separate tab initially. It may be reconsidered later without changing the backend/domain architecture.
- Sliders for integer controls have a linked numeric field for exact input.
- Live slider writes are coalesced to at most roughly one write per 200 ms while dragging; `OnChangeEnded` must immediately commit the final value.
- Disabled/inactive controls stay visible and disabled when their existence is known.
- Selecting a profile applies it immediately.
- The current profile becomes visibly dirty when actual device state differs from the saved profile.
- Preview is a separate normal window.
- Closing the main window hides it; explicit Quit terminates the resident app.

## Code quality

Keep the strict golangci-lint v2 configuration referenced in `docs/REFERENCES.md` (the aimon/meshgo style, including `nlreturn`); adapt exclusions only when V4Lens has a concrete reason.

Before declaring a change complete, run at least:

```sh
gofmt -w .
go test ./...
go vet ./...
golangci-lint run ./...
```

Build the GUI binary in CI as well. Add integration tests under an explicit build tag when they require `v4l2loopback` or real V4L2 devices; baseline CI must not require physical camera hardware.

## Decision discipline

Do not silently change product behavior because an implementation shortcut is easier. If hardware/library/Fyne behavior forces a meaningful change, document it in `docs/DECISION_LOG.md` and ask the user before changing a product-level decision.

There is no legacy V4Lens configuration yet. Do not add migration or backward-compatibility machinery speculatively.
