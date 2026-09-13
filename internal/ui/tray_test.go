package ui

import (
	"testing"

	"fyne.io/fyne/v2"

	"git.skobk.in/skobkin/v4lens/internal/app"
	"git.skobk.in/skobkin/v4lens/internal/domain"
)

func TestTrayMenuStructure(t *testing.T) {
	f := newUIFixture(t)
	m := f.window

	m.onDevicesChanged(f.deviceEvent())

	menu := m.buildTrayMenu()

	var labels []string

	for _, it := range menu.Items {
		if it.IsSeparator {
			labels = append(labels, "-")

			continue
		}

		labels = append(labels, it.Label)
	}

	want := []string{
		"Open",
		"Preview",
		"-",
		"Synthetic Cam 4K", // per-device profile submenu
		"-",
		"Start with system",
		"Apply profile on start",
		"Preferences…",
		"Quit",
	}

	if len(labels) != len(want) {
		t.Fatalf("menu labels %v, want %v", labels, want)
	}

	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("menu label %d = %q, want %q (all: %v)", i, labels[i], want[i], want)
		}
	}
}

func TestTrayPreviewItemStates(t *testing.T) {
	f := newUIFixture(t)
	m := f.window

	// No devices: Preview disabled.
	menu := m.buildTrayMenu()
	preview := menu.Items[1]

	if !preview.Disabled {
		t.Fatal("Preview must be disabled with no devices")
	}

	// One device: direct action.
	m.onDevicesChanged(f.deviceEvent())
	menu = m.buildTrayMenu()
	preview = menu.Items[1]

	if preview.Disabled {
		t.Fatal("Preview must be enabled with one usable device")
	}

	if preview.ChildMenu != nil {
		t.Fatal("single device must preview directly (no submenu)")
	}

	if preview.Action == nil {
		t.Fatal("direct Preview item must carry an action")
	}

	// Two devices: submenu.
	two := app.DevicesChanged{Devices: []app.DeviceView{
		{Identity: syntheticIdentity(), Connected: true},
		{Identity: domain.DeviceIdentity{
			Key:         fixtureKey + "-2",
			DisplayName: "Second Cam",
			Endpoints: []domain.EndpointInfo{
				{Node: "/dev/video2", Index: 0, Role: domain.RoleCapture, CaptureCaps: domain.CaptureCaps{SinglePlanar: true}},
			},
		}, Connected: true},
	}}

	m.onDevicesChanged(two)
	menu = m.buildTrayMenu()
	preview = menu.Items[1]

	if preview.ChildMenu == nil {
		t.Fatal("multiple devices must use a Preview submenu")
	}

	if got := len(preview.ChildMenu.Items); got != 2 {
		t.Fatalf("Preview submenu has %d items, want 2", got)
	}
}

func TestTrayMenuDefaultProfileChecked(t *testing.T) {
	f := newUIFixture(t)
	m := f.window

	m.onDevicesChanged(f.deviceEvent())

	// No profiles yet: an honest disabled placeholder.
	menu := m.buildTrayMenu()
	devItem := menu.Items[3]

	if devItem.ChildMenu == nil || len(devItem.ChildMenu.Items) != 1 {
		t.Fatal("device submenu must exist")
	}

	if !devItem.ChildMenu.Items[0].Disabled {
		t.Fatal("empty profile list must be a disabled placeholder")
	}

	// Two stored profiles, the second one default: its item is checked.
	if err := f.store.Save(fixtureKey, matchingProfile("day")); err != nil {
		t.Fatal(err)
	}

	if err := f.store.Save(fixtureKey, matchingProfile("night")); err != nil {
		t.Fatal(err)
	}

	if err := m.app.SetDefaultProfile(fixtureKey, "night"); err != nil {
		t.Fatal(err)
	}

	menu = m.buildTrayMenu()
	devItem = menu.Items[3]

	checked := 0

	for _, it := range devItem.ChildMenu.Items {
		switch {
		case it.Label == "night" && it.Checked:
			checked++
		case it.Label == "day" && it.Checked:
			t.Fatal("non-default profile must not be checked")
		}
	}

	if checked != 1 {
		t.Fatalf("default profile checkmark missing (checked=%d)", checked)
	}
}

func TestTrayApplyOnStartCheckboxReflectsPrefs(t *testing.T) {
	f := newUIFixture(t)
	m := f.window

	var item *fyne.MenuItem

	for _, it := range m.buildTrayMenu().Items {
		if it.Label == "Apply profile on start" {
			item = it

			break
		}
	}

	if item == nil {
		t.Fatal("Apply profile on start item missing")
	}

	if item.Checked != f.app.Prefs().ApplyOnStart {
		t.Fatal("checkbox must mirror the persisted preference")
	}
}
