package ui

import (
	"context"
	"fmt"
	"image/color"
	"log/slog"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"git.skobk.in/skobkin/v4lens/internal/app"
	"git.skobk.in/skobkin/v4lens/internal/domain"
	"git.skobk.in/skobkin/v4lens/internal/profile"
	"git.skobk.in/skobkin/v4lens/internal/ui/viewmodel"
)

// MainWindow is the main application shell: context bar (device select +
// status dot, profile select + default marker, Save/Preview/overflow),
// the four tabs (Image / PTZ / Advanced / Format) and the state footer.
// All bus events reach the widgets through dispatched functions — device
// I/O never runs inside Fyne callbacks.
type MainWindow struct {
	fa  fyne.App
	app *app.App
	log *slog.Logger
	win fyne.Window

	// Dispatch defaults to fyne.Do; tests inject a synchronous runner.
	Dispatch func(fn func())

	deviceSel  *widget.Select
	statusDot  *canvas.Circle
	profileSel *widget.Select
	saveBtn    *widget.Button
	previewBtn *widget.Button
	moreBtn    *widget.Button

	tabs   *container.AppTabs
	imageP *ControlPanel
	ptzP   *ControlPanel
	advP   *ControlPanel
	format *FormatTab
	footer *widget.Label

	actions  *profileActions
	prefsWin *PreferencesWindow
	unsub    func()

	// UI state lives only on the dispatch/UI goroutine — no locks.
	devices     []app.DeviceView
	labels      []string
	selected    string
	profiles    []string
	profileName string
	snap        *app.ControlsSnapshot
	fmtSnap     *app.FormatSnapshot
	coalescer   *viewmodel.Coalescer
	inflight    int
	lastMsg     string
}

// NewMainWindow builds the window (not yet subscribed to events).
func NewMainWindow(fa fyne.App, a *app.App, log *slog.Logger) *MainWindow {
	m := &MainWindow{fa: fa, app: a, log: log, win: fa.NewWindow("V4Lens")}

	m.statusDot = &canvas.Circle{FillColor: color.NRGBA{R: 0x88, G: 0x88, B: 0x88, A: 0xff}}
	m.statusDot.Resize(fyne.NewSize(12, 12))

	m.deviceSel = widget.NewSelect(nil, m.onDeviceSelected)
	m.deviceSel.PlaceHolder = "No device"

	m.profileSel = widget.NewSelect(nil, m.onProfileSelected)
	m.profileSel.PlaceHolder = "No profile selected"

	m.saveBtn = widget.NewButton("Save", nil)
	m.previewBtn = widget.NewButton("Preview", nil)

	// The preview window arrives with the streaming integration.
	m.previewBtn.Disable()
	m.previewBtn.SetText("Preview (soon)")

	m.moreBtn = widget.NewButtonWithIcon("", theme.MoreHorizontalIcon(), m.showOverflow)

	m.imageP = NewControlPanel(Hooks{})
	m.ptzP = NewControlPanel(Hooks{})
	m.advP = NewControlPanel(Hooks{})

	m.format = NewFormatTab(m.win)

	m.tabs = container.NewAppTabs(
		container.NewTabItem("Image", m.imageP.Object()),
		container.NewTabItem("PTZ", m.ptzP.Object()),
		container.NewTabItem("Advanced", m.advP.Object()),
		container.NewTabItem("Format", m.format.Object()),
	)

	m.footer = widget.NewLabel("Starting…")
	m.footer.TextStyle = fyne.TextStyle{Monospace: true}
	m.footer.Wrapping = fyne.TextWrapWord

	bar := container.NewHBox(
		m.statusDot,
		m.deviceSel,
		widget.NewLabel("Profile:"),
		m.profileSel,
		m.saveBtn,
		m.previewBtn,
		m.moreBtn,
	)

	m.win.SetContent(container.NewBorder(bar, m.footer, nil, nil, m.tabs))
	m.win.Resize(fyne.NewSize(880, 640))

	m.actions = &profileActions{
		app:           a,
		win:           m.win,
		LookupKey:     func() string { return m.selected },
		LookupProfile: func() string { return m.profileName },
		OnChange:      m.refreshProfiles,
	}

	m.format.Lookup = func() string { return m.selected }
	m.format.Apply = func(key string, cfg domain.CaptureConfig) (domain.CaptureConfig, error) {
		// An exclusive mutation is about to change device state under us:
		// stale coalesced writes must never land afterwards.
		m.resetCoalescer()

		return a.ApplyFormat(key, cfg)
	}
	m.format.QueryIntervals = a.QueryIntervals
	m.format.OnDirty = m.updateFooter

	m.saveBtn.OnTapped = m.actions.save

	m.win.SetCloseIntercept(m.onClose)

	return m
}

// Show reveals the window.
func (m *MainWindow) Show() {
	m.win.Show()
}

// Start subscribes to the app event bus and pumps events into the UI.
func (m *MainWindow) Start() {
	ch, unsub := m.app.Bus().Subscribe()
	m.unsub = unsub

	go func() {
		for ev := range ch {
			ev := ev
			m.dispatch(func() { m.onEvent(ev) })
		}
	}()
}

func (m *MainWindow) dispatch(fn func()) {
	if m.Dispatch != nil {
		m.Dispatch(fn)

		return
	}

	fyne.Do(fn)
}

func (m *MainWindow) onClose() {
	if m.unsub != nil {
		m.unsub()
	}

	m.stopCoalescer()
	m.app.Stop()
	m.fa.Quit()
}

func (m *MainWindow) onEvent(ev app.Event) {
	switch e := ev.(type) {
	case app.DevicesChanged:
		m.onDevicesChanged(e)
	case app.ControlsSnapshot:
		m.onControlsSnapshot(e)
	case app.FormatSnapshot:
		m.onFormatSnapshot(e)
	case app.ApplyFinished:
		m.onApplyFinished(e)
	case app.PrefsChanged:
		m.updateFooter()
	case app.ActivationRequested:
		m.win.Show()
	case app.PreviewState:
		// Populated by the preview integration.
	}
}

// ---- context bar ----

func (m *MainWindow) onDevicesChanged(ev app.DevicesChanged) {
	m.devices = ev.Devices

	m.labels = make([]string, 0, len(ev.Devices))

	for _, d := range ev.Devices {
		m.labels = append(m.labels, deviceLabel(d))
	}

	m.deviceSel.Options = m.labels

	// Keep the selection when the device is still present; a vanished
	// selection clears the whole context.
	still := false

	for _, d := range ev.Devices {
		if d.Identity.Key == m.selected {
			still = true
		}
	}

	if m.selected != "" && !still {
		m.selectDevice("")
	}

	if m.selected == "" && len(m.labels) > 0 {
		// Auto-select the first usable device on first sight.
		m.deviceSel.SetSelected(m.labels[0])
	}

	m.deviceSel.Refresh()
	m.updateStatusDot()
}

func deviceLabel(d app.DeviceView) string {
	label := d.Identity.DisplayName

	if !d.Connected {
		label += " (disconnected)"
	}

	return label
}

func (m *MainWindow) onDeviceSelected(label string) {
	for i, l := range m.labels {
		if l == label {
			m.selectDevice(m.devices[i].Identity.Key)

			return
		}
	}
}

// selectDevice switches the whole context to one device ("" = none).
func (m *MainWindow) selectDevice(key string) {
	m.stopCoalescer()

	m.selected = key
	m.profileName = ""
	m.snap = nil
	m.fmtSnap = nil
	m.lastMsg = ""
	m.inflight = 0

	if key == "" {
		m.imageP.SetContent(nil, nil, false)
		m.ptzP.SetContent(nil, nil, false)
		m.advP.SetContent(nil, nil, true)
		m.format.Clear()
		m.profileSel.Options = nil
		m.profiles = nil
		m.deviceSel.Refresh()
		m.updateStatusDot()
		m.updateFooter()

		return
	}

	m.setupCoalescer()
	m.refreshProfiles()
	m.updateStatusDot()
	m.updateFooter()

	// The device may have connected before we selected it: republish.
	go m.app.Refresh(key)
}

func (m *MainWindow) updateStatusDot() {
	c := color.NRGBA{R: 0x88, G: 0x88, B: 0x88, A: 0xff} // disconnected gray

	for _, d := range m.devices {
		if d.Identity.Key != m.selected {
			continue
		}

		switch {
		case d.Connected && d.Status == domain.StatusBusy:
			c = color.NRGBA{R: 0xf0, G: 0xa0, B: 0x20, A: 0xff} // busy amber
		case d.Connected:
			c = color.NRGBA{R: 0x30, G: 0xb0, B: 0x50, A: 0xff} // available green
		}
	}

	m.statusDot.FillColor = c
	canvas.Refresh(m.statusDot)
}

// refreshProfiles reloads the profile list and the default marker.
func (m *MainWindow) refreshProfiles() {
	if m.selected == "" {
		return
	}

	names, err := m.app.Profiles(m.selected)
	if err != nil {
		m.log.Warn("profile list failed", "device", m.selected, "err", err)
		names = nil
	}

	m.profiles = names

	def := m.app.DefaultProfile(m.selected)

	options := make([]string, 0, len(names))

	for _, n := range names {
		if n == def {
			options = append(options, "★ "+n+"  (default)")
		} else {
			options = append(options, n)
		}
	}

	m.profileSel.Options = options

	// Keep the selection stable through refreshes; echo-free.
	if m.profileName != "" {
		m.profileSel.SetSelected(profileOption(m.profileName, def))
	}

	m.profileSel.Refresh()
	m.updateFooter()
}

func profileOption(name, def string) string {
	if name == def {
		return "★ " + name + "  (default)"
	}

	return name
}

// onProfileSelected applies the chosen profile (the picker is an action,
// not just state). Programmatic selections are echo-guarded.
func (m *MainWindow) onProfileSelected(option string) {
	name := profileNameOf(option)

	if name == "" || m.selected == "" {
		return
	}

	if name == m.profileName {
		return
	}

	m.profileName = name

	// Selection IS application (UI_UX profile picker); pending manual
	// writes are invalidated first.
	m.resetCoalescer()
	m.actions.applyNow(m.selected, name)
}

// profileNameOf strips the default marker decoration.
func profileNameOf(option string) string {
	name := strings.TrimPrefix(option, "★ ")

	return strings.TrimSuffix(name, "  (default)")
}

// showOverflow opens the overflow menu dialog.
func (m *MainWindow) showOverflow() {
	items := []*widget.Button{
		widget.NewButton("Apply selected profile now", func() {
			m.dismissTopOverlay()
			m.resetCoalescer()
			m.actions.applyNow(m.selected, m.profileName)
		}),
		widget.NewButton("Save profile as…", func() {
			m.dismissTopOverlay()
			m.actions.saveAs()
		}),
		widget.NewButton("Rename profile…", func() {
			m.dismissTopOverlay()
			m.actions.rename()
		}),
		widget.NewButton("Set selected as default", func() {
			m.dismissTopOverlay()
			m.actions.setDefault()
		}),
		widget.NewButton("Delete profile…", func() {
			m.dismissTopOverlay()
			m.actions.remove()
		}),
		widget.NewButton("Preferences…", func() {
			m.dismissTopOverlay()
			m.prefsWin = NewPreferencesWindow(m.fa, m.app)
			m.prefsWin.Show()
		}),
	}

	box := container.NewVBox()

	for _, b := range items {
		box.Add(b)
	}

	dialog.ShowCustom("V4Lens", "Close", box, m.win)
}

func (m *MainWindow) dismissTopOverlay() {
	ov := m.win.Canvas().Overlays().Top()

	if ov != nil {
		m.win.Canvas().Overlays().Remove(ov)
	}
}

// ---- snapshots ----

func (m *MainWindow) onControlsSnapshot(ev app.ControlsSnapshot) {
	if ev.Key != m.selected {
		return
	}

	// A fresh read-back invalidates any in-flight write intents.
	m.resetCoalescer()

	m.snap = &ev

	var imageDescs, ptzDescs, advDescs []domain.ControlDescriptor

	for _, d := range ev.Descriptors {
		switch viewmodel.Classify(d).Tab { //nolint:exhaustive // everything else is Advanced
		case viewmodel.TabImage:
			imageDescs = append(imageDescs, d)
		case viewmodel.TabPTZ:
			ptzDescs = append(ptzDescs, d)
		default:
			advDescs = append(advDescs, d)
		}
	}

	hooks := m.controlHooks()

	m.imageP.hooks = hooks
	m.ptzP.hooks = hooks
	m.advP.hooks = hooks

	m.imageP.SetContent(imageDescs, ev.Values, false)
	m.ptzP.SetContent(ptzDescs, ev.Values, false)
	m.advP.SetContent(advDescs, ev.Values, true)

	m.updateFooter()
}

func (m *MainWindow) onFormatSnapshot(ev app.FormatSnapshot) {
	if ev.Key != m.selected {
		return
	}

	m.fmtSnap = &ev
	m.format.SetSnapshot(ev.Inputs, ev.Formats, ev.Active)

	m.updateFooter()
}

func (m *MainWindow) onApplyFinished(ev app.ApplyFinished) {
	if ev.Key != m.selected {
		return
	}

	switch ev.Result.Status {
	case profile.StatusFull:
		m.lastMsg = fmt.Sprintf("Profile %q applied.", ev.Result.Profile)
	case profile.StatusPartial:
		m.lastMsg = fmt.Sprintf("Profile %q applied with issues.", ev.Result.Profile)
		showApplyResult(ev.Result, m.win)
	default:
		m.lastMsg = fmt.Sprintf("Profile %q apply failed.", ev.Result.Profile)
		showApplyResult(ev.Result, m.win)
	}

	m.refreshProfiles()
	m.updateFooter()
}

// ---- control writes ----

// controlHooks builds the write routing for the control panels: slider
// movement coalesces, discrete edits commit, MODIFY_LAYOUT controls take
// the exclusive path.
func (m *MainWindow) controlHooks() Hooks {
	return Hooks{
		Offer: func(id uint32, v domain.ControlValue) {
			m.inflight++
			m.updateFooter()

			if m.coalescer != nil {
				m.coalescer.Offer(id, v)
			}
		},
		Commit: func(id uint32, v domain.ControlValue) {
			m.inflight++
			m.updateFooter()

			if m.coalescer == nil {
				m.inflight--

				return
			}

			if d := m.descriptorOf(id); d != nil && d.Flags.Has(domain.FlagModifyLayout) {
				// Never a live coalesced write: exclusive mutation, then
				// a snapshot refresh (layout may change capabilities).
				key := m.selected

				go func() {
					_, _ = m.app.WriteControlExclusive(key, id, v)
				}()

				return
			}

			m.coalescer.Commit(context.Background(), id, v)
		},
	}
}

// onWriteResult refreshes the renderer from the truthful effective value
// (never from the requested one) and surfaces errors in the footer.
func (m *MainWindow) onWriteResult(key string, id uint32, wr domain.WriteResult, err error) {
	if key != m.selected {
		return
	}

	m.inflight--

	if err != nil {
		m.lastMsg = fmt.Sprintf("Write failed: %v", err)
		m.updateFooter()

		return
	}

	if wr.Effective != nil {
		for _, p := range []*ControlPanel{m.imageP, m.ptzP, m.advP} {
			if r := p.Renderer(id); r != nil {
				r.SetValue(*wr.Effective)
			}
		}
	}

	m.updateFooter()
}

func (m *MainWindow) descriptorOf(id uint32) *domain.ControlDescriptor {
	if m.snap == nil {
		return nil
	}

	for i := range m.snap.Descriptors {
		if m.snap.Descriptors[i].ID == id {
			return &m.snap.Descriptors[i]
		}
	}

	return nil
}

// ---- coalescer ----

func (m *MainWindow) setupCoalescer() {
	m.stopCoalescer()

	key := m.selected

	if key == "" {
		return
	}

	m.coalescer = viewmodel.NewCoalescer(
		func(_ context.Context, id uint32, v domain.ControlValue) (domain.WriteResult, error) {
			return m.app.WriteControl(key, id, v)
		},
		func(id uint32, wr domain.WriteResult, err error) {
			m.dispatch(func() { m.onWriteResult(key, id, wr, err) })
		},
		viewmodel.DefaultCoalescePeriod,
		nil,
	)
}

func (m *MainWindow) stopCoalescer() {
	if m.coalescer != nil {
		m.coalescer.Stop()
		m.coalescer = nil
	}
}

func (m *MainWindow) resetCoalescer() {
	if m.coalescer != nil {
		m.coalescer.Reset()
	}
}

// ---- footer ----

// updateFooter renders the truthful state line: device/profile context,
// in-flight writes, last apply result and the profile dirty state (computed
// over profile-managed fields only, from effective read-backs).
func (m *MainWindow) updateFooter() {
	line := ""

	if m.selected != "" {
		line = m.selected

		if name := m.profileName; name != "" {
			diffs := m.profileDiffs()

			switch {
			case diffs < 0:
				line += " · profile " + name + ": state unknown"
			case diffs == 0:
				line += " · profile " + name + ": matches device"
			default:
				line += fmt.Sprintf(" · profile %s: %d difference(s) — Save to capture current state", name, diffs)
			}
		}
	} else {
		line = "No device selected"
	}

	if m.inflight > 0 {
		line += fmt.Sprintf(" · %d write(s) in flight", m.inflight)
	}

	if m.lastMsg != "" {
		line += " · " + m.lastMsg
	}

	m.footer.SetText(line)
}

// profileDiffs counts the profile-managed differences between the selected
// profile and the effective device state; -1 when not computable yet.
func (m *MainWindow) profileDiffs() int {
	if m.profileName == "" || m.snap == nil || m.fmtSnap == nil {
		return -1
	}

	p, err := m.app.ProfileStore().Load(m.selected, m.profileName)
	if err != nil {
		return -1
	}

	snapshot := profile.DeviceSnapshot{
		Capture:  m.fmtSnap.Active,
		Controls: m.snap.Values,
	}

	return len(profile.Dirty(p, snapshot))
}
