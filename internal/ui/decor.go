package ui

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
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

// cardRadius is the corner rounding of group cards.
const cardRadius = 8

// cardWidget is a rounded surface holding one control group. The
// background follows the theme's menu/card color; the content is a public
// container so test walks can descend into it.
type cardWidget struct {
	widget.BaseWidget
	stack   *fyne.Container
	bg      *canvas.Rectangle
	content *fyne.Container
}

// newCard assembles one group card: optional icon + bold title, a
// hairline separator, then the rows.
func newCard(icon fyne.Resource, title string, rows []fyne.CanvasObject) *cardWidget {
	head := widget.NewLabel(title)
	head.TextStyle = fyne.TextStyle{Bold: true} // test-visible group marker

	header := make([]fyne.CanvasObject, 0, 2)

	if icon != nil {
		header = append(header, widget.NewIcon(icon))
	}

	header = append(header, head)

	body := make([]fyne.CanvasObject, 0, len(rows)+2)
	body = append(body, container.NewHBox(header...), widget.NewSeparator())
	body = append(body, rows...)

	c := &cardWidget{
		// Roomier than the theme padding: cards read as surfaces, not rows.
		content: container.NewVBox(body...),
	}
	c.ExtendBaseWidget(c)

	c.bg = canvas.NewRectangle(c.current())
	c.bg.CornerRadius = cardRadius
	c.stack = container.NewStack(c.bg, container.New(layout.NewCustomPaddedLayout(cardPad, cardPad, cardPad, cardPad), c.content))

	return c
}

// cardPad is the inner padding of a group card.
const cardPad = 12

// CreateRenderer presents the padded content over the rounded background.
func (c *cardWidget) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.stack)
}

// Refresh re-resolves the card color so theme switches re-skin it.
func (c *cardWidget) Refresh() {
	if c.bg != nil {
		c.bg.FillColor = c.current()
	}

	c.BaseWidget.Refresh()
}

func (c *cardWidget) current() color.Color {
	return c.Theme().Color(theme.ColorNameMenuBackground, fyne.CurrentApp().Settings().ThemeVariant())
}
