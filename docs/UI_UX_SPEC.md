# V4Lens UI / UX Specification

## 1. Visual authority

The approved concept image is:

`assets/v4lens-ui-concept.png`

It demonstrates hierarchy and interaction intent. It intentionally resembles feasible Fyne controls rather than a bespoke rendered design. Do not chase exact generated-image pixels. Prefer standard Fyne widgets and layouts unless a small custom presentation widget is clearly justified.

## 2. Main window structure

The content begins immediately below the normal window title bar. Do not waste vertical space on a large product logo/name/subtitle block.

Persistent top context area:

```text
Camera/Device  [ AVerMedia PW513 ▼ ]  ● Available
Profile        [ Video calls      ▼ ]  ★ Default       [Save] [Preview] [⋮]
```

Exact one-row/two-row wrapping may adapt to window width, but both device and profile context remain visible while switching tabs.

Below it:

```text
Image | PTZ | Format | Advanced
```

`Format` is the approved user-facing term. Do not use a standalone `Capture` tab label because it can imply recording.

## 3. Image tab

Known controls should be semantically grouped, for example:

- Exposure
- Image
- White Balance
- Focus / Lens (when present)
- additional well-understood image groups as supported by the device

Integer controls that benefit from a slider use:

```text
Brightness    ─────────●──────────────  [ 6 ]
```

The numeric field is editable and linked bidirectionally to the slider.

Rules:

- validate against min/max/step;
- normalize off-step input to the nearest valid value or to the device-reported result;
- read back the effective value when needed;
- while dragging, issue at most approximately one write every 200 ms, always preferring the newest pending value;
- `OnChangeEnded` writes the final value immediately even if the throttle interval has not elapsed;
- entry commit occurs on Enter and/or focus loss;
- do not queue every intermediate drag position.

Boolean controls use standard checks. Enumerated controls use standard selects. Buttons remain buttons. Read-only values are displayed but not editable.

If a control exists but becomes `INACTIVE`, `GRABBED` or otherwise unavailable because another control changed, keep it visible but disabled where possible. Refresh control descriptors after writes that can change control availability.

## 4. PTZ tab

PTZ is deliberately separate from image tuning.

Typical controls:

- Pan absolute
- Tilt absolute
- Zoom absolute
- Pan speed
- Tilt speed
- device-specific PTZ/reset controls when safely representable

Use the same slider + exact-value grammar for numeric PTZ values.

If a device has no PTZ controls, show a concise empty state rather than an empty generic form.

### Mandatory post-MVP direction

The Preview window should later become a direct hardware-PTZ surface:

- drag image → pan/tilt;
- mouse wheel → zoom;
- optional reset/center interaction only where supported by hardware controls.

This must drive real V4L2 PTZ controls, not merely crop/translate the preview image.

## 5. Format tab

Format changes are staged, not written live on every dropdown selection.

Fields:

- Input
- Pixel format / FOURCC
- Resolution
- Frame rate

Selections are capability-dependent. Do not let the user casually construct combinations the device never reported as supported.

Preferred dependency flow:

```text
Pixel format → valid resolutions → valid frame rates
```

Input selection may change the entire capability set and should refresh downstream choices.

Show current active format separately from staged values when they differ.

Actions:

- `Apply format settings`
- `Revert` / restore current active values

Changing format may make the current profile dirty.

## 6. Advanced tab

`Advanced` is a separate MVP tab.

Purpose:

- controls known to V4L2 but not assigned to a curated semantic group;
- vendor controls;
- uncommon control classes;
- diagnostic presentation of controls that cannot be safely edited.

Do not make `Advanced` a single unstructured dump. Group by reported V4L2 control class and preserve useful driver names/order where possible.

Unknown simple boolean/integer/menu/string controls should use generic safe widgets. Unsupported compound controls must be explicitly marked unsupported rather than omitted silently.

## 7. Profile interaction

The top Profile select changes the active profile and applies it immediately.

Menu/overflow actions should provide at least:

- Save
- Save As…
- Rename…
- Set as default
- Delete…
- Manage profiles… if/when a dedicated management window is useful

The current profile is visibly dirty when actual relevant device state differs from its saved content. Example footer:

```text
★ Video calls *                         ! Camera settings differ from saved profile
  Profile modified
```

`Save` stores the effective/current state rather than merely the user's unconfirmed intended values.

## 8. Preview window

Preview is a separate normal window.

Bottom status should show compact useful information such as:

```text
● Previewing · MJPG · 1920×1080 · 60 fps
```

The UI rendering cadence should normally be capped around 30 FPS even when capture runs faster. Stale frames are dropped.

When V4Lens temporarily stops its own preview to apply format settings:

- keep the Preview window open;
- retain the last successfully displayed frame;
- display a translucent status overlay such as `Reconfiguring format…`;
- remove the overlay and continue rendering after restart;
- on failure, replace/augment it with a concise error state while preserving controls in the main window.

Unsupported preview FOURCC:

- do not block controls or profiles;
- Preview states `Preview unavailable for <FOURCC>`.

## 9. Tray menu

Do not add an app-name header or decorative spacer.

Concept:

```text
Open
Preview                 # immediate with one usable device; submenu with >1
────────────────
AVerMedia PW513  ›
    Video calls     ●
    Desk
    Wide shot
Other device       ›
    ...
────────────────
☑ Start with system
☑ Apply profile on start
────────────────
Preferences
Quit
```

Each device has its own profile submenu. Selecting a profile applies it immediately.

`Start with system` and `Apply profile on start` are the same settings shown in Preferences, not duplicate state.

## 10. Preferences window

Separate small window. MVP settings:

- Start with system
- Apply profile on start
- Notify on meaningful partial/default-profile apply failures
- Log level: debug / info / warn / error
- Log to file

Avoid turning Preferences into a second camera-control UI.

## 11. Main-window close / activation

- Closing the main window hides it and leaves the tray application running.
- `Quit` exits.
- Launching V4Lens while an instance already exists activates/shows that instance instead of displaying an "already running" dead end.
