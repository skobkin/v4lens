package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"git.skobk.in/skobkin/v4lens/internal/app"
)

// logLevels are the accepted slog level names.
var logLevels = []string{"debug", "info", "warn", "error"}

// PreferencesWindow edits the persisted preference set. Changes save on
// Apply; the autostart .desktop sync for "Start with system" is owned by
// the platform integration (it applies whenever the pref changes).
type PreferencesWindow struct {
	app *app.App
	win fyne.Window

	startWithSystem *widget.Check
	applyOnStart    *widget.Check
	notifyIssues    *widget.Check
	logLevel        *widget.Select
	logToFile       *widget.Check
	applyBtn        *widget.Button
}

// NewPreferencesWindow builds (but does not show) the preferences window,
// seeded from the current prefs.
func NewPreferencesWindow(fa fyne.App, a *app.App) *PreferencesWindow {
	prefs := a.Prefs()

	p := &PreferencesWindow{
		app: a,
		win: fa.NewWindow("V4Lens Preferences"),
	}

	p.startWithSystem = widget.NewCheck("Start with system", nil)
	p.startWithSystem.SetChecked(prefs.StartWithSystem)

	p.applyOnStart = widget.NewCheck("Apply default profiles on start", nil)
	p.applyOnStart.SetChecked(prefs.ApplyOnStart)

	p.notifyIssues = widget.NewCheck("Notify about profile apply issues", nil)
	p.notifyIssues.SetChecked(prefs.NotifyApplyIssues)

	p.logLevel = widget.NewSelect(logLevels, nil)
	p.logLevel.SetSelected(prefs.LogLevel)

	p.logToFile = widget.NewCheck("Write log to file (XDG state)", nil)
	p.logToFile.SetChecked(prefs.LogToFile)

	p.applyBtn = widget.NewButton("Apply", p.apply)

	form := widget.NewForm(
		widget.NewFormItem("Startup", p.startWithSystem),
		widget.NewFormItem("Startup", p.applyOnStart),
		widget.NewFormItem("Notifications", p.notifyIssues),
		widget.NewFormItem("Logging", p.logLevel),
		widget.NewFormItem("Logging", p.logToFile),
	)

	p.win.SetContent(container.NewVBox(
		form,
		container.NewHBox(p.applyBtn),
	))

	p.win.Resize(fyne.NewSize(420, 300))

	return p
}

// Show reveals the window.
func (p *PreferencesWindow) Show() {
	p.win.Show()
}

func (p *PreferencesWindow) apply() {
	prefs := p.app.Prefs()
	prefs.StartWithSystem = p.startWithSystem.Checked
	prefs.ApplyOnStart = p.applyOnStart.Checked
	prefs.NotifyApplyIssues = p.notifyIssues.Checked
	prefs.LogToFile = p.logToFile.Checked

	if p.logLevel.Selected != "" {
		prefs.LogLevel = p.logLevel.Selected
	}

	if err := p.app.UpdatePrefs(prefs); err != nil {
		p.applyBtn.Importance = widget.DangerImportance
		p.applyBtn.SetText("Save failed")

		return
	}

	p.win.Close()
}
