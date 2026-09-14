package ui

import (
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"git.skobk.in/skobkin/v4lens/internal/domain"
	"git.skobk.in/skobkin/v4lens/internal/ui/viewmodel"
)

// FormatTab is the staged capture-configuration editor: input → pixel
// format → resolution → frame rate cascade with Apply/Revert. Formats and
// sizes describe the currently ACTIVE input (S_INPUT is never issued for
// discovery); intervals load lazily per concrete FOURCC+W+H; a staged
// input differing from the active one shows its downstream values as
// unvalidated until Apply. All selection logic lives in the Fyne-free
// FormatVM.
type FormatTab struct {
	// Lookup returns the selected device key ("" = none).
	Lookup func() string
	// Apply applies the staged config through the app engine path.
	Apply func(key string, cfg domain.CaptureConfig) (domain.CaptureConfig, error)
	// QueryIntervals lazily enumerates intervals for the staged triple.
	QueryIntervals func(key string, cc domain.FOURCC, w, h uint32) ([]domain.IntervalCap, error)
	// OnDirty re-renders footer state after staging changes.
	OnDirty func()
	// Dispatch defaults to fyne.Do; tests inject a synchronous runner.
	Dispatch func(fn func())

	win   fyne.Window
	box   *fyne.Container
	empty *widget.Label

	vm *viewmodel.FormatVM

	echo bool
	gen  int

	inputSel  *widget.Select
	formatSel *widget.Select
	sizeSel   *widget.Select
	wEntry    *widget.Entry
	hEntry    *widget.Entry
	fpsSel    *widget.Select
	fpsEntry  *widget.Entry
	status    *widget.Label
	current   *widget.Label
	applyBtn  *widget.Button
	revertBtn *widget.Button
}

// NewFormatTab builds the tab shell.
func NewFormatTab(win fyne.Window) *FormatTab {
	t := &FormatTab{
		win:   win,
		box:   container.NewVBox(),
		empty: widget.NewLabel("No device selected."),
	}

	t.box.Add(t.empty)

	return t
}

// Object returns the tab canvas object.
func (t *FormatTab) Object() fyne.CanvasObject {
	return t.box
}

func (t *FormatTab) dispatch(fn func()) {
	if t.Dispatch != nil {
		t.Dispatch(fn)

		return
	}

	fyne.Do(fn)
}

// SetSnapshot rebuilds the tab from a device snapshot. timePerFrame is
// derived from the effective config: an interval is reported only when the
// driver supports V4L2_CAP_TIMEPERFRAME.
func (t *FormatTab) SetSnapshot(inputs []domain.InputInfo, formats []domain.FormatDesc, active domain.CaptureConfig) {
	t.vm = viewmodel.NewFormatVM(
		inputs, formats, active,
		active.Interval != nil,
		nil, nil,
	)

	t.gen++
	t.rebuild()
}

// Clear resets the tab when no device is selected.
func (t *FormatTab) Clear() {
	t.vm = nil
	t.gen++
	t.box.Objects = []fyne.CanvasObject{t.empty}
}

// VM exposes the current model (nil without a device).
func (t *FormatTab) VM() *viewmodel.FormatVM {
	return t.vm
}

func (t *FormatTab) rebuild() {
	if t.vm == nil {
		t.box.Objects = []fyne.CanvasObject{t.empty}

		return
	}

	t.echo = true
	defer t.resetEcho()

	vm := t.vm

	t.inputSel = widget.NewSelect(inputOptions(vm), func(_ string) { t.onInputChanged() })
	t.formatSel = widget.NewSelect(formatOptions(vm), func(_ string) { t.onFormatChanged() })
	t.sizeSel = widget.NewSelect(sizeOptions(vm), func(label string) { t.onSizeLabel(label) })
	t.wEntry = numericEntry("width")
	t.hEntry = numericEntry("height")
	t.wEntry.OnSubmitted = func(string) { t.stageFromEntries() }
	t.hEntry.OnSubmitted = func(string) { t.stageFromEntries() }

	t.fpsSel = widget.NewSelect(nil, t.onFPSLabel)
	t.fpsEntry = widget.NewEntry()
	t.fpsEntry.SetPlaceHolder("exact interval, e.g. 1/60")
	t.fpsEntry.OnSubmitted = t.onFPSEntry

	t.status = widget.NewLabel("")
	t.current = widget.NewLabel("")
	t.applyBtn = widget.NewButton("Apply", t.onApply)
	t.revertBtn = widget.NewButton("Revert", t.onRevert)
	t.applyBtn.Disable()
	t.revertBtn.Disable()

	// Reflect the active config in the pickers (echo-guarded).
	t.inputSel.SetSelected(inputLabelFor(vm, vm.Active.Input))
	t.formatSel.SetSelected(string(vm.Active.FourCC))

	if hasDiscrete(vm) {
		t.sizeSel.SetSelected(sizeLabel(vm.Active.Width, vm.Active.Height))
	}

	setEntries(t.wEntry, t.hEntry, vm.Active.Width, vm.Active.Height)

	// Assemble: the frame-rate row exists ONLY when the device supports
	// configurable frame intervals.
	rows := []fyne.CanvasObject{
		container.NewHBox(widget.NewLabel("Input"), t.inputSel),
		container.NewHBox(widget.NewLabel("Pixel format"), t.formatSel),
	}

	if hasDiscrete(vm) {
		rows = append(rows, container.NewHBox(widget.NewLabel("Resolution"), t.sizeSel))
	}

	rows = append(rows,
		container.NewHBox(widget.NewLabel("Width"), withFixedWidth(80, t.wEntry),
			widget.NewLabel("Height"), withFixedWidth(80, t.hEntry)),
	)

	if vm.TimePerFrame {
		rows = append(rows,
			container.NewHBox(widget.NewLabel("Frame rate"), withFixedWidth(170, t.fpsSel),
				withFixedWidth(130, t.fpsEntry)),
		)
	}

	rows = append(rows,
		t.status,
		t.current,
		container.NewHBox(t.applyBtn, t.revertBtn),
	)

	t.box.Objects = rows

	t.loadIntervals()
	t.refresh()
}

func (t *FormatTab) resetEcho() { t.echo = false }

func (t *FormatTab) onInputChanged() {
	if t.echo || t.vm == nil {
		return
	}

	idx := t.inputSel.SelectedIndex()

	if idx < 0 || idx >= len(t.vm.Inputs) {
		return
	}

	t.vm.SelectInput(t.vm.Inputs[idx].Index)
	t.loadIntervals()
	t.refresh()
}

func (t *FormatTab) onFormatChanged() {
	if t.echo || t.vm == nil {
		return
	}

	idx := t.formatSel.SelectedIndex()

	if idx < 0 || idx >= len(t.vm.Formats) {
		return
	}

	if err := t.vm.SelectFormat(t.vm.Formats[idx].FourCC); err != nil {
		t.status.SetText(err.Error())
	}

	t.syncStagedSize()
	t.loadIntervals()
	t.refresh()
}

func (t *FormatTab) onSizeLabel(label string) {
	if t.echo || t.vm == nil {
		return
	}

	if w, h, ok := parseSizeLabel(label); ok {
		t.stageSize(w, h)
	}
}

func (t *FormatTab) stageSize(w, h uint32) {
	if err := t.vm.SelectSize(w, h); err != nil {
		t.status.SetText(err.Error())

		return
	}

	t.syncStagedSize()
	t.loadIntervals()
	t.refresh()
}

func (t *FormatTab) stageFromEntries() {
	if t.echo || t.vm == nil {
		return
	}

	w, werr := strconv.ParseUint(strings.TrimSpace(t.wEntry.Text), 10, 32)
	h, herr := strconv.ParseUint(strings.TrimSpace(t.hEntry.Text), 10, 32)

	if werr != nil || herr != nil {
		t.status.SetText("width/height must be numeric")

		return
	}

	t.stageSize(uint32(w), uint32(h)) //nolint:gosec // bounded by ParseUint 32
}

func (t *FormatTab) onFPSLabel(label string) {
	if t.echo || t.vm == nil {
		return
	}

	if iv, ok := intervalForLabel(label); ok {
		t.stageInterval(iv)
	}
}

func (t *FormatTab) onFPSEntry(s string) {
	if t.echo || t.vm == nil {
		return
	}

	iv, err := parseInterval(s)
	if err != nil {
		t.status.SetText("invalid interval: " + err.Error())

		return
	}

	t.stageInterval(iv)
}

func (t *FormatTab) stageInterval(iv domain.FrameInterval) {
	if err := t.vm.SelectInterval(iv); err != nil {
		t.status.SetText(err.Error())

		return
	}

	t.refresh()
}

// syncStagedSize updates the size picker/entries to the staged size.
func (t *FormatTab) syncStagedSize() {
	staged := t.vm.Staged()

	defer t.resetEcho()
	t.echo = true

	if hasDiscrete(t.vm) {
		t.sizeSel.SetSelected(sizeLabel(staged.Width, staged.Height))
	}

	setEntries(t.wEntry, t.hEntry, staged.Width, staged.Height)
}

func (t *FormatTab) onApply() {
	if t.vm == nil || t.Lookup == nil || t.Apply == nil {
		return
	}

	key := t.Lookup()

	if key == "" {
		return
	}

	staged := t.vm.Staged()

	t.applyBtn.Disable()
	t.status.SetText("Applying…")

	go func() {
		_, err := t.Apply(key, staged)

		t.dispatch(func() {
			if t.vm == nil {
				return
			}

			t.applyBtn.Enable()

			if err != nil {
				t.status.SetText("Apply failed: " + err.Error())
				dialog.ShowError(err, t.win)

				return
			}

			// The effective state arrives via the FormatSnapshot event;
			// here we only acknowledge.
			t.status.SetText("Applied.")
		})
	}()
}

func (t *FormatTab) onRevert() {
	if t.vm == nil {
		return
	}

	t.vm.SetActive(t.vm.Active, t.vm.Inputs, t.vm.Formats)
	t.rebuild()
}

// loadIntervals queries interval capabilities lazily for the staged
// FOURCC+W+H. Only the newest query's result populates the picker.
func (t *FormatTab) loadIntervals() {
	if t.vm == nil || !t.vm.TimePerFrame {
		return
	}

	t.gen++
	gen := t.gen

	t.fpsSel.Options = nil
	t.fpsSel.PlaceHolder = "loading…"
	t.fpsSel.Refresh()

	key := ""
	if t.Lookup != nil {
		key = t.Lookup()
	}

	staged := t.vm.Staged()

	if key == "" || t.QueryIntervals == nil {
		t.fpsSel.PlaceHolder = "unavailable"

		return
	}

	cc, w, h := staged.FourCC, staged.Width, staged.Height

	go func() {
		caps, err := t.QueryIntervals(key, cc, w, h)

		t.dispatch(func() {
			if t.vm == nil || gen != t.gen {
				return
			}

			t.fpsSel.PlaceHolder = "frame rate"

			if err != nil {
				t.fpsSel.Refresh()
				t.status.SetText("interval capabilities: " + err.Error())

				return
			}

			opts := make([]string, 0, len(caps))

			for _, c := range caps {
				opts = append(opts, fpsOptionsOf(c)...)
			}

			t.fpsSel.Options = opts
			t.fpsSel.Refresh()
		})
	}()
}

// refresh re-renders staged-vs-current state.
func (t *FormatTab) refresh() {
	if t.vm == nil {
		return
	}

	staged := t.vm.Staged()
	stagedDesc := captureDesc(staged)

	switch {
	case t.vm.Uncertain:
		t.status.TextStyle = fyne.TextStyle{Italic: true}
		t.status.SetText("Staged input differs from the active one — format/size/rate are unvalidated until Apply. " + stagedDesc)
	case t.vm.Dirty():
		t.status.TextStyle = fyne.TextStyle{}
		t.status.SetText(stagedDesc + " — unapplied")
	default:
		t.status.TextStyle = fyne.TextStyle{}
		t.status.SetText("No staged changes.")
	}

	t.current.SetText("Current: " + captureDesc(t.vm.Active))

	if t.vm.Dirty() {
		t.applyBtn.Enable()
		t.revertBtn.Enable()
	} else {
		t.applyBtn.Disable()
		t.revertBtn.Disable()
	}

	if t.OnDirty != nil {
		t.OnDirty()
	}
}

// fpsOptionsOf renders one interval capability as selectable labels:
// discrete entries as-is, stepwise/continuous as a bounded rational hint.
func fpsOptionsOf(c domain.IntervalCap) []string {
	if c.Type == domain.IntervalDiscrete {
		if len(c.Discrete) > 0 {
			out := make([]string, 0, len(c.Discrete))

			for _, iv := range c.Discrete {
				out = append(out, viewmodel.FPSLabel(iv)+"  ("+ivLabel(iv)+")")
			}

			return out
		}

		return []string{viewmodel.FPSLabel(c.Min) + "  (" + ivLabel(c.Min) + ")"}
	}

	return []string{fmt.Sprintf("%s – %s  (type an exact value below)", viewmodel.FPSLabel(c.Min), viewmodel.FPSLabel(c.Max))}
}

func ivLabel(iv domain.FrameInterval) string {
	return strconv.FormatUint(uint64(iv.Num), 10) + "/" + strconv.FormatUint(uint64(iv.Den), 10)
}

// intervalForLabel extracts the exact rational from a picker label.
func intervalForLabel(label string) (domain.FrameInterval, bool) {
	if i := strings.LastIndex(label, "("); i >= 0 {
		if iv, err := parseInterval(strings.TrimSuffix(label[i+1:], ")")); err == nil {
			return iv, true
		}
	}

	return domain.FrameInterval{}, false
}

// parseInterval parses an exact rational "num/den" (0/0 is never valid).
func parseInterval(s string) (domain.FrameInterval, error) {
	s = strings.TrimSpace(s)

	num, den, ok := strings.Cut(s, "/")
	if !ok {
		return domain.FrameInterval{}, fmt.Errorf("expected num/den, got %q", s)
	}

	n, err := strconv.ParseUint(strings.TrimSpace(num), 10, 32)
	if err != nil {
		return domain.FrameInterval{}, fmt.Errorf("bad numerator %q", num)
	}

	d, err := strconv.ParseUint(strings.TrimSpace(den), 10, 32)
	if err != nil {
		return domain.FrameInterval{}, fmt.Errorf("bad denominator %q", den)
	}

	iv := domain.FrameInterval{Num: uint32(n), Den: uint32(d)} //nolint:gosec // bounded by ParseUint 32

	if !iv.Valid() {
		return domain.FrameInterval{}, fmt.Errorf("0/0 is not a valid interval")
	}

	return iv, nil
}

func inputOptions(vm *viewmodel.FormatVM) []string {
	out := make([]string, 0, len(vm.Inputs))

	for _, in := range vm.Inputs {
		out = append(out, inputLabel(in))
	}

	return out
}

func inputLabel(in domain.InputInfo) string {
	return fmt.Sprintf("%d: %s", in.Index, in.Name)
}

func inputLabelFor(vm *viewmodel.FormatVM, idx uint32) string {
	for _, in := range vm.Inputs {
		if in.Index == idx {
			return inputLabel(in)
		}
	}

	return strconv.FormatUint(uint64(idx), 10)
}

func formatOptions(vm *viewmodel.FormatVM) []string {
	out := make([]string, 0, len(vm.Formats))

	for _, f := range vm.Formats {
		out = append(out, string(f.FourCC))
	}

	return out
}

func sizeOptions(vm *viewmodel.FormatVM) []string {
	choices := vm.SizeChoices()
	out := make([]string, 0, len(choices))

	for _, s := range choices {
		if s.Type == domain.FrameSizeDiscrete {
			out = append(out, sizeLabel(s.Width, s.Height))
		}
	}

	return out
}

func hasDiscrete(vm *viewmodel.FormatVM) bool {
	for _, s := range vm.SizeChoices() {
		if s.Type == domain.FrameSizeDiscrete {
			return true
		}
	}

	return false
}

func sizeLabel(w, h uint32) string {
	return fmt.Sprintf("%d×%d", w, h)
}

func parseSizeLabel(label string) (uint32, uint32, bool) {
	ws, hs, ok := strings.Cut(label, "×")
	if !ok {
		return 0, 0, false
	}

	w, err1 := strconv.ParseUint(ws, 10, 32)
	h, err2 := strconv.ParseUint(hs, 10, 32)

	return uint32(w), uint32(h), err1 == nil && err2 == nil //nolint:gosec // bounded by ParseUint 32
}

func setEntries(wEntry, hEntry *widget.Entry, w, h uint32) {
	wEntry.SetText(strconv.FormatUint(uint64(w), 10))
	hEntry.SetText(strconv.FormatUint(uint64(h), 10))
}

func numericEntry(placeholder string) *widget.Entry {
	e := widget.NewEntry()
	e.TextStyle = fyne.TextStyle{Monospace: true}
	e.SetPlaceHolder(placeholder)

	return e
}

// captureDesc renders a capture config for the status lines.
func captureDesc(cfg domain.CaptureConfig) string {
	s := fmt.Sprintf("%s %d×%d (input %d)", string(cfg.FourCC), cfg.Width, cfg.Height, cfg.Input)

	if cfg.Interval != nil {
		s += " @ " + viewmodel.FPSLabel(*cfg.Interval)
	}

	return s
}
