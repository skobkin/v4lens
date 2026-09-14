package ui

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Theme-aware presentation widgets. They follow the stock pattern (resolve
// colors in CreateRenderer and again in Refresh): a system dark/light
// switch refreshes every object in the tree, and these widgets re-skin
// there. Bare canvas objects would keep the color they were built with.

// statusDotSize is the indicator diameter.
const statusDotSize = 10

// statusDot is a small state-indicator circle colored through a theme
// color name (Success = available, Warning = busy, Disabled = idle).
type statusDot struct {
	widget.BaseWidget
	circle *canvas.Circle
	name   fyne.ThemeColorName
}

// newStatusDot builds the indicator in its neutral state.
func newStatusDot() *statusDot {
	d := &statusDot{name: theme.ColorNameDisabled}
	d.ExtendBaseWidget(d)

	return d
}

// SetColor switches the indicator to a theme color.
func (d *statusDot) SetColor(name fyne.ThemeColorName) {
	d.name = name
	d.Refresh()
}

// CreateRenderer builds the circle.
func (d *statusDot) CreateRenderer() fyne.WidgetRenderer {
	d.circle = canvas.NewCircle(d.current())

	return widget.NewSimpleRenderer(d.circle)
}

// MinSize keeps the dot a fixed small square.
func (d *statusDot) MinSize() fyne.Size {
	d.ExtendBaseWidget(d)

	return fyne.NewSquareSize(statusDotSize)
}

// Refresh re-resolves the color so theme switches land here too.
func (d *statusDot) Refresh() {
	if d.circle != nil {
		d.circle.FillColor = d.current()
	}

	d.BaseWidget.Refresh()
}

func (d *statusDot) current() color.Color {
	return d.Theme().Color(d.name, fyne.CurrentApp().Settings().ThemeVariant())
}

// vRule is a vertical hairline rule in the separator color (context-bar
// group divider). Its MinSize height of 1 stretches to the row inside an
// HBox, like a widget.Separator does inside a VBox.
type vRule struct {
	widget.BaseWidget
	bar *canvas.Rectangle
}

// newVRule builds the divider.
func newVRule() *vRule {
	r := &vRule{}
	r.ExtendBaseWidget(r)

	return r
}

// CreateRenderer builds the rule.
func (r *vRule) CreateRenderer() fyne.WidgetRenderer {
	r.bar = canvas.NewRectangle(r.current())

	return widget.NewSimpleRenderer(r.bar)
}

// MinSize fixes the rule at one pixel wide.
func (r *vRule) MinSize() fyne.Size {
	r.ExtendBaseWidget(r)

	return fyne.NewSize(1, 1)
}

// Refresh re-resolves the separator color (theme switches included).
func (r *vRule) Refresh() {
	if r.bar != nil {
		r.bar.FillColor = r.current()
	}

	r.BaseWidget.Refresh()
}

func (r *vRule) current() color.Color {
	return r.Theme().Color(theme.ColorNameSeparator, fyne.CurrentApp().Settings().ThemeVariant())
}

// captionLabel renders a small uppercase group caption in the secondary
// color — the honest stand-in for the concept's small-caps captions.
func captionLabel(s string) *widget.Label {
	l := widget.NewLabel(strings.ToUpper(s))
	l.Importance = widget.LowImportance

	return l
}
