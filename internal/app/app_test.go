package app

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"git.skobk.in/skobkin/v4lens/internal/config"
	"git.skobk.in/skobkin/v4lens/internal/device"
	"git.skobk.in/skobkin/v4lens/internal/device/fakebackend"
	"git.skobk.in/skobkin/v4lens/internal/domain"
	"git.skobk.in/skobkin/v4lens/internal/profile"
)

// harness wires an App over fake backends with temp stores.
type harness struct {
	t      *testing.T
	app    *App
	cfg    *config.Store
	stores *profile.Store
	events <-chan Event

	backends map[string]*fakebackend.Backend
}

const testKey = "synthetic-cam-4k-0001abcd"

func identityFor(key string) domain.DeviceIdentity { //nolint:unparam // parameterized for future multi-device tests
	return domain.DeviceIdentity{
		Key:         key,
		DisplayName: "Synthetic Cam 4K",
		Serial:      "TEST-PW513-0001",
		USBVendor:   "1a86",
		USBProduct:  "7523",
		Endpoints: []domain.EndpointInfo{
			{Node: "/dev/video0", Index: 0, Role: domain.RoleCapture, CaptureCaps: domain.CaptureCaps{SinglePlanar: true}},
		},
	}
}

func fakeOptions() fakebackend.Options {
	return fakebackend.Options{
		Node: "/dev/video0",
		Card: "Synthetic Cam 4K",
		Controls: []fakebackend.ControlSpec{
			{ID: 0x00980900, Name: "Brightness", Type: domain.TypeInteger, Min: -64, Max: 64, Default: 0, Value: 0},
			{ID: 0x00980901, Name: "Contrast", Type: domain.TypeInteger, Min: 0, Max: 255, Default: 128, Value: 128},
			{ID: 0x00980913, Name: "Read Only Item", Type: domain.TypeInteger, Min: 0, Max: 10, Value: 3, Flags: domain.FlagReadOnly},
			{ID: 0x00980914, Name: "Press Me", Type: domain.TypeButton, Flags: domain.FlagWriteOnly},
			{ID: 0x00980915, Name: "Write Only Item", Type: domain.TypeInteger, Min: 0, Max: 10, Value: 5, Flags: domain.FlagWriteOnly},
			{ID: 0x00980916, Name: "Volatile Item", Type: domain.TypeInteger, Min: 0, Max: 10, Value: 5, Flags: domain.FlagVolatile},
			{ID: 0x00980998, Name: "Fragile", Type: domain.TypeInteger, Min: 0, Max: 10, Default: 5, Value: 5, WriteErr: errors.New("injected failure")},
			{ID: 0x01990920, Name: "Compound Blob", Type: domain.TypeU8, Elems: 4, Value: 0},
		},
		Formats: []fakebackend.FormatSpec{{
			FourCC: "UYVY",
			Sizes:  []domain.FrameSize{{Type: domain.FrameSizeDiscrete, Width: 1280, Height: 720}, {Type: domain.FrameSizeDiscrete, Width: 640, Height: 480}},
			Intervals: map[string][]domain.IntervalCap{
				"1280x720": {{Type: domain.IntervalDiscrete, Min: domain.FrameInterval{Num: 1, Den: 30}}},
			},
		}},
		Inputs:       []domain.InputInfo{{Index: 0, Name: "Camera"}},
		Active:       domain.CaptureConfig{FourCC: "UYVY", Width: 1280, Height: 720},
		TimePerFrame: true,
	}
}

func newHarness(t *testing.T, seed map[string]string) *harness {
	t.Helper()

	dir := t.TempDir()

	cfg := config.NewStore(filepath.Join(dir, "config.yaml"))
	prefs := config.Default()
	prefs.ApplyOnStart = true

	if prefs.Defaults == nil {
		prefs.Defaults = map[string]string{}
	}

	for k, v := range seed {
		prefs.Defaults[k] = v
	}

	if err := cfg.Save(prefs); err != nil {
		t.Fatal(err)
	}

	stores := profile.NewStore(filepath.Join(dir, "profiles"), nil)
	h := &harness{
		t:        t,
		cfg:      cfg,
		stores:   stores,
		backends: map[string]*fakebackend.Backend{},
	}

	h.app = h.mustNew()

	return h
}

func (h *harness) mustNew() *App {
	t := h.t

	backends := h.backends

	app, err := New(Options{
		Config:   h.cfg,
		Profiles: h.stores,
		OpenBackend: func(id domain.DeviceIdentity) (device.Handle, error) {
			b := fakebackend.New(fakeOptions())

			backends[id.Key] = b

			return b, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if h.events == nil {
		h.events, _ = app.Bus().Subscribe()
	}

	return app
}

func (h *harness) seedProfile(name string, mutate func(*profile.Profile)) {
	h.t.Helper()

	p := &profile.Profile{
		Name:  name,
		Match: profile.Match{Key: testKey},
		Capture: &domain.CaptureConfig{
			FourCC: "UYVY", Width: 640, Height: 480,
			Interval: &domain.FrameInterval{Num: 1, Den: 30},
		},
		Controls: []profile.ControlSetting{
			{ID: 0x00980900, Name: "Brightness", Value: domain.NewIntValue(0x00980900, domain.TypeInteger, 42)},
		},
	}

	if mutate != nil {
		mutate(p)
	}

	if err := h.stores.Save(testKey, p); err != nil {
		h.t.Fatal(err)
	}
}

// collect drains n events within a deadline.
func (h *harness) collect(n int, d time.Duration) []Event {
	h.t.Helper()

	var got []Event

	deadline := time.After(d)

	for len(got) < n {
		select {
		case ev := <-h.events:
			got = append(got, ev)
		case <-deadline:

			return got
		}
	}

	return got
}

// drainExisting returns every event already queued. Publishing on the bus
// is synchronous, so after an app call returns its events are all queued
// and no polling windows are needed.
func drainExisting(events <-chan Event) []Event {
	var out []Event

	for {
		select {
		case ev := <-events:
			out = append(out, ev)
		default:

			return out
		}
	}
}

func countKind[T Event](events []Event) int {
	n := 0

	for _, ev := range events {
		if _, ok := ev.(T); ok {
			n++
		}
	}

	return n
}

func TestDeviceLifecyclePublishesEvents(t *testing.T) {
	h := newHarness(t, nil)
	defer h.app.Stop()

	if !h.app.deviceAppeared(identityFor(testKey)) {
		t.Fatal("device did not appear")
	}

	events := h.collect(3, time.Second) // DevicesChanged + Controls + Format

	var sawDevices, sawControls, sawFormat bool

	for _, ev := range events {
		switch e := ev.(type) {
		case DevicesChanged:
			sawDevices = true

			if len(e.Devices) != 1 || !e.Devices[0].Connected {
				t.Fatalf("device view wrong: %+v", e.Devices)
			}
		case ControlsSnapshot:
			sawControls = true

			if e.Key != testKey || len(e.Descriptors) == 0 {
				t.Fatalf("controls snapshot wrong: key=%q descs=%d", e.Key, len(e.Descriptors))
			}
		case FormatSnapshot:
			sawFormat = true

			if e.Active.FourCC != "UYVY" || e.Active.Width != 1280 {
				t.Fatalf("format snapshot wrong: %+v", e.Active)
			}
		}
	}

	if !sawDevices || !sawControls || !sawFormat {
		t.Fatalf("missing events: devices=%v controls=%v format=%v (%v)", sawDevices, sawControls, sawFormat, events)
	}

	// Removal flips state without dropping the identity (profiles survive).
	h.app.deviceDisappeared(testKey)

	events = h.collect(1, time.Second)

	dc, ok := events[0].(DevicesChanged)
	if !ok || dc.Devices[0].Connected {
		t.Fatalf("removal event wrong: %+v", events)
	}
}

func TestApplyOnStartOnlyInitial(t *testing.T) {
	h := newHarness(t, map[string]string{testKey: "Defaults"})
	defer h.app.Stop()

	h.seedProfile("Defaults", nil)

	h.app.Start() // scanner disabled in tests (no Roots) — drive events manually
	h.app.onScanEvent(device.DeviceEvent{Kind: device.DeviceAddedInitial, Identity: identityFor(testKey)})

	b := h.backends[testKey]

	spec, ok := b.FindControl(0x00980900)
	if !ok || spec.Value != 42 {
		t.Fatalf("apply-on-start did not reach the fake device: %+v", spec)
	}

	events := drainExisting(h.events)

	if countKind[ApplyFinished](events) != 1 {
		t.Fatalf("expected exactly one ApplyFinished, got %+v", events)
	}
}

func TestReconnectAppliesDefaultExactlyOnce(t *testing.T) {
	h := newHarness(t, map[string]string{testKey: "Defaults"})
	defer h.app.Stop()

	h.seedProfile("Defaults", nil)

	// Initial presence: apply-on-start fires.
	h.app.onScanEvent(device.DeviceEvent{Kind: device.DeviceAddedInitial, Identity: identityFor(testKey)})

	if b := h.backends[testKey]; b == nil {
		t.Fatal("backend not opened")
	}

	drainExisting(h.events)

	// Device vanishes and reappears: exactly ONE reconnect apply.
	h.app.onScanEvent(device.DeviceEvent{Kind: device.DeviceRemoved, Identity: identityFor(testKey)})
	drainExisting(h.events)

	h.app.onScanEvent(device.DeviceEvent{Kind: device.DeviceReconnected, Identity: identityFor(testKey)})

	b := h.backends[testKey]

	spec, ok := b.FindControl(0x00980900)
	if !ok || spec.Value != 42 {
		t.Fatalf("reconnect apply did not reach the fake device: %+v", spec)
	}

	events := drainExisting(h.events)

	if n := countKind[ApplyFinished](events); n != 1 {
		t.Fatalf("reconnect ApplyFinished count = %d, want exactly 1 (%+v)", n, events)
	}
}

func TestNoDefaultProfileSkipsApply(t *testing.T) {
	h := newHarness(t, nil) // no defaults seeded
	defer h.app.Stop()

	h.app.Start()
	h.app.deviceAppeared(identityFor(testKey))

	if evs := h.collect(3, 300*time.Millisecond); len(evs) >= 1 {
		for _, ev := range evs {
			if _, ok := ev.(ApplyFinished); ok {
				t.Fatal("apply ran without a default profile")
			}
		}
	}
}

func TestSaveProfileKeepsManagedSetOnly(t *testing.T) {
	h := newHarness(t, nil)
	defer h.app.Stop()

	h.app.deviceAppeared(identityFor(testKey))
	h.collect(3, time.Second)

	if err := h.app.SaveProfile(testKey, "Effective"); err != nil {
		t.Fatalf("save: %v", err)
	}

	p, err := h.stores.Load(testKey, "Effective")
	if err != nil {
		t.Fatal(err)
	}

	saved := map[uint32]bool{}
	for _, s := range p.Controls {
		saved[s.ID] = true
	}

	// Writable state controls present with their effective values.
	if !saved[0x00980900] || !saved[0x00980901] {
		t.Fatalf("writable controls missing from profile: %v", saved)
	}

	// READ_ONLY / BUTTON / WRITE_ONLY / VOLATILE / compound excluded.
	for _, id := range []uint32{0x00980913, 0x00980914, 0x00980915, 0x00980916, 0x01990920} {
		if saved[id] {
			t.Fatalf("non-managed control 0x%08x persisted", id)
		}
	}

	// Capture saved with interval (device supports timeperframe).
	if p.Capture == nil || p.Capture.Interval == nil || !p.Capture.Interval.Valid() {
		t.Fatalf("capture/interval wrong: %+v", p.Capture)
	}

	if p.Match.Key != testKey || p.Match.Serial != "TEST-PW513-0001" {
		t.Fatalf("match metadata wrong: %+v", p.Match)
	}
}

func TestSaveProfileOmitsIntervalWithoutCapability(t *testing.T) {
	dir := t.TempDir()

	cfg := config.NewStore(filepath.Join(dir, "config.yaml"))

	if err := cfg.Save(config.Default()); err != nil {
		t.Fatal(err)
	}

	stores := profile.NewStore(filepath.Join(dir, "profiles"), nil)

	var b *fakebackend.Backend

	app, err := New(Options{
		Config:   cfg,
		Profiles: stores,
		OpenBackend: func(domain.DeviceIdentity) (device.Handle, error) {
			o := fakeOptions()
			o.TimePerFrame = false // capability off

			b = fakebackend.New(o)

			return b, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	defer app.Stop()

	events, _ := app.Bus().Subscribe()

	app.deviceAppeared(identityFor(testKey))
	drainAll(events, 300*time.Millisecond)

	if err := app.SaveProfile(testKey, "NoTPF"); err != nil {
		t.Fatalf("save: %v", err)
	}

	p, err := stores.Load(testKey, "NoTPF")
	if err != nil {
		t.Fatal(err)
	}

	if p.Capture == nil {
		t.Fatal("capture config missing")
	}

	if p.Capture.Interval != nil {
		t.Fatalf("interval saved despite missing timeperframe: %+v", p.Capture.Interval)
	}
}

func TestApplyProfileManualAndResultEvent(t *testing.T) {
	h := newHarness(t, nil)
	defer h.app.Stop()

	h.seedProfile("Manual", nil)
	h.app.deviceAppeared(identityFor(testKey))
	h.collect(3, time.Second)

	res, err := h.app.ApplyProfile(testKey, "Manual")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	if res.Status != profile.StatusFull {
		t.Fatalf("status = %v (%+v)", res.Status, res)
	}

	b := h.backends[testKey]

	if spec, _ := b.FindControl(0x00980900); spec.Value != 42 {
		t.Fatalf("brightness not applied: %d", spec.Value)
	}

	if n := countKind[ApplyFinished](drainExisting(h.events)); n != 1 {
		t.Fatalf("ApplyFinished published %d times, want 1", n)
	}
}

func TestApplyIssuesCooldown(t *testing.T) {
	h := newHarness(t, map[string]string{testKey: "Broken"})
	defer h.app.Stop()

	// Profile with an always-failing control (exists on the fake with an
	// injected write error): every apply reports issues.
	h.seedProfile("Broken", func(p *profile.Profile) {
		p.Controls = append(p.Controls, profile.ControlSetting{
			ID:    0x00980998,
			Name:  "Fragile",
			Value: domain.NewIntValue(0x00980998, domain.TypeInteger, 1),
		})
	})

	prefs := h.app.Prefs()
	prefs.NotifyApplyIssues = true

	if err := h.app.UpdatePrefs(prefs); err != nil {
		t.Fatal(err)
	}

	h.app.notifyCooldown = 50 * time.Millisecond

	const (
		initialEv = iota
		removedEv
		reconnectedEv
	)

	scan := func(kind int) {
		kinds := []device.DeviceEventKind{device.DeviceAddedInitial, device.DeviceRemoved, device.DeviceReconnected}
		h.app.onScanEvent(device.DeviceEvent{Kind: kinds[kind], Identity: identityFor(testKey)})
	}

	// Apply #1 (issues): published — the first notification is allowed.
	scan(initialEv)

	if n := countKind[ApplyFinished](drainExisting(h.events)); n != 1 {
		t.Fatalf("first issue apply published %d events, want 1", n)
	}

	// Apply #2 immediately after: suppressed by the cooldown.
	scan(removedEv)
	scan(reconnectedEv)

	if n := countKind[ApplyFinished](drainExisting(h.events)); n != 0 {
		t.Fatalf("second issue apply leaked past cooldown: %d events", n)
	}

	// Apply #3 after the cooldown expires: published again.
	time.Sleep(60 * time.Millisecond)

	scan(removedEv)
	scan(reconnectedEv)

	if n := countKind[ApplyFinished](drainExisting(h.events)); n != 1 {
		t.Fatalf("third issue apply published %d events, want 1", n)
	}
}

func TestPrefsEventsAndDefaults(t *testing.T) {
	h := newHarness(t, nil)
	defer h.app.Stop()

	if h.app.DefaultProfile(testKey) != "" {
		t.Fatal("no default expected")
	}

	if err := h.app.SetDefaultProfile(testKey, "Video Calls"); err != nil {
		t.Fatalf("set default: %v", err)
	}

	if h.app.DefaultProfile(testKey) != "Video Calls" {
		t.Fatalf("default = %q", h.app.DefaultProfile(testKey))
	}

	// Persisted.
	prefs, err := h.cfg.Load()
	if err != nil {
		t.Fatal(err)
	}

	if prefs.Defaults[testKey] != "Video Calls" {
		t.Fatalf("default not persisted: %+v", prefs.Defaults)
	}

	if err := h.app.SetDefaultProfile(testKey, ""); err != nil {
		t.Fatal(err)
	}

	if h.app.DefaultProfile(testKey) != "" {
		t.Fatal("clearing default failed")
	}

	// UpdatePrefs publishes PrefsChanged (drain the events SetDefaultProfile
	// already published so the assertion is unambiguous).
	drainExisting(h.events)

	prefs = h.app.Prefs()
	prefs.LogLevel = "debug"

	if err := h.app.UpdatePrefs(prefs); err != nil {
		t.Fatal(err)
	}

	if n := countKind[PrefsChanged](drainExisting(h.events)); n != 1 {
		t.Fatalf("PrefsChanged published %d times, want 1", n)
	}
}

func TestOpenBackendFailureIsReported(t *testing.T) {
	dir := t.TempDir()

	cfg := config.NewStore(filepath.Join(dir, "config.yaml"))

	if err := cfg.Save(config.Default()); err != nil {
		t.Fatal(err)
	}

	app, err := New(Options{
		Config:   cfg,
		Profiles: profile.NewStore(filepath.Join(dir, "profiles"), nil),
		OpenBackend: func(domain.DeviceIdentity) (device.Handle, error) {
			return nil, errors.New("no such node")
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	defer app.Stop()

	if app.deviceAppeared(identityFor(testKey)) {
		t.Fatal("backend failure must not register the device")
	}

	if views := app.Devices(); len(views) != 0 {
		t.Fatalf("failed device leaked into views: %+v", views)
	}
}

func drainAll(events <-chan Event, d time.Duration) {
	deadline := time.After(d)

	for {
		select {
		case <-events:
		case <-deadline:

			return
		}
	}
}

func TestBusDropOldestNeverBlocks(t *testing.T) {
	bus := NewBus()

	ch, unsub := bus.Subscribe()
	defer unsub()

	for i := range 200 {
		bus.Publish(DevicesChanged{}) // full: 200 >> 64 buffer

		_ = i
	}

	// Channel holds exactly the newest 64 events.
	if len(ch) != 64 {
		t.Fatalf("buffer = %d, want 64", len(ch))
	}

	ev := <-ch

	dc, ok := ev.(DevicesChanged)
	if !ok {
		t.Fatalf("wrong event type: %T", ev)
	}

	_ = dc
}
