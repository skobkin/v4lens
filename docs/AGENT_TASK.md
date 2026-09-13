# Implementation Task for Claude Code (GLM-5.3 / GLM-5.3 Flash)

## Mission

Implement the initial V4Lens MVP in a new repository intended for:

- canonical Forgejo: `https://git.skobk.in/skobkin/v4lens`
- GitHub mirror: `https://github.com/skobkin/v4lens`
- Go module: `git.skobk.in/skobkin/v4lens`

Read `AGENTS.md` and all authoritative documents before editing code.

## Execution principles

1. Do not redesign the product while implementing it.
2. Keep V4L2/library details out of UI/profile packages.
3. Keep I/O and decoding off the Fyne UI thread.
4. Prefer standard Fyne widgets/layouts consistent with the supplied UI reference.
5. Preserve generic support for controls V4Lens does not know semantically.
6. Treat partial apply as first-class data.
7. Do not add package-manager/AUR packaging in this task.
8. Do not add speculative non-goals from `MVP_SCOPE.md`.
9. When a real library/hardware behavior forces a product-level deviation, stop and ask the user; record the approved decision in `DECISION_LOG.md`.

## Phase 0 — inspect references and validate technical choices

Before building the whole UI:

- inspect the direct reference files in `REFERENCES.md`;
- choose/adapt the strict golangci-lint v2 config immediately;
- establish Woodpecker baseline CI early;
- validate go4vl (or the selected V4L2 adapter) for device/control/format enumeration needed by the domain;
- perform a narrow preview decoder spike comparing the preferred `libyuv` thin adapter with the Pion frame package where useful;
- verify at least the intended build path for 1920×1080 MJPEG and one raw YUV format;
- keep the decoder behind a narrow interface so this choice is replaceable.

If the implementation environment has no camera, do not fabricate hardware test results. Continue with fake-backend tests and document what still requires real-device verification.

## Phase 1 — repository/bootstrap quality baseline

Create:

- module and `cmd/v4lens`;
- Fyne 2.8.x dependency;
- strict `.golangci.yml`;
- MIT license;
- `AGENTS.md` adapted from this starter pack;
- baseline Woodpecker CI for lint/vet/test/build;
- initial config/XDG path helpers;
- logging manager and tests.

Do not add GoReleaser/package publishing unless needed solely for a local build check; release packaging is not MVP scope.

## Phase 2 — device/domain/backend

Implement:

- persistent device identity;
- device enumeration/disconnect/reconnect lifecycle;
- V4L2 capability discovery;
- ordinary + extended control descriptors/values;
- input/format/size/frame-interval capability discovery;
- fake backend interfaces for tests.

## Phase 3 — profiles/apply engine

Implement YAML config and per-device profile files.

Profile apply must:

- apply format before controls when required;
- cooperate with our own Preview state;
- never disrupt an external owner;
- retry dependency-blocked controls only in a small bounded manner after relevant state changes;
- continue after independent failures;
- read back effective state;
- return structured partial results;
- drive dirty-state calculation.

## Phase 4 — main Fyne UI

Implement the supplied hierarchy:

- persistent device/profile top context;
- Image/PTZ/Format/Advanced tabs;
- semantic + generic control renderers;
- exact numeric inputs beside useful sliders;
- 200 ms newest-value coalescing and immediate final writes;
- separate Preferences window;
- profile Save/Save As/Rename/Default/Delete workflow;
- partial-apply result presentation.

Use `assets/v4lens-ui-concept.png` as hierarchy/reference only. Stay within realistic Fyne widgets.

## Phase 5 — Preview

Implement:

- separate Preview window;
- bounded latest-frame pipeline;
- required formats;
- frozen-frame reconfiguration overlay;
- unsupported-format state;
- clean stop/restart/disconnect handling.

## Phase 6 — tray, single instance, autostart

Implement/adapt:

- qBtRemoteGo-style single-instance Unix lock + activation forwarding;
- tray Open/Preview/device-profile submenus;
- one-device/multi-device Preview behavior;
- Start with system;
- Apply profile on start;
- `$XDG_CONFIG_HOME/autostart/v4lens.desktop` with `--background`;
- clean shutdown.

Do not create a visible runtime `.desktop` launcher under `$XDG_DATA_HOME/applications`.

## Phase 7 — verification

Run and fix:

```sh
gofmt -w .
go test ./...
go vet ./...
golangci-lint run ./...
```

Ensure Woodpecker baseline CI passes.

Compare the final implementation against every checkbox in `ACCEPTANCE_CRITERIA.md`. Do not mark a criterion complete based only on code inspection when it requires actual runtime behavior; distinguish unit-tested, integration-tested and still-needs-real-device verification.

## Expected implementation report

At completion provide:

- architecture summary;
- decoder/backend choices and why;
- tests/checks run;
- real-device tests run (if any);
- known limitations;
- acceptance criteria still not empirically verified;
- any Decision Log additions.
