package ui

import (
	"log/slog"

	fyneapp "fyne.io/fyne/v2/app"

	"git.skobk.in/skobkin/v4lens/internal/app"
)

// Run builds the Fyne application, wires the main window to the app event
// bus and blocks until the main window closes. The tray/single-instance
// integration hooks in around this entry point in their own phase.
func Run(a *app.App, log *slog.Logger) {
	fa := fyneapp.New()

	m := NewMainWindow(fa, a, log)
	m.Show()
	m.Start()

	fa.Run()

	// fa.Run returned (last window closed); the close interceptor already
	// stopped the app layer.
	log.Info("ui exited")
}
