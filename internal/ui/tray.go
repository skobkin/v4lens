package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"

	"git.skobk.in/skobkin/v4lens/internal/app"
	"git.skobk.in/skobkin/v4lens/internal/platform"
	"git.skobk.in/skobkin/v4lens/internal/resources"
)

// Tray integration: a flat menu (two levels max, no app-name header)
// rebuilt on device/preference events via SetSystemTrayMenu. Profile items
// carry the checkmark on the device's DEFAULT profile (the durable state
// V4Lens can truthfully reconstruct); tapping a profile applies it.
func (m *MainWindow) setupTray() {
	desk, ok := m.fa.(desktop.App)
	if !ok {
		m.log.Debug("system tray unavailable (app does not implement desktop.App)")

		return
	}

	m.tray = desk
	m.rebuildTray()
}

// rebuildTray re-renders the tray menu from current UI state. Must run on
// the UI goroutine (it reads UI state without locks).
func (m *MainWindow) rebuildTray() {
	if m.tray == nil {
		return
	}

	m.tray.SetSystemTrayMenu(m.buildTrayMenu())
}

// buildTrayMenu assembles the menu from the current device list and
// preferences. Pure with respect to device state — no device I/O.
func (m *MainWindow) buildTrayMenu() *fyne.Menu {
	open := fyne.NewMenuItemWithIcon("Open", theme.FolderOpenIcon(), m.showMainWindow)

	items := []*fyne.MenuItem{
		open,
		m.buildTrayPreviewItem(),
		fyne.NewMenuItemSeparator(),
	}

	for _, d := range m.devices {
		if !d.Connected {
			continue
		}

		items = append(items, m.buildTrayDeviceItem(d))
	}

	items = append(items,
		fyne.NewMenuItemSeparator(),
		m.buildTrayAutostartItem(),
		m.buildTrayApplyOnStartItem(),
		fyne.NewMenuItemWithIcon("Preferences…", theme.SettingsIcon(), m.showPreferences),
		fyne.NewMenuItemWithIcon("Quit", theme.NewThemedResource(resources.PowerIcon), m.onQuit),
	)

	return fyne.NewMenu("", items...)
}

// buildTrayPreviewItem is a direct action with exactly one usable device,
// a submenu with several, disabled with none.
func (m *MainWindow) buildTrayPreviewItem() *fyne.MenuItem {
	connected := m.connectedDeviceKeys()

	item := fyne.NewMenuItemWithIcon("Preview", theme.MediaPlayIcon(), nil)

	switch len(connected) {
	case 0:
		item.Disabled = true
	case 1:
		key := connected[0]
		item.Action = func() { m.previewDevice(key) }
	default:
		children := make([]*fyne.MenuItem, 0, len(connected))

		for _, key := range connected {
			k := key

			children = append(children, fyne.NewMenuItem(m.displayNameOf(k), func() { m.previewDevice(k) }))
		}

		item.ChildMenu = fyne.NewMenu("", children...)
	}

	return item
}

// buildTrayDeviceItem builds one device's profile submenu; the default
// profile (per preferences) carries the checkmark.
func (m *MainWindow) buildTrayDeviceItem(d app.DeviceView) *fyne.MenuItem {
	key := d.Identity.Key

	item := fyne.NewMenuItemWithIcon(m.displayNameOf(key), theme.NewThemedResource(resources.CameraIcon), nil)

	names, err := m.app.Profiles(key)
	if err != nil {
		m.log.Warn("tray profile listing failed", "device", key, "err", err)
	}

	if len(names) == 0 {
		empty := fyne.NewMenuItem("(no profiles)", nil)
		empty.Disabled = true
		item.ChildMenu = fyne.NewMenu("", empty)

		return item
	}

	def := m.app.DefaultProfile(key)

	children := make([]*fyne.MenuItem, 0, len(names))

	for _, name := range names {
		n := name

		child := fyne.NewMenuItem(n, func() { m.applyProfileFromTray(key, n) })
		child.Checked = def == n

		children = append(children, child)
	}

	item.ChildMenu = fyne.NewMenu("", children...)

	return item
}

func (m *MainWindow) buildTrayAutostartItem() *fyne.MenuItem {
	item := fyne.NewMenuItem("Start with system", m.toggleAutostart)
	item.Checked = platform.AutostartEnabled(m.dirs)

	return item
}

func (m *MainWindow) buildTrayApplyOnStartItem() *fyne.MenuItem {
	item := fyne.NewMenuItem("Apply profile on start", m.toggleApplyOnStart)
	item.Checked = m.app.Prefs().ApplyOnStart

	return item
}

// ---- tray actions ----

// showMainWindow raises the main window (tray Open, second-instance
// activation).
func (m *MainWindow) showMainWindow() {
	m.background = false
	m.win.Show()
	m.win.RequestFocus()
}

func (m *MainWindow) toggleAutostart() {
	var err error

	if platform.AutostartEnabled(m.dirs) {
		err = platform.DisableAutostart(m.dirs)
	} else {
		err = platform.EnableAutostart(m.dirs)
	}

	if err != nil {
		dialog.ShowError(err, m.win)
	}

	m.rebuildTray()
}

func (m *MainWindow) toggleApplyOnStart() {
	prefs := m.app.Prefs()
	prefs.ApplyOnStart = !prefs.ApplyOnStart

	if err := m.app.UpdatePrefs(prefs); err != nil {
		dialog.ShowError(err, m.win)
	}

	// PrefsChanged triggers the rebuild; keep it immediate anyway in case
	// persistence failed.
	m.rebuildTray()
}

// applyProfileFromTray applies a profile from the tray: the device becomes
// the selected context, then the standard apply path runs.
func (m *MainWindow) applyProfileFromTray(key, name string) {
	if key == "" || name == "" {
		return
	}

	if key != m.selected {
		m.selectDevice(key)
	}

	m.resetCoalescer()
	m.actions.applyNow(key, name)
}

// ---- small state helpers (UI goroutine) ----

func (m *MainWindow) connectedDeviceKeys() []string {
	keys := make([]string, 0, len(m.devices))

	for _, d := range m.devices {
		if d.Connected {
			keys = append(keys, d.Identity.Key)
		}
	}

	return keys
}

func (m *MainWindow) displayNameOf(key string) string {
	for _, d := range m.devices {
		if d.Identity.Key == key {
			return d.Identity.DisplayName
		}
	}

	return key
}
