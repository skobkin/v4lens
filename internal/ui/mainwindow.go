package ui

import (
	"context"
	"fmt"
	"log/slog"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"git.skobk.in/skobkin/v4lens/internal/app"
	"git.skobk.in/skobkin/v4lens/internal/buildinfo"
	"git.skobk.in/skobkin/v4lens/internal/domain"
	"git.skobk.in/skobkin/v4lens/internal/platform"
	"git.skobk.in/skobkin/v4lens/internal/preview"
	"git.skobk.in/skobkin/v4lens/internal/profile"
	"git.skobk.in/skobkin/v4lens/internal/resources"
	"git.skobk.in/skobkin/v4lens/internal/ui/viewmodel"
)

// MainWindow is the main application shell: context bar (Camera group —
// device select + status dot + state text; Profile group — profile select
// + default marker; Save/Preview/overflow on the right), the four tabs
// (PTZ / Image / Advanced / Format) and the state footer. Image sits
// directly next to Advanced so related tuning groups (white balance) are
// one tab away. All bus events reach the widgets through dispatched
// functions — device I/O never runs inside Fyne callbacks.
type MainWindow struct {
	fa  fyne.App
	app *app.App
	log *slog.Logger
	win fyne.Window

	// Dispatch defaults to fyne.Do; tests inject a synchronous runner.
	Dispatch func(fn func())

	deviceSel   *widget.Select
	statusDot   *statusDot
	statusText  *widget.Label
	profileSel  *widget.Select
	defaultMark *fyne.Container
	saveBtn     *widget.Button
	previewBtn  *widget.Button
	moreBtn     *widget.Button

	tabs   *container.AppTabs
	imageP *ControlPanel
	ptzP   *ControlPanel
	advP   *ControlPanel
	format *FormatTab

	// Footer state bar (spec §7): profile context on the left, the
	// actionable message or the resting dirty warning on the right.
	footerBar    *fyne.Container
	profileStar  *widget.Icon
	profileTitle *widget.Label
	profileState *widget.Label
	warnIcon     *widget.Icon
	footerMsg    *widget.Label

	actions  *profileActions
	prefsWin *PreferencesWindow
	unsub    func()

	preview    *preview.Manager
	previewWin *PreviewWindow

	tray       desktop.App
	dirs       platform.Dirs
	background bool // started with --background (main window hidden)

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

// NewMainWindow builds the window (not yet subscribed to events). dirs
// feeds the tray's autostart integration.
func NewMainWindow(fa fyne.App, a *app.App, dirs platform.Dirs, log *slog.Logger) *MainWindow {
	// The window title leads with the build version so release builds are
	// identifiable at a glance ("V4Lens 0.1.0"; development builds read
	// "V4Lens dev").
	windowTitle := buildinfo.AppName + " " + buildinfo.Version
	m := &MainWindow{fa: fa, app: a, dirs: dirs, log: log, win: fa.NewWindow(windowTitle)}

	m.statusDot = newStatusDot()
	m.statusText = widget.NewLabel("")

	m.deviceSel = widget.NewSelect(nil, m.onDeviceSelected)
	m.deviceSel.PlaceHolder = "No device"

	m.profileSel = widget.NewSelect(nil, m.onProfileSelected)
	m.profileSel.PlaceHolder = "No profile selected"

	// The star lights up only while the selected profile is the device's
	// default; the option list itself stays plain.
	m.defaultMark = container.NewHBox(
		widget.NewIcon(theme.NewWarningThemedResource(resources.StarIcon)),
		widget.NewLabel("Default"),
	)
	m.defaultMark.Hide()

	m.saveBtn = widget.NewButtonWithIcon("Save", theme.DocumentSaveIcon(), nil)
	m.saveBtn.Importance = widget.HighImportance

	m.previewBtn = widget.NewButtonWithIcon("Preview", theme.MediaPlayIcon(), nil)

	// The preview window arrives with the streaming integration.
	m.previewBtn.Disable()
	m.previewBtn.SetText("Preview (soon)")

	m.moreBtn = widget.NewButtonWithIcon("", theme.MoreVerticalIcon(), m.showOverflow)

	m.imageP = NewControlPanel(Hooks{})
	m.ptzP = NewControlPanel(Hooks{})
	m.advP = NewControlPanel(Hooks{})

	m.format = NewFormatTab(m.win)

	m.tabs = container.NewAppTabs(
		container.NewTabItemWithIcon("PTZ", theme.NewThemedResource(resources.CrosshairIcon), m.ptzP.Object()),
		container.NewTabItemWithIcon("Image", theme.FileImageIcon(), m.imageP.Object()),
		container.NewTabItemWithIcon("Advanced", theme.SettingsIcon(), m.advP.Object()),
		container.NewTabItemWithIcon("Format", theme.DocumentIcon(), m.format.Object()),
	)

	m.profileStar = widget.NewIcon(theme.NewWarningThemedResource(resources.StarIcon))
	m.profileStar.Hide()

	m.profileTitle = widget.NewLabel("")
	m.profileTitle.TextStyle = fyne.TextStyle{Bold: true}

	m.profileState = widget.NewLabel("")
	m.profileState.Importance = widget.LowImportance

	m.warnIcon = widget.NewIcon(theme.WarningIcon())
	m.warnIcon.Hide()

	m.footerMsg = widget.NewLabel("Starting…")
	m.footerMsg.Truncation = fyne.TextTruncateEllipsis

	left := container.NewHBox(m.profileStar, container.NewVBox(m.profileTitle, m.profileState))
	right := container.NewHBox(m.warnIcon, m.footerMsg)

	m.footerBar = container.NewVBox(
		widget.NewSeparator(),
		container.NewHBox(left, layout.NewSpacer(), right),
	)

	// Two captioned groups (Camera / Profile) with a hairline divider
	// between them; the actions pin to the right edge.
	camGroup := container.NewVBox(captionLabel("Camera"),
		container.NewHBox(
			// Fixed width so the camera name is readable instead of clipped
			// to the select's minimum size.
			withFixedWidth(260, m.deviceSel),
			m.statusDot,
			m.statusText,
		))

	profGroup := container.NewVBox(captionLabel("Profile"),
		container.NewHBox(m.profileSel, m.defaultMark))

	// The right cell mirrors the left groups' two-row shape (caption row +
	// control row): buttons keep their natural height and line up with the
	// selects instead of stretching to the full bar height (Border
	// stretches side cells, HBox stretches its children).
	actions := container.NewVBox(captionLabel(""),
		container.NewHBox(m.saveBtn, m.previewBtn, m.moreBtn))

	bar := container.NewBorder(nil, nil,
		container.NewHBox(camGroup, newVRule(), profGroup),
		actions,
	)

	m.win.SetContent(container.NewBorder(bar, m.footerBar, nil, nil, m.tabs))
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

// Start subscribes to the app event bus, pumps events into the UI and
// installs the system tray menu. After subscribing it pulls the current
// device list once: the initial discovery may have run (and published its
// DevicesChanged) before this subscription existed, and the scanner only
// publishes on later changes.
func (m *MainWindow) Start() {
	ch, unsub := m.app.Bus().Subscribe()
	m.unsub = unsub

	go func() {
		for ev := range ch {
			ev := ev
			m.dispatch(func() { m.onEvent(ev) })
		}
	}()

	if devs := m.app.Devices(); len(devs) > 0 {
		m.dispatch(func() { m.onDevicesChanged(app.DevicesChanged{Devices: devs}) })
	}

	m.setupTray()
}

func (m *MainWindow) dispatch(fn func()) {
	if m.Dispatch != nil {
		m.Dispatch(fn)

		return
	}

	fyne.Do(fn)
}

// onClose runs when the main window is closed. The application stays
// resident in the tray: the window is hidden and its streaming lanes are
// released, but the process keeps running.
func (m *MainWindow) onClose() {
	m.stopCoalescer()

	// Stop every preview pipeline synchronously before anything else
	// touches the device.
	if m.preview != nil {
		m.preview.StopAll()
	}

	if m.previewWin != nil {
		m.previewWin.shutdown()
	}

	m.win.Hide()
}

// onQuit tears the whole application down (tray Quit action).
func (m *MainWindow) onQuit() {
	if m.unsub != nil {
		m.unsub()
	}

	m.onClose()

	m.app.Stop()
	m.fa.Quit()
}

// SetPreviewManager wires the streaming lane (the manager doubles as the
// apply engine's StreamOwner) and enables the Preview button.
func (m *MainWindow) SetPreviewManager(mgr *preview.Manager) {
	m.preview = mgr
	m.previewBtn.SetText("Preview")
	m.previewBtn.OnTapped = m.onPreview
	m.previewBtn.Enable()
}

// onPreview opens (or focuses) the preview window for the selected device.
// The callback itself performs no device I/O — the window's start path
// resolves the capture config on its own goroutine.
func (m *MainWindow) onPreview() {
	m.previewDevice(m.selected)
}

// previewDevice opens (or focuses) the preview window for one device,
// switching the main-window context when necessary.
func (m *MainWindow) previewDevice(key string) {
	if key == "" || m.preview == nil {
		return
	}

	if key != m.selected {
		m.selectDevice(key)
	}

	if m.previewWin != nil {
		if m.previewWin.key == key {
			m.previewWin.win.RequestFocus()

			return
		}

		// Different device: tear the old preview down, open fresh.
		m.previewWin.shutdown()
		m.previewWin.win.Close()
	}

	m.previewWin = NewPreviewWindow(m.fa, m.preview, m.app.ActiveCapture, m.log)
	m.previewWin.Show(key)
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
		m.rebuildTray()
	case app.ActivationRequested:
		// Second-instance activation: the running (possibly background)
		// instance reveals its main window.
		m.showMainWindow()
	case app.PreviewState:
		// Reserved for tray state display (post-MVP).
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
	m.rebuildTray()
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

	// A device switch closes the preview — the user re-taps Preview for
	// the new context (no implicit stream start on mere selection).
	if m.previewWin != nil {
		m.previewWin.shutdown()
		m.previewWin.win.Close()
		m.previewWin = nil
	}

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

// updateStatusDot renders the connection state as dot color + truthful
// text next to it. No invented states: unknown devices stay neutral.
func (m *MainWindow) updateStatusDot() {
	name := theme.ColorNameDisabled
	text := ""

	for _, d := range m.devices {
		if d.Identity.Key != m.selected {
			continue
		}

		switch {
		case d.Connected && d.Status == domain.StatusBusy:
			name, text = theme.ColorNameWarning, "Busy"
		case d.Connected:
			name, text = theme.ColorNameSuccess, "Available"
		default:
			text = "Disconnected"
		}
	}

	m.statusDot.SetColor(name)
	m.statusText.SetText(text)
	m.statusText.Refresh()
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
	m.profileSel.Options = names

	// Keep the selection stable through refreshes; echo-free.
	if m.profileName != "" {
		m.profileSel.SetSelected(m.profileName)
	}

	m.profileSel.Refresh()
	m.updateDefaultMark()
	m.updateFooter()
}

// updateDefaultMark shows the star marker only while the selected profile
// is the device's default.
func (m *MainWindow) updateDefaultMark() {
	if m.profileName != "" && m.profileName == m.app.DefaultProfile(m.selected) {
		m.defaultMark.Show()

		return
	}

	m.defaultMark.Hide()
}

// onProfileSelected applies the chosen profile (the picker is an action,
// not just state). Programmatic selections are echo-guarded.
func (m *MainWindow) onProfileSelected(option string) {
	if option == "" || m.selected == "" {
		return
	}

	if option == m.profileName {
		return
	}

	m.profileName = option
	m.updateDefaultMark()

	// Selection IS application (UI_UX profile picker); pending manual
	// writes are invalidated first.
	m.resetCoalescer()
	m.actions.applyNow(m.selected, option)
}

// showOverflow opens the overflow menu dialog.
// showPreferences opens the preferences window (overflow menu and tray).
func (m *MainWindow) showPreferences() {
	m.prefsWin = NewPreferencesWindow(m.fa, m.app)
	m.prefsWin.Show()
}

// showOverflow opens the profile/preference action menu right under the
// ⋮ button. Tapping an item dismisses the popup automatically.
func (m *MainWindow) showOverflow() {
	menu := fyne.NewMenu("",
		fyne.NewMenuItem("Apply selected profile now", func() {
			m.resetCoalescer()
			m.actions.applyNow(m.selected, m.profileName)
		}),
		fyne.NewMenuItem("Save profile as…", m.actions.saveAs),
		fyne.NewMenuItem("Rename profile…", m.actions.rename),
		fyne.NewMenuItem("Set selected as default", m.actions.setDefault),
		fyne.NewMenuItem("Delete profile…", m.actions.remove),
		fyne.NewMenuItem("Preferences…", m.showPreferences),
	)

	popup := widget.NewPopUpMenu(menu, m.win.Canvas())

	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(m.moreBtn)
	popup.ShowAtPosition(pos.Add(fyne.NewPos(0, m.moreBtn.Size().Height)))
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
	if m.background {
		// Window hidden (--background): apply issues surface as desktop
		// notifications (publication is cooldown-gated upstream); full
		// successes stay silent. No dialogs while nobody watches.
		if ev.Result.Status != profile.StatusFull {
			m.fa.SendNotification(fyne.NewNotification(
				"V4Lens — "+m.displayNameOf(ev.Key),
				applySummary(ev.Result),
			))
		}

		if ev.Key == m.selected {
			m.recordApplyMessage(ev)
			m.updateFooter()
		}

		return
	}

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

// recordApplyMessage updates the footer message from an apply result
// without presenting dialogs.
func (m *MainWindow) recordApplyMessage(ev app.ApplyFinished) {
	switch ev.Result.Status {
	case profile.StatusFull:
		m.lastMsg = fmt.Sprintf("Profile %q applied.", ev.Result.Profile)
	case profile.StatusPartial:
		m.lastMsg = fmt.Sprintf("Profile %q applied with issues.", ev.Result.Profile)
	default:
		m.lastMsg = fmt.Sprintf("Profile %q apply failed.", ev.Result.Profile)
	}
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

// updateFooter renders the state bar. Left: the profile context — star
// when the profile is the device's default, bold profile name, truthful
// dirty-state subtext. Right: the actionable truth of the moment wins —
// the last apply/write message, then in-flight writes, then the resting
// dirty warning (spec §7).
func (m *MainWindow) updateFooter() {
	switch {
	case m.selected == "":
		m.profileTitle.SetText("No device selected")
		m.profileState.SetText("")
	case m.profileName == "":
		m.profileTitle.SetText(m.displayNameOf(m.selected))
		m.profileState.SetText("No profile selected")
	default:
		m.profileTitle.SetText(m.profileName)

		diffs := m.profileDiffs()

		switch {
		case diffs < 0:
			m.profileState.SetText("Profile state unknown")
		case diffs == 0:
			m.profileState.SetText("Profile matches device")
		default:
			m.profileState.SetText(fmt.Sprintf("%d difference(s) — Save to capture current state", diffs))
		}
	}

	dirty := m.profileName != "" && m.profileDiffs() > 0

	switch {
	case m.lastMsg != "":
		m.footerMsg.SetText(m.lastMsg)
	case m.inflight > 0:
		m.footerMsg.SetText(fmt.Sprintf("%d write(s) in flight", m.inflight))
	case dirty:
		m.footerMsg.SetText("Camera settings differ from saved profile")
	default:
		m.footerMsg.SetText("")
	}

	if dirty && m.lastMsg == "" && m.inflight == 0 {
		m.warnIcon.Show()
	} else {
		m.warnIcon.Hide()
	}

	if m.selected != "" && m.profileName != "" && m.profileName == m.app.DefaultProfile(m.selected) {
		m.profileStar.Show()
	} else {
		m.profileStar.Hide()
	}
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
