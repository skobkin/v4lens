package ui

import (
	"log/slog"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"

	"git.skobk.in/skobkin/v4lens/internal/app"
	"git.skobk.in/skobkin/v4lens/internal/buildinfo"
	"git.skobk.in/skobkin/v4lens/internal/platform"
	"git.skobk.in/skobkin/v4lens/internal/preview"
	"git.skobk.in/skobkin/v4lens/internal/resources"
)

// appID is the single application identity: the Wayland app_id hint and
// the D-Bus notification/tray identity.
const appID = "in.skobk.in.v4lens"

// Run builds the Fyne application, wires the main window to the app event
// bus, attaches the preview manager (which also serves as the apply
// engine's StreamOwner), installs the tray and blocks until the app
// quits. The application layer is started here (after the window's bus
// subscription exists, so the initial discovery events are seen) — with
// background=true the main window stays hidden (tray-only start,
// `--background`); activation reveals it.
func Run(a *app.App, previews *preview.Manager, dirs platform.Dirs, background bool, log *slog.Logger) {
	// Nothing injects the metadata from outside the binary — the identity
	// and version come from buildinfo, so they are set here, before app
	// creation; without it Fyne reports placeholder metadata ("0.0.1").
	fyneapp.SetMetadata(fyne.AppMetadata{
		ID:         appID,
		Name:       buildinfo.AppName,
		Version:    buildinfo.Version,
		Migrations: map[string]bool{"fyneDo": true},
	})

	// Stable application identity: the Wayland app_id hint and D-Bus
	// notification/tray identity instead of Fyne's per-launch
	// "missing-id-<timestamp>".
	fa := fyneapp.NewWithID(appID)

	// V4Lens colors for both variants; the variant itself (dark/light)
	// follows the system color scheme and live-updates from the desktop.
	fa.Settings().SetTheme(newTheme())

	// App mark: the driver falls back to it for window title bars and the
	// system tray icon.
	fa.SetIcon(resources.CameraIcon)

	m := NewMainWindow(fa, a, dirs, log)

	if previews != nil {
		m.SetPreviewManager(previews)
	}

	if !background {
		m.Show()
	}

	m.background = background
	m.Start()

	// Tray mark in the active variant's foreground color so it stays
	// visible on light and dark panels alike (the raw app mark is black).
	// Re-applied when the desktop color scheme flips.
	installTrayIcon(fa)
	fa.Settings().AddListener(func(fyne.Settings) { installTrayIcon(fa) })

	// Start discovery only after the bus subscription above: the initial
	// scan publishes its events immediately.
	a.Start()

	fa.Run()

	// fa.Run returned (app quit); the close interceptor already stopped
	// the app layer and the preview pipelines.
	log.Info("ui exited")
}

// installTrayIcon publishes the app mark as a themed resource: Fyne
// rasterizes it in the current variant's foreground color, and the
// trailing listener re-runs this after every color-scheme change so the
// icon re-tints. Must be called after the tray menu exists.
func installTrayIcon(fa fyne.App) {
	desk, ok := fa.(desktop.App)
	if !ok {
		return
	}

	desk.SetSystemTrayIcon(theme.NewThemedResource(resources.CameraIcon))
}
