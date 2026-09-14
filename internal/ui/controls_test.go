package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

func newTestEnv(t *testing.T) {
	t.Helper()

	test.NewApp()
}

// walk descends into containers and through our card widgets into their
// content (our rows compose only containers plus leaf editor widgets).
func walk(obj fyne.CanvasObject, fn func(fyne.CanvasObject)) {
	fn(obj)

	if c, ok := obj.(*fyne.Container); ok {
		for _, o := range c.Objects {
			walk(o, fn)
		}
	}

	if card, ok := obj.(*cardWidget); ok {
		walk(card.content, fn)
	}
}

// find returns the first canvas object of type T in the tree.
func find[T fyne.CanvasObject](obj fyne.CanvasObject) T {
	var (
		found T
		have  bool
	)

	walk(obj, func(o fyne.CanvasObject) {
		if have {
			return
		}

		if c, ok := o.(T); ok {
			found = c
			have = true
		}
	})

	return found
}

func TestRendererKindFor(t *testing.T) {
	cases := []struct {
		name string
		desc domain.ControlDescriptor
		want RendererKind
	}{
		{"boolean", domain.ControlDescriptor{ID: 1, Type: domain.TypeBoolean}, KindBoolean},
		{"menu", domain.ControlDescriptor{ID: 2, Type: domain.TypeMenu}, KindMenu},
		{"integer menu", domain.ControlDescriptor{ID: 3, Type: domain.TypeIntegerMenu}, KindMenu},
		{
			"practical integer",
			domain.ControlDescriptor{ID: 4, Type: domain.TypeInteger, Min: -64, Max: 64, Step: 1},
			KindInteger,
		},
		{
			"huge range integer",
			domain.ControlDescriptor{ID: 5, Type: domain.TypeInteger, Min: 0, Max: 5_000_000_000, Step: 1},
			KindIntegerEntry,
		},
		{
			"integer64 huge",
			domain.ControlDescriptor{ID: 6, Type: domain.TypeInteger64, Min: -1 << 40, Max: 1 << 40, Step: 1},
			KindIntegerEntry,
		},
		{
			"scalar u8",
			domain.ControlDescriptor{ID: 7, Type: domain.TypeU8, Elems: 1, Min: 0, Max: 255, Step: 1},
			KindInteger,
		},
		{
			"array u8 unsupported",
			domain.ControlDescriptor{ID: 8, Type: domain.TypeU8, Elems: 4},
			KindUnsupported,
		},
		{"bitmask", domain.ControlDescriptor{ID: 9, Type: domain.TypeBitmask}, KindBitmask},
		{"button", domain.ControlDescriptor{ID: 10, Type: domain.TypeButton}, KindButton},
		{"string", domain.ControlDescriptor{ID: 11, Type: domain.TypeString}, KindString},
		{
			"read-only",
			domain.ControlDescriptor{ID: 12, Type: domain.TypeBoolean, Flags: domain.FlagReadOnly},
			KindReadOnly,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RendererKindFor(tc.desc); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestBooleanRendererRoutesCommits(t *testing.T) {
	newTestEnv(t)

	var commits []domain.ControlValue

	r := newControlRenderer(
		domain.ControlDescriptor{ID: 0x0098090c, Name: "Auto WB", Type: domain.TypeBoolean},
		nil,
		Hooks{Commit: func(id uint32, v domain.ControlValue) {
			commits = append(commits, v)
		}},
	)

	check := find[*widget.Check](r.Object())

	if check == nil {
		t.Fatal("no checkbox rendered")
	}

	check.SetChecked(true)

	if len(commits) != 1 || !commits[0].Bool {
		t.Fatalf("commits = %+v", commits)
	}

	// A read-back (SetValue) must not echo back as a write.
	r.SetValue(domain.NewBoolValue(0x0098090c, false))

	if len(commits) != 1 {
		t.Fatalf("read-back echoed as write: %+v", commits)
	}

	if check.Checked {
		t.Fatal("read-back did not update the checkbox")
	}
}

func TestMenuRendererMapsLabelsToIndices(t *testing.T) {
	newTestEnv(t)

	var commits []domain.ControlValue

	r := newControlRenderer(
		domain.ControlDescriptor{
			ID:   1,
			Name: "Power line",
			Type: domain.TypeMenu,
			MenuItems: []domain.MenuItem{
				{Index: 0, Name: "50 Hz"},
				{Index: 1, Name: "60 Hz"},
			},
		},
		nil,
		Hooks{Commit: func(_ uint32, v domain.ControlValue) {
			commits = append(commits, v)
		}},
	)

	sel := find[*widget.Select](r.Object())

	if sel == nil {
		t.Fatal("no select rendered")
	}

	// Read-back: index 1 selects "60 Hz" without echoing a write.
	r.SetValue(domain.NewIntValue(1, domain.TypeMenu, 1))

	if sel.Selected != "60 Hz" {
		t.Fatalf("selected = %q", sel.Selected)
	}

	// User selection maps the label back to the exact index.
	sel.SetSelected("50 Hz")

	if len(commits) != 1 || commits[0].Int != 0 {
		t.Fatalf("commits = %+v", commits)
	}
}

func TestIntegerSliderRendererBidirectional(t *testing.T) {
	newTestEnv(t)

	var offers, commits []domain.ControlValue

	r := newControlRenderer(
		domain.ControlDescriptor{
			ID: 0x00980900, Name: "Brightness",
			Type: domain.TypeInteger, Min: 0, Max: 100, Step: 10,
		},
		&domain.ControlValue{ID: 0x00980900, Type: domain.TypeInteger, Int: 50},
		Hooks{
			Offer:  func(_ uint32, v domain.ControlValue) { offers = append(offers, v) },
			Commit: func(_ uint32, v domain.ControlValue) { commits = append(commits, v) },
		},
	)

	obj := r.Object()

	slider := find[*widget.Slider](obj)
	entry := find[*widget.Entry](obj)

	if slider == nil || entry == nil {
		t.Fatal("slider/entry missing")
	}

	// Drag movement offers coalesced writes.
	slider.OnChanged(70)

	if len(offers) != 1 || offers[0].Int != 70 {
		t.Fatalf("offers = %+v", offers)
	}

	// Release commits.
	slider.OnChangeEnded(70)

	if len(commits) != 1 || commits[0].Int != 70 {
		t.Fatalf("commits = %+v", commits)
	}

	// Entry submit normalizes to the nearest step before committing.
	entry.OnSubmitted("51")

	if len(commits) != 2 || commits[1].Int != 50 {
		t.Fatalf("off-step submit committed %+v, want 50", commits)
	}

	if slider.Value != 50 {
		t.Fatalf("slider not synced to normalized value: %v", slider.Value)
	}

	// Read-back refreshes both widgets without echoing writes.
	r.SetValue(domain.NewIntValue(0x00980900, domain.TypeInteger, 30))

	if slider.Value != 30 || entry.Text != "30" {
		t.Fatalf("read-back not rendered: slider=%v entry=%q", slider.Value, entry.Text)
	}

	if len(offers) != 1 || len(commits) != 2 {
		t.Fatal("read-back echoed as write")
	}
}

// An INACTIVE control disables the whole editor row: the value entry next
// to the slider must not stay freely editable (regression: only the slider
// was registered as the disableable target).
func TestInactiveSliderDisablesEntry(t *testing.T) {
	newTestEnv(t)

	r := newControlRenderer(
		domain.ControlDescriptor{
			ID: 0x009a0902, Name: "Exposure Time, Absolute",
			Type: domain.TypeInteger, Min: 0, Max: 500, Step: 1,
		},
		&domain.ControlValue{ID: 0x009a0902, Type: domain.TypeInteger, Int: 156},
		Hooks{},
	)

	obj := r.Object()

	slider := find[*widget.Slider](obj)
	entry := find[*widget.Entry](obj)

	if slider == nil || entry == nil {
		t.Fatal("slider/entry missing")
	}

	r.SetDisabled(true, "inactive — enable its prerequisite control first")

	if !entry.Disabled() || !slider.Disabled() {
		t.Fatalf("inactive control editable: entry=%v slider=%v", entry.Disabled(), slider.Disabled())
	}

	r.SetDisabled(false, "")

	if entry.Disabled() || slider.Disabled() {
		t.Fatal("controls not re-enabled after the dependency cleared")
	}
}

func TestIntegerEntryRendererNormalizes(t *testing.T) {
	newTestEnv(t)

	var commits []domain.ControlValue

	r := newControlRenderer(
		domain.ControlDescriptor{
			ID: 1, Name: "Huge", Type: domain.TypeInteger,
			Min: 0, Max: 5_000_000_000, Step: 1,
		},
		&domain.ControlValue{ID: 1, Type: domain.TypeInteger, Int: 1000},
		Hooks{Commit: func(_ uint32, v domain.ControlValue) {
			commits = append(commits, v)
		}},
	)

	entry := find[*widget.Entry](r.Object())

	entry.OnSubmitted("99999999999999") // clamps to max

	if len(commits) != 1 || commits[0].Int != 5_000_000_000 {
		t.Fatalf("commits = %+v", commits)
	}

	entry.OnSubmitted("not a number")

	if len(commits) != 1 {
		t.Fatal("garbage input must not write")
	}
}

func TestBitmaskRendererParsesHex(t *testing.T) {
	newTestEnv(t)

	var commits []domain.ControlValue

	r := newControlRenderer(
		domain.ControlDescriptor{ID: 1, Name: "Mask", Type: domain.TypeBitmask},
		&domain.ControlValue{ID: 1, Type: domain.TypeBitmask, Int: 0xdeadbeef},
		Hooks{Commit: func(_ uint32, v domain.ControlValue) {
			commits = append(commits, v)
		}},
	)

	entry := find[*widget.Entry](r.Object())

	if entry.Text != "0xdeadbeef" {
		t.Fatalf("rendered = %q", entry.Text)
	}

	entry.OnSubmitted("0x0000ff00")

	if len(commits) != 1 || commits[0].Int != 0xff00 {
		t.Fatalf("commits = %+v", commits)
	}

	entry.OnSubmitted("zzz")

	if len(commits) != 1 {
		t.Fatal("invalid hex must not write")
	}
}

func TestModifyLayoutBypassesCoalescer(t *testing.T) {
	newTestEnv(t)

	var offers, commits int

	r := newControlRenderer(
		domain.ControlDescriptor{
			ID: 1, Name: "Layout", Type: domain.TypeInteger,
			Min: 0, Max: 10, Step: 1,
			Flags: domain.FlagModifyLayout,
		},
		&domain.ControlValue{ID: 1, Type: domain.TypeInteger, Int: 5},
		Hooks{
			Offer:  func(uint32, domain.ControlValue) { offers++ },
			Commit: func(uint32, domain.ControlValue) { commits++ },
		},
	)

	slider := find[*widget.Slider](r.Object())

	// A MODIFY_LAYOUT control never rides the coalescer: dragging routes
	// straight to the exclusive commit path.
	slider.OnChanged(3)

	if offers != 0 || commits != 1 {
		t.Fatalf("offers=%d commits=%d", offers, commits)
	}
}

func TestControlPanelGroupsAndRegisters(t *testing.T) {
	newTestEnv(t)

	descs := []domain.ControlDescriptor{
		{ID: 0x00980900, Name: "Brightness", Type: domain.TypeInteger, Min: 0, Max: 10, Step: 1, ClassID: 0x00980001, ClassName: "User Controls"},
		{ID: 0x0098090c, Name: "Auto WB", Type: domain.TypeBoolean, ClassID: 0x00980001, ClassName: "User Controls"},
		{ID: 0x009a0908, Name: "Pan Absolute", Type: domain.TypeInteger, Min: 0, Max: 10, Step: 1, ClassID: 0x009a0001, ClassName: "Camera Controls"},
		{ID: 0x01990920, Name: "Vendor Blob", Type: domain.TypeU8, Elems: 4, ClassID: 0x01990001, ClassName: "UVC Vendor"},
		{ID: 0x01990921, Name: "Vendor knob", Type: domain.TypeInteger, Min: 0, Max: 10, Step: 1, ClassID: 0x01990001, ClassName: "UVC Vendor"},
	}

	values := map[uint32]domain.ControlValue{
		0x00980900: {ID: 0x00980900, Type: domain.TypeInteger, Int: 5},
	}

	p := NewControlPanel(Hooks{})
	p.SetContent(descs, values, true)

	for _, id := range []uint32{0x00980900, 0x0098090c, 0x009a0908, 0x01990920, 0x01990921} {
		if p.Renderer(id) == nil {
			t.Fatalf("renderer for 0x%08x missing — unknown controls must never disappear", id)
		}
	}

	// Advanced grouping keeps driver order within class groups and class
	// groups in first-appearance order; the curated white balance control
	// keeps its own titled group instead of the driver class name:
	// User Controls, White Balance, Camera Controls, UVC Vendor. The bold
	// titles now live inside the group cards, so walk descends into them.
	var titles []string

	walk(p.box, func(o fyne.CanvasObject) {
		if l, ok := o.(*widget.Label); ok && l.TextStyle.Bold {
			titles = append(titles, l.Text)
		}
	})

	want := []string{"User Controls", "White Balance", "Camera Controls", "UVC Vendor"}

	if len(titles) != len(want) {
		t.Fatalf("group titles = %v, want %v", titles, want)
	}

	for i := range want {
		if titles[i] != want[i] {
			t.Fatalf("group titles = %v, want %v", titles, want)
		}
	}
}

func TestControlPanelReplaysDisabledFlags(t *testing.T) {
	newTestEnv(t)

	descs := []domain.ControlDescriptor{
		{ID: 1, Name: "Grabbed knob", Type: domain.TypeInteger, Min: 0, Max: 10, Step: 1,
			CurFlags: domain.FlagGrabbed},
	}

	p := NewControlPanel(Hooks{})
	p.SetContent(descs, nil, false)

	slider := find[*widget.Slider](p.Renderer(1).Object())

	if slider == nil || !slider.Disabled() {
		t.Fatal("grabbed control must render disabled")
	}
}
