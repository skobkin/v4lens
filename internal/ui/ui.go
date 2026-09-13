package ui

import (
	"log/slog"

	fyneapp "fyne.io/fyne/v2/app"

	"git.skobk.in/skobkin/v4lens/internal/app"
	"git.skobk.in/skobkin/v4lens/internal/preview"
)

// Run builds the Fyne application, wires the main window to the app event
// bus, attaches the preview manager (which also serves as the apply
// engine's StreamOwner) and blocks until the main window closes.
func Run(a *app.App, previews *preview.Manager, log *slog.Logger) {
	fa := fyneapp.New()

	m := NewMainWindow(fa, a, log)

	if previews != nil {
		m.SetPreviewManager(previews)
	}

	m.Show()
	m.Start()

	fa.Run()

	// fa.Run returned (last window closed); the close interceptor already
	// stopped the app layer and the preview pipelines.
	log.Info("ui exited")
}
