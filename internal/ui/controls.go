// Package ui implements the Fyne main window: control renderers, semantic
// tabs, the staged Format tab and the profile/preferences dialogs. All
// device I/O lives behind the app layer; Fyne callbacks only enqueue
// writes (coalesced or immediate) and every device result reaches the UI
// through dispatched events.
package ui

import (
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"git.skobk.in/skobkin/v4lens/internal/domain"
	"git.skobk.in/skobkin/v4lens/internal/ui/viewmodel"
)

// RendererKind identifies the editor shape chosen for a control. The
// registry maps (type, flags) to a kind: booleans to a check, menus to a
// select, practical-range integers to slider+entry, awkward ranges to a
// plain entry, bitmasks to a hex entry (never a slider), buttons to an
// action button, strings to an entry, read-only values to a display row,
// and unsupported compound shapes to a visible non-editable row.
type RendererKind int

const (
	// KindUnsupported renders a visible non-editable row (compound/array
	// payload shapes are not editable in the MVP but never disappear).
	KindUnsupported RendererKind = iota
	// KindReadOnly shows the current value without an editor.
	KindReadOnly
	// KindBoolean renders a checkbox.
	KindBoolean
	// KindMenu renders a select for menu/integer-menu controls.
	KindMenu
	// KindInteger renders a bidirectional slider + entry pair.
	KindInteger
	// KindIntegerEntry renders a numeric entry only (awkward range).
	KindIntegerEntry
	// KindBitmask renders a hex entry.
	KindBitmask
	// KindButton renders an action button.
	KindButton
	// KindString renders a text entry.
	KindString
)

// RendererKindFor maps a descriptor to its renderer kind.
func RendererKindFor(d domain.ControlDescriptor) RendererKind {
	if !d.Scalar() || !d.SimpleType() {
		return KindUnsupported
	}

	if d.Flags.Has(domain.FlagReadOnly) {
		return KindReadOnly
	}

	switch d.Type {
	case domain.TypeBoolean:

		return KindBoolean
	case domain.TypeMenu, domain.TypeIntegerMenu:

		return KindMenu
	case domain.TypeButton:

		return KindButton
	case domain.TypeString:

		return KindString
	case domain.TypeBitmask:

		return KindBitmask
	default:

		if viewmodel.PracticalSlider(d.Min, d.Max) {
			return KindInteger
		}

		return KindIntegerEntry
	}
}

// Hooks routes editor actions to the application (never to the device
// directly: Fyne callbacks must not perform device I/O synchronously).
type Hooks struct {
	// Offer enqueues a coalescable write (slider movement, ≤1 per 200 ms).
	Offer func(id uint32, v domain.ControlValue)
	// Commit enqueues an immediate write (select/check changes, entry
	// submits, slider drag end, buttons).
	Commit func(id uint32, v domain.ControlValue)
}

// ControlRenderer is one live control row. SetValue refreshes from
// read-backs; SetFlags reflects dependency state (inactive/grabbed).
type ControlRenderer interface {
	ID() uint32
	Object() fyne.CanvasObject
	SetValue(v domain.ControlValue)
	SetDisabled(disabled bool, reason string)
}

// newControlRenderer builds the renderer for one descriptor; values may be
// missing (nil) for write-only controls before the first write.
func newControlRenderer(d domain.ControlDescriptor, value *domain.ControlValue, hooks Hooks) ControlRenderer {
	r := &baseRenderer{desc: d, hooks: hooks}

	switch RendererKindFor(d) {
	case KindBoolean:
		r.build = buildBoolean
	case KindMenu:
		r.build = buildMenu
	case KindButton:
		r.build = buildButton
	case KindString:
		r.build = buildString
	case KindBitmask:
		r.build = buildBitmask
	case KindInteger:
		r.build = buildIntegerSlider
	case KindIntegerEntry:
		r.build = buildIntegerEntry
	case KindReadOnly:
		r.build = buildReadOnly
	default:
		r.build = buildUnsupported
	}

	if value != nil {
		r.value = *value
	}

	return r
}

// baseRenderer lazily builds its widget tree once and replays the current
// value/flags into it. echo guards programmatic updates against re-entrant
// write callbacks.
type baseRenderer struct {
	desc  domain.ControlDescriptor
	hooks Hooks
	value domain.ControlValue
	build func(*baseRenderer) fyne.CanvasObject
	echo  bool

	object   fyne.CanvasObject
	editor   fyne.Disableable
	setValue func(*baseRenderer, domain.ControlValue)
	reason   *widget.Label
}

func (r *baseRenderer) ID() uint32 {
	return r.desc.ID
}

func (r *baseRenderer) Object() fyne.CanvasObject {
	if r.object == nil {
		r.object = r.build(r)
		r.applyValue(r.value)
	}

	return r.object
}

func (r *baseRenderer) SetValue(v domain.ControlValue) {
	r.value = v

	if r.setValue != nil {
		r.setValue(r, v)
	}
}

func (r *baseRenderer) SetDisabled(disabled bool, reason string) {
	r.reason.SetText(reason)

	if disabled {
		r.reason.Show()
	} else {
		r.reason.Hide()
	}

	if r.editor != nil {
		r.editor.Disable()
		if !disabled {
			r.editor.Enable()
		}
	}
}

// applyValue pushes the current value into a freshly built widget tree.
func (r *baseRenderer) applyValue(v domain.ControlValue) {
	if r.setValue != nil {
		r.setValue(r, v)
	}
}

// self-guard helpers: programmatic updates must not echo back as writes
// (Slider.SetValue fires OnChanged AND OnChangeEnded; Select.SetSelected
// fires OnChanged).
func (r *baseRenderer) guard() func() {
	r.echo = true

	return func() { r.echo = false }
}

// commit routes a confirmed write; MODIFY_LAYOUT controls never ride the
// coalescer — they always take the exclusive path.
func (r *baseRenderer) commit(v domain.ControlValue) {
	if r.hooks.Commit != nil {
		r.hooks.Commit(r.desc.ID, v)
	}
}

// offer routes a coalescable write; MODIFY_LAYOUT controls bypass the
// coalescer entirely (layout changes cannot be a live slider stream).
func (r *baseRenderer) offer(v domain.ControlValue) {
	if r.desc.Flags.Has(domain.FlagModifyLayout) {
		r.commit(v)

		return
	}

	if r.hooks.Offer != nil {
		r.hooks.Offer(r.desc.ID, v)
	}
}

func buildUnsupported(r *baseRenderer) fyne.CanvasObject {
	note := widget.NewLabel("not editable (" + r.desc.Type.String() + " payload)")
	note.TextStyle = fyne.TextStyle{Italic: true}

	return withReason(r, container.NewHBox(note))
}

func buildReadOnly(r *baseRenderer) fyne.CanvasObject {
	val := widget.NewLabel("")
	r.setValue = func(br *baseRenderer, v domain.ControlValue) {
		val.SetText(v.String())
	}

	return withReason(r, val)
}

func buildBoolean(r *baseRenderer) fyne.CanvasObject {
	c := widget.NewCheck("", nil)

	r.setValue = func(br *baseRenderer, v domain.ControlValue) {
		defer br.guard()()

		c.SetChecked(v.Bool)
	}

	c.OnChanged = func(on bool) {
		if r.echo {
			return
		}

		r.commit(domain.NewBoolValue(r.desc.ID, on))
	}

	return withReason(r, c)
}

func buildMenu(r *baseRenderer) fyne.CanvasObject {
	// Duplicate labels are possible in exotic menus; keep a positional
	// mapping so selection always maps back to the exact index.
	labels := make([]string, 0, len(r.desc.MenuItems))
	indices := make([]int64, 0, len(r.desc.MenuItems))

	for _, m := range r.desc.MenuItems {
		labels = append(labels, m.Label())
		indices = append(indices, m.Index)
	}

	sel := widget.NewSelect(labels, nil)

	r.setValue = func(br *baseRenderer, v domain.ControlValue) {
		defer br.guard()()

		for i, idx := range indices {
			if idx == v.Int {
				sel.SetSelected(labels[i])

				return
			}
		}
	}

	sel.OnChanged = func(label string) {
		if r.echo {
			return
		}

		for i, l := range labels {
			if l == label {
				r.commit(domain.NewIntValue(r.desc.ID, r.desc.Type, indices[i]))

				return
			}
		}
	}

	return withReason(r, sel)
}

func buildButton(r *baseRenderer) fyne.CanvasObject {
	btn := widget.NewButton(r.desc.Name, nil)
	btn.OnTapped = func() {
		r.commit(domain.NewIntValue(r.desc.ID, domain.TypeButton, 0))
	}

	return withReason(r, btn)
}

func buildString(r *baseRenderer) fyne.CanvasObject {
	entry := widget.NewEntry()
	entry.SetPlaceHolder("value; Enter to apply")

	r.setValue = func(br *baseRenderer, v domain.ControlValue) {
		defer br.guard()()

		entry.SetText(v.Str)
	}

	entry.OnSubmitted = func(s string) {
		if r.echo {
			return
		}

		r.commit(domain.NewStringValue(r.desc.ID, s))
	}

	return withReason(r, entry)
}

func buildBitmask(r *baseRenderer) fyne.CanvasObject {
	entry := widget.NewEntry()
	entry.SetPlaceHolder("0x00000000; Enter to apply")

	r.setValue = func(br *baseRenderer, v domain.ControlValue) {
		defer br.guard()()

		entry.SetText(fmt.Sprintf("0x%08x", uint64(v.Int)&0xffffffff)) //nolint:gosec // bitmasks are u32; masking is the point
	}

	entry.OnSubmitted = func(s string) {
		if r.echo {
			return
		}

		v, err := strconv.ParseInt(strings.TrimSpace(s), 0, 64)
		if err != nil {
			entry.SetText(fmt.Sprintf("0x%08x", uint64(r.value.Int)&0xffffffff)) //nolint:gosec // bitmasks are u32; masking is the point

			return
		}

		r.commit(domain.NewIntValue(r.desc.ID, domain.TypeBitmask, v))
	}

	return withReason(r, entry)
}

// buildIntegerSlider renders a practical-range integer as a bidirectional
// slider + entry pair: dragging offers coalesced writes, release commits,
// and an entry submit normalizes (clamp + step) before committing.
func buildIntegerSlider(r *baseRenderer) fyne.CanvasObject {
	entry := widget.NewEntry()
	entry.TextStyle = fyne.TextStyle{Monospace: true}

	slider := widget.NewSlider(float64(r.desc.Min), float64(r.desc.Max))
	slider.Step = 1

	if r.desc.Step > 1 {
		slider.Step = float64(r.desc.Step)
	}

	setEntry := func(v int64) {
		entry.SetText(strconv.FormatInt(v, 10))
	}

	r.setValue = func(br *baseRenderer, v domain.ControlValue) {
		defer br.guard()()

		// Read-back is authoritative even if the driver reports something
		// outside the advertised range — never clamp the echo.
		slider.Value = float64(v.Int)
		slider.Refresh()
		setEntry(v.Int)
	}

	slider.OnChanged = func(f float64) {
		if r.echo {
			return
		}

		setEntry(int64(f))

		r.offer(domain.NewIntValue(r.desc.ID, r.desc.Type, int64(f)))
	}

	slider.OnChangeEnded = func(f float64) {
		if r.echo {
			return
		}

		r.commit(domain.NewIntValue(r.desc.ID, r.desc.Type, int64(f)))
	}

	// The editor is wrapped in a layout container; register BOTH the slider
	// and the entry as disableable targets explicitly — an inactive control
	// must not leave a freely editable entry next to a dead slider.
	r.editor = disableGroup{slider, entry}

	entry.OnSubmitted = func(s string) {
		if r.echo {
			return
		}

		v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			setEntry(int64(slider.Value))

			return
		}

		norm, err := viewmodel.NormalizeNumeric(v, r.desc.Min, r.desc.Max, r.desc.Step)
		if err != nil {
			setEntry(int64(slider.Value))

			return
		}

		defer r.guard()() // our own normalized update must not re-offer

		slider.Value = float64(norm)
		slider.Refresh()
		setEntry(norm)

		r.commit(domain.NewIntValue(r.desc.ID, r.desc.Type, norm))
	}

	return withReason(r, container.NewBorder(nil, nil, nil, entry, slider))
}

// buildIntegerEntry renders an awkward-range integer as a plain entry.
func buildIntegerEntry(r *baseRenderer) fyne.CanvasObject {
	entry := widget.NewEntry()
	entry.TextStyle = fyne.TextStyle{Monospace: true}

	r.setValue = func(br *baseRenderer, v domain.ControlValue) {
		defer br.guard()()

		entry.SetText(strconv.FormatInt(v.Int, 10))
	}

	entry.OnSubmitted = func(s string) {
		if r.echo {
			return
		}

		v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			entry.SetText(strconv.FormatInt(r.value.Int, 10))

			return
		}

		norm, err := viewmodel.NormalizeNumeric(v, r.desc.Min, r.desc.Max, r.desc.Step)
		if err != nil {
			entry.SetText(strconv.FormatInt(r.value.Int, 10))

			return
		}

		set := norm
		defer r.guard()()
		entry.SetText(strconv.FormatInt(set, 10))

		r.commit(domain.NewIntValue(r.desc.ID, r.desc.Type, norm))
	}

	return withReason(r, entry)
}

// disableGroup forwards enable/disable state to several widgets — used
// where one editor row holds more than one interactive widget (the
// slider+entry pair).
type disableGroup []fyne.Disableable

func (g disableGroup) Disable() {
	for _, d := range g {
		d.Disable()
	}
}

func (g disableGroup) Enable() {
	for _, d := range g {
		d.Enable()
	}
}

func (g disableGroup) Disabled() bool {
	for _, d := range g {
		if d.Disabled() {
			return true
		}
	}

	return false
}

// withReason wraps an editor with its name label and the dependency-state
// reason line.
func withReason(r *baseRenderer, editor fyne.CanvasObject) fyne.CanvasObject {
	if d, ok := editor.(fyne.Disableable); ok {
		r.editor = d
	}

	r.reason = widget.NewLabel("")
	r.reason.TextStyle = fyne.TextStyle{Italic: true}
	r.reason.Hide()
	r.reason.Importance = widget.WarningImportance

	name := widget.NewLabel(r.desc.Name)

	return container.NewVBox(
		container.NewBorder(nil, nil, name, nil, editor),
		r.reason,
	)
}
