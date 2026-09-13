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
// quits. With background=true the main window stays hidden (tray-only
// start, `--background`); activation reveals it.
func Run(a *app.App, previews *preview.Manager, dirs platform.Dirs, background bool, log *slog.Logger) {
	fa := fyneapp.New()

	m := NewMainWindow(fa, a, dirs, log)

	if previews != nil {
		m.SetPreviewManager(previews)
	}

	if !background {
		m.Show()
	}

	m.background = background
	m.Start()

	fa.Run()

	// fa.Run returned (app quit); the close interceptor already stopped
	// the app layer and the preview pipelines.
	log.Info("ui exited")
}
