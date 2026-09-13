package ui

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"git.skobk.in/skobkin/v4lens/internal/app"
	"git.skobk.in/skobkin/v4lens/internal/config"
	"git.skobk.in/skobkin/v4lens/internal/device"
	"git.skobk.in/skobkin/v4lens/internal/domain"
	"git.skobk.in/skobkin/v4lens/internal/profile"
)

// uiFixture carries the minimal app-layer wiring the main window needs.
type uiFixture struct {
	t      *testing.T
	app    *app.App
	store  *profile.Store
	window *MainWindow
	q      dispatchQueue
}

const fixtureKey = "synthetic-cam-4k-0001abcd"

func newUIFixture(t *testing.T) *uiFixture {
	t.Helper()

	newTestEnv(t)

	fa := test.NewApp()

	dir := t.TempDir()

	cfg := config.NewStore(filepath.Join(dir, "config.yaml"))

	if err := cfg.Save(config.Default()); err != nil {
		t.Fatal(err)
	}

	store := profile.NewStore(filepath.Join(dir, "profiles"), nil)

	a, err := app.New(app.Options{
		Config:      cfg,
		Profiles:    store,
		OpenBackend: func(domain.DeviceIdentity) (device.Handle, error) { return nil, fmt.Errorf("not used") },
	})
	if err != nil {
		t.Fatal(err)
	}

	m := NewMainWindow(fa, a, discardLogger())

	sync := func(fn func()) { fn() }
	m.Dispatch = sync

	// The format tab receives dispatches from query goroutines; park them
	// until the test drains them on the test goroutine.
	q := dispatchQueue(make(chan func()))
	m.format.Dispatch = q.dispatch

	f := &uiFixture{t: t, app: a, store: store, window: m, q: q}
	t.Cleanup(m.stopCoalescer)

	return f
}

// deviceEvent is the synthetic single-device device list.
func (f *uiFixture) deviceEvent() app.DevicesChanged {
	return app.DevicesChanged{Devices: []app.DeviceView{{
		Identity:  syntheticIdentity(),
		Connected: true,
	}}}
}

func syntheticIdentity() domain.DeviceIdentity {
	return domain.DeviceIdentity{
		Key:         fixtureKey,
		DisplayName: "Synthetic Cam 4K",
		Serial:      "TEST-PW513-0001",
		Endpoints: []domain.EndpointInfo{
			{Node: "/dev/video0", Index: 0, Role: domain.RoleCapture, CaptureCaps: domain.CaptureCaps{SinglePlanar: true}},
		},
	}
}

func controlSnapshot() app.ControlsSnapshot {
	descs := []domain.ControlDescriptor{
		{ID: 0x00980900, Name: "Brightness", Type: domain.TypeInteger, Min: 0, Max: 10, Step: 1, ClassID: 0x00980001, ClassName: "User Controls"},
		{ID: 0x01990920, Name: "Vendor Blob", Type: domain.TypeU8, Elems: 4, ClassID: 0x01990001, ClassName: "UVC Vendor"},
	}

	values := map[uint32]domain.ControlValue{
		0x00980900: {ID: 0x00980900, Type: domain.TypeInteger, Int: 5},
	}

	return app.ControlsSnapshot{Key: fixtureKey, Descriptors: descs, Values: values}
}

func formatSnapshot() app.FormatSnapshot {
	return app.FormatSnapshot{
		Key: fixtureKey,
		Inputs: []domain.InputInfo{
			{Index: 0, Name: "Camera"},
		},
		Formats: []domain.FormatDesc{{
			FourCC: "UYVY",
			Sizes: []domain.FrameSize{
				{Type: domain.FrameSizeDiscrete, Width: 1280, Height: 720},
				{Type: domain.FrameSizeDiscrete, Width: 640, Height: 480},
			},
		}},
		Active: domain.CaptureConfig{
			Input: 0, FourCC: "UYVY", Width: 1280, Height: 720,
			Interval: &domain.FrameInterval{Num: 1, Den: 30},
		},
	}
}

// matchingProfile returns a profile equal to the fixture snapshots.
func matchingProfile(name string) *profile.Profile {
	bright := int64(5)

	return &profile.Profile{
		Name: name,
		Match: profile.Match{
			Key:         fixtureKey,
			DisplayName: "Synthetic Cam 4K",
			Serial:      "TEST-PW513-0001",
		},
		Capture: &domain.CaptureConfig{
			Input: 0, FourCC: "UYVY", Width: 1280, Height: 720,
			Interval: &domain.FrameInterval{Num: 1, Den: 30},
		},
		Controls: []profile.ControlSetting{{
			ID:   0x00980900,
			Name: "Brightness",
			Value: domain.ControlValue{
				ID: 0x00980900, Type: domain.TypeInteger, Int: bright,
			},
		}},
	}
}

func TestMainWindowDevicesAndPanelsPopulate(t *testing.T) {
	f := newUIFixture(t)
	m := f.window

	m.onEvent(f.deviceEvent())

	if len(m.deviceSel.Options) != 1 {
		t.Fatalf("device options = %v", m.deviceSel.Options)
	}

	m.onEvent(controlSnapshot())

	if m.imageP.Renderer(0x00980900) == nil {
		t.Fatal("brightness renderer missing from Image tab")
	}

	if m.advP.Renderer(0x01990920) == nil {
		t.Fatal("compound control must stay visible in Advanced")
	}

	m.onEvent(formatSnapshot())
	f.q.expect() // lazy interval query dispatch

	if m.format.VM() == nil {
		t.Fatal("format tab model missing after snapshot")
	}
}

func TestMainWindowFooterDirtyStates(t *testing.T) {
	f := newUIFixture(t)
	m := f.window

	if err := f.store.Save(fixtureKey, matchingProfile("Video-Calls")); err != nil {
		t.Fatal(err)
	}

	m.onEvent(f.deviceEvent())
	m.onDeviceSelected("Synthetic Cam 4K")
	m.onEvent(controlSnapshot())
	m.onEvent(formatSnapshot())
	f.q.expect() // lazy interval query dispatch

	m.profileName = "Video-Calls"

	m.updateFooter()

	if got := m.footer.Text; !contains(got, "matches device") {
		t.Fatalf("footer = %q, want matching state", got)
	}

	// Device drifts: bump the snapshot brightness.
	snap := controlSnapshot()
	snap.Values[0x00980900] = domain.ControlValue{ID: 0x00980900, Type: domain.TypeInteger, Int: 9}
	m.onEvent(snap)

	if got := m.footer.Text; !contains(got, "1 difference(s)") {
		t.Fatalf("footer = %q, want dirty state", got)
	}
}

func TestMainWindowApplyFinishedPartialShowsIssue(t *testing.T) {
	f := newUIFixture(t)
	m := f.window

	m.onEvent(f.deviceEvent())
	m.onDeviceSelected("Synthetic Cam 4K")

	res := profile.ApplyResult{
		Key:     fixtureKey,
		Profile: "Video-Calls",
		Capture: profile.CaptureOutcome{Attempted: true, Status: profile.StepSkipped, Reason: "device busy"},
		Steps: []profile.StepOutcome{
			{ID: 0x00980900, Name: "Brightness", Status: profile.StepFailed, Reason: "injected failure"},
		},
		Status: profile.StatusPartial,
	}

	m.onEvent(app.ApplyFinished{Key: fixtureKey, Result: res})

	if !contains(m.footer.Text, "issues") {
		t.Fatalf("footer = %q, want apply-issue state", m.footer.Text)
	}
}

func TestMainWindowWriteResultUpdatesRenderer(t *testing.T) {
	f := newUIFixture(t)
	m := f.window

	m.onEvent(f.deviceEvent())
	m.onDeviceSelected("Synthetic Cam 4K")
	m.onEvent(controlSnapshot())

	r := m.imageP.Renderer(0x00980900)

	m.onWriteResult(fixtureKey, 0x00980900,
		domain.WriteResult{Accepted: true, Effective: &domain.ControlValue{ID: 0x00980900, Type: domain.TypeInteger, Int: 7}},
		nil,
	)

	entry := find[*widget.Entry](r.Object())

	if entry == nil || entry.Text != "7" {
		t.Fatalf("renderer not refreshed from effective read-back: %v", entry)
	}

	if contains(m.footer.Text, "1 write(s) in flight") {
		t.Fatalf("footer = %q, in-flight count not settled", m.footer.Text)
	}
}

func TestFormatTabStagingAndApplyFlow(t *testing.T) {
	f := newUIFixture(t)

	tab := f.window.format

	q := dispatchQueue(make(chan func()))
	tab.Dispatch = q.dispatch

	applied := make(chan domain.CaptureConfig, 1)

	tab.Lookup = func() string { return fixtureKey }

	tab.Apply = func(key string, cfg domain.CaptureConfig) (domain.CaptureConfig, error) {
		if key != fixtureKey {
			t.Errorf("apply for wrong key %q", key)
		}

		applied <- cfg

		return cfg, nil
	}

	tab.QueryIntervals = func(key string, cc domain.FOURCC, w, h uint32) ([]domain.IntervalCap, error) {
		return []domain.IntervalCap{
			{Type: domain.IntervalDiscrete, Min: domain.FrameInterval{Num: 1, Den: 60}},
		}, nil
	}

	tab.SetSnapshot(formatSnapshot().Inputs, formatSnapshot().Formats, formatSnapshot().Active)
	q.expect() // lazy interval load from rebuild

	vm := tab.VM()

	if vm == nil || vm.Dirty() {
		t.Fatal("fresh tab must be clean")
	}

	// Stage another size via the picker.
	tab.sizeSel.SetSelected("640×480")
	q.expect() // lazy interval reload

	if !vm.Dirty() || vm.Staged().Width != 640 || vm.Staged().Height != 480 {
		t.Fatalf("staged = %+v", vm.Staged())
	}

	// Stage an exact interval via the entry; 0/0 is rejected.
	tab.fpsEntry.OnSubmitted("1001/60000")

	if got := vm.Staged().Interval; got == nil || *got != (domain.FrameInterval{Num: 1001, Den: 60000}) {
		t.Fatalf("interval = %+v", got)
	}

	tab.fpsEntry.OnSubmitted("0/0")

	if got := vm.Staged().Interval; got == nil || got.Num != 1001 {
		t.Fatalf("invalid interval overwrote staged value: %+v", got)
	}

	// Apply sends the staged config through the engine path; the completion
	// dispatch parks until the test drains it.
	tab.applyBtn.OnTapped()

	cfg := <-applied
	q.expect()

	if cfg.Width != 640 || cfg.Height != 480 {
		t.Fatalf("applied = %+v", cfg)
	}

	if tab.status.Text != "Applied." {
		t.Fatalf("status = %q, want applied", tab.status.Text)
	}

	// Revert returns to the active state.
	tab.onRevert()
	q.expect()

	if vm.Dirty() {
		t.Fatalf("revert left staged state: %+v", vm.Staged())
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}

	return false
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// dispatchQueue parks cross-goroutine UI dispatches until the test drains
// them on the test goroutine — the Fyne test driver runs fns inline on the
// caller's goroutine, which would race widget state.
type dispatchQueue chan func()

func (q dispatchQueue) dispatch(fn func()) { q <- fn }

// expect blocks for one queued dispatch and runs it.
func (q dispatchQueue) expect() {
	(<-q)()
}
