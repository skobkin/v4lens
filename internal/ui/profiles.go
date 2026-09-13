package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"git.skobk.in/skobkin/v4lens/internal/app"
	"git.skobk.in/skobkin/v4lens/internal/profile"
)

// profileActions implements the profile operations behind the context bar
// and overflow menu: Save (effective state only), Save As, Rename, Set as
// default, Delete, and the structured partial-apply result dialog.
type profileActions struct {
	app *app.App
	win fyne.Window
	// Lookup returns the selected device key and selected profile name.
	LookupKey     func() string
	LookupProfile func() string
	// OnChange re-renders profile-dependent UI (lists, markers, footer).
	OnChange func()
}

// save stores the device's EFFECTIVE read-back state into the selected
// profile (or asks for a name first when none is selected).
func (p *profileActions) save() {
	key := p.LookupKey()
	if key == "" {
		return
	}

	name := p.LookupProfile()

	if name == "" {
		p.saveAs()

		return
	}

	if err := p.app.SaveProfile(key, name); err != nil {
		dialog.ShowError(err, p.win)

		return
	}

	p.changed()
}

// saveAs prompts for a new profile name and saves the effective state.
func (p *profileActions) saveAs() {
	key := p.LookupKey()
	if key == "" {
		return
	}

	nameEntry := widget.NewEntry()
	nameEntry.SetPlaceHolder("profile name")

	dialog.NewForm("Save profile as…", "Save", "Cancel",
		[]*widget.FormItem{widget.NewFormItem("Name", nameEntry)},
		func(ok bool) {
			if !ok {
				return
			}

			name := sanitizeProfileName(nameEntry.Text)
			if name == "" {
				dialog.ShowError(fmt.Errorf("profile name is required"), p.win)

				return
			}

			if err := p.app.SaveProfile(key, name); err != nil {
				dialog.ShowError(err, p.win)

				return
			}

			p.changed()
		},
		p.win,
	).Show()
}

// rename renames the selected profile.
func (p *profileActions) rename() {
	key, old := p.LookupKey(), p.LookupProfile()

	if key == "" || old == "" {
		return
	}

	nameEntry := widget.NewEntry()
	nameEntry.SetText(old)

	dialog.NewForm("Rename profile", "Rename", "Cancel",
		[]*widget.FormItem{widget.NewFormItem("Name", nameEntry)},
		func(ok bool) {
			if !ok {
				return
			}

			name := sanitizeProfileName(nameEntry.Text)
			if name == "" || name == old {
				return
			}

			if err := p.app.RenameProfile(key, old, name); err != nil {
				dialog.ShowError(err, p.win)

				return
			}

			p.changed()
		},
		p.win,
	).Show()
}

// setDefault marks (or clears) the selected profile as this device's
// default (apply-on-start and reconnect reference it).
func (p *profileActions) setDefault() {
	key, name := p.LookupKey(), p.LookupProfile()

	if key == "" {
		return
	}

	if err := p.app.SetDefaultProfile(key, name); err != nil {
		dialog.ShowError(err, p.win)

		return
	}

	p.changed()
}

// remove deletes the selected profile after confirmation.
func (p *profileActions) remove() {
	key, name := p.LookupKey(), p.LookupProfile()

	if key == "" || name == "" {
		return
	}

	dialog.NewConfirm("Delete profile", fmt.Sprintf("Delete profile %q?", name), func(ok bool) {
		if !ok {
			return
		}

		if err := p.app.DeleteProfile(key, name); err != nil {
			dialog.ShowError(err, p.win)

			return
		}

		p.changed()
	}, p.win).Show()
}

// applyNow applies the selected profile and presents the structured
// result: full applies refresh silently, issues open the result dialog.
func (p *profileActions) applyNow(key, name string) {
	if key == "" || name == "" {
		return
	}

	go func() {
		res, err := p.app.ApplyProfile(key, name)

		fyne.Do(func() {
			if err != nil {
				dialog.ShowError(err, p.win)

				return
			}

			// Full applies surface through the footer/dirty refresh; the
			// dialog is reserved for issues worth the user's attention.
			if res.Status != profile.StatusFull {
				showApplyResult(res, p.win)
			}

			p.changed()
		})
	}()
}

// showApplyResult renders the structured ApplyResult honestly: capture
// outcome plus every failed/skipped control step (applied steps are
// summarized by count).
func showApplyResult(res profile.ApplyResult, win fyne.Window) {
	head := widget.NewLabel(applySummary(res))
	head.TextStyle = fyne.TextStyle{Bold: true}

	content := container.NewVBox(head)

	if res.Capture.Attempted {
		line := "Capture: " + res.Capture.Status.String()

		if res.Capture.Reason != "" {
			line += " — " + res.Capture.Reason
		}

		content.Add(widget.NewLabel(line))
	}

	var applied int

	for _, s := range res.Steps {
		if s.Status == profile.StepApplied {
			applied++

			continue
		}

		line := s.Name + ": " + s.Status.String()

		if s.Reason != "" {
			line += " — " + s.Reason
		}

		content.Add(widget.NewLabel(line))
	}

	if applied > 0 {
		content.Add(widget.NewLabel(fmt.Sprintf("%d control(s) applied.", applied)))
	}

	dialog.ShowCustom("Profile apply result", "Close", container.NewVScroll(content), win)
}

func applySummary(res profile.ApplyResult) string {
	switch res.Status {
	case profile.StatusFull:

		return "Profile applied successfully."
	case profile.StatusPartial:

		return "Profile applied with issues (partially applied)."
	default:

		return "Profile apply failed."
	}
}

func (p *profileActions) changed() {
	if p.OnChange != nil {
		p.OnChange()
	}
}

// sanitizeProfileName normalizes a dialog-entered profile name to the
// store's file-safe slug space (store slugs again on save; this keeps the
// displayed name predictable).
func sanitizeProfileName(s string) string {
	out := make([]byte, 0, len(s))

	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			out = append(out, byte(r)) //nolint:gosec // the accepted set is pure ASCII
		case r == ' ':

			out = append(out, '-')
		}
	}

	return string(out)
}
