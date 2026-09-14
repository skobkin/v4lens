package ui

import (
	"log/slog"

	fyneapp "fyne.io/fyne/v2/app"

	"git.skobk.in/skobkin/v4lens/internal/app"
	"git.skobk.in/skobkin/v4lens/internal/platform"
	"git.skobk.in/skobkin/v4lens/internal/preview"
)

// Run builds the Fyne application, wires the main window to the app event
// bus, attaches the preview manager (which also serves as the apply
// engine's StreamOwner), installs the tray and blocks until the app
// quits. The application layer is started here (after the window's bus
// subscription exists, so the initial discovery events are seen) — with
// background=true the main window stays hidden (tray-only start,
// `--background`); activation reveals it.
func Run(a *app.App, previews *preview.Manager, dirs platform.Dirs, background bool, log *slog.Logger) {
	fa := fyneapp.New()

	// V4Lens colors for both variants; the variant itself (dark/light)
	// follows the system color scheme and live-updates from the desktop.
	fa.Settings().SetTheme(newTheme())

	m := NewMainWindow(fa, a, dirs, log)

	if previews != nil {
		m.SetPreviewManager(previews)
	}

	if !background {
		m.Show()
	}

	m.background = background
	m.Start()

	// Start discovery only after the bus subscription above: the initial
	// scan publishes its events immediately.
	a.Start()

	fa.Run()

	// fa.Run returned (app quit); the close interceptor already stopped
	// the app layer and the preview pipelines.
	log.Info("ui exited")
}
