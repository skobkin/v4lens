package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"git.skobk.in/skobkin/v4lens/internal/config"
	"git.skobk.in/skobkin/v4lens/internal/device"
	"git.skobk.in/skobkin/v4lens/internal/domain"
	"git.skobk.in/skobkin/v4lens/internal/profile"
)

// DefaultScanInterval is the sysfs hotplug poll period for the MVP.
const DefaultScanInterval = 2 * time.Second

// DefaultNotifyCooldown is the per-device minimum spacing between repeated
// apply-issue publications, so persistent failures cannot spam.
const DefaultNotifyCooldown = time.Minute

// Options builds the App.
type Options struct {
	Config   *config.Store
	Profiles *profile.Store
	Log      *slog.Logger

	// OpenBackend creates the device handle for a discovered identity.
	// Production opens the capture endpoint node; tests inject fakes.
	OpenBackend func(identity domain.DeviceIdentity) (device.Handle, error)

	// Roots optionally enables the sysfs hotplug scanner (zero Roots or
	// nil Classify leaves hotplug off — tests drive lifecycle directly).
	Roots        device.Roots
	ScanInterval time.Duration

	// StreamOwner coordinates applies with our own preview (nil until the
	// preview integration exists).
	StreamOwner profile.StreamOwner
}

// App is the application orchestrator.
type App struct {
	opts    Options
	log     *slog.Logger
	bus     *Bus
	prefs   config.Prefs
	applier *profile.Applier
	scanner *device.Scanner

	notifyCooldown time.Duration

	mu      sync.Mutex
	devices map[string]*devEntry
	notify  map[string]time.Time
	stopCh  chan struct{}
	stopOne sync.Once
	wg      sync.WaitGroup
}

type devEntry struct {
	identity  domain.DeviceIdentity
	sess      *device.Session
	connected bool
}

// New builds the App, loading preferences (falling back to defaults with a
// warning when the config file is malformed).
func New(opts Options) (*App, error) {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}

	if opts.Config == nil {
		return nil, errors.New("app: config store is required")
	}

	if opts.Profiles == nil {
		return nil, errors.New("app: profile store is required")
	}

	if opts.OpenBackend == nil {
		return nil, errors.New("app: backend opener is required")
	}

	prefs, err := opts.Config.Load()
	if err != nil {
		opts.Log.Warn("config malformed, using defaults", "err", err)
		prefs = config.Default()
	}

	if opts.ScanInterval <= 0 {
		opts.ScanInterval = DefaultScanInterval
	}

	return &App{
		opts:           opts,
		log:            opts.Log,
		bus:            NewBus(),
		prefs:          prefs,
		applier:        profile.NewApplier(opts.Log),
		devices:        map[string]*devEntry{},
		notify:         map[string]time.Time{},
		notifyCooldown: DefaultNotifyCooldown,
		stopCh:         make(chan struct{}),
	}, nil
}

// Bus exposes the typed event bus.
func (a *App) Bus() *Bus {
	return a.bus
}

// Prefs returns the current preferences snapshot.
func (a *App) Prefs() config.Prefs {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.prefs
}

// Start launches hotplug scanning (when Roots are configured). Initial
// discovery devices receive apply-on-start; reconnects receive exactly one
// default-profile application per reappearance.
func (a *App) Start() {
	if a.opts.Roots.Classify == nil || a.opts.Roots.SysClassV4L == "" {
		a.log.Debug("hotplug scanning disabled (no roots configured)")

		return
	}

	a.scanner = device.NewScanner(a.opts.Roots, a.log, a.opts.ScanInterval)
	events := a.scanner.Events()
	a.scanner.Start()

	a.wg.Add(1)

	go func() {
		defer a.wg.Done()

		for {
			select {
			case ev, ok := <-events:
				if !ok {
					return
				}

				a.onScanEvent(ev)
			case <-a.stopCh:

				return
			}
		}
	}()
}

// Stop shuts the app down: scanner, all device sessions and handles.
func (a *App) Stop() {
	a.stopOne.Do(func() {
		close(a.stopCh)
	})

	a.wg.Wait()

	if a.scanner != nil {
		a.scanner.Stop()
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for key, e := range a.devices {
		if e.sess != nil {
			_ = e.sess.Handle().Close()
			e.sess.Stop()
		}

		e.sess = nil
		e.connected = false

		a.log.Debug("device session closed", "device", key)
	}
}

func (a *App) onScanEvent(ev device.DeviceEvent) {
	switch ev.Kind {
	case device.DeviceAddedInitial:
		if a.deviceAppeared(ev.Identity) && a.Prefs().ApplyOnStart {
			a.applyDefaultProfile(ev.Identity.Key, "apply-on-start")
		}

	case device.DeviceReconnected:
		// Genuine reconnect: exactly one identity-matched default-profile
		// attempt, regardless of the apply-on-start preference.
		if a.deviceAppeared(ev.Identity) {
			a.applyDefaultProfile(ev.Identity.Key, "reconnect")
		}

	case device.DeviceRemoved:
		a.deviceDisappeared(ev.Identity.Key)
	}
}

// deviceAppeared opens a session for a present device. It is a no-op when
// the device already has an open session. Returns false when the device
// could not be opened.
func (a *App) deviceAppeared(identity domain.DeviceIdentity) bool {
	key := identity.Key

	a.mu.Lock()

	if e, ok := a.devices[key]; ok && e.sess != nil {
		a.mu.Unlock()

		return true // already up
	}

	a.mu.Unlock()

	ep := identity.CaptureEndpoint()
	if ep == nil {
		a.log.Warn("device has no capture endpoint, ignoring", "device", key)

		return false
	}

	h, err := a.opts.OpenBackend(identity)
	if err != nil {
		a.log.Warn("backend open failed", "device", key, "node", ep.Node, "err", err)

		return false
	}

	e := &devEntry{identity: identity, sess: device.NewSession(h, a.log), connected: true}

	a.mu.Lock()
	a.devices[key] = e
	a.mu.Unlock()

	a.log.Info("device connected", "device", key, "node", h.Node())

	a.bus.Publish(DevicesChanged{Devices: a.Devices()})
	a.refreshSnapshots(key)

	return true
}

func (a *App) deviceDisappeared(key string) {
	a.mu.Lock()

	e, ok := a.devices[key]

	if !ok || e.sess == nil {
		a.mu.Unlock()

		return
	}

	_ = e.sess.Handle().Close()
	e.sess.Stop()
	e.sess = nil
	e.connected = false
	a.mu.Unlock()

	a.log.Info("device disconnected", "device", key)

	a.bus.Publish(DevicesChanged{Devices: a.Devices()})
}

// Devices returns the current device views (identity + state).
func (a *App) Devices() []DeviceView {
	a.mu.Lock()
	defer a.mu.Unlock()

	out := make([]DeviceView, 0, len(a.devices))

	for _, e := range a.devices {
		out = append(out, DeviceView{Identity: e.identity, Connected: e.connected})
	}

	return out
}

// refreshSnapshots publishes the control and format snapshots for one
// device through the session (short ops).
func (a *App) refreshSnapshots(key string) {
	a.mu.Lock()
	e := a.devices[key]
	a.mu.Unlock()

	if e == nil || e.sess == nil {
		return
	}

	ctx := context.Background()

	var descs []domain.ControlDescriptor
	var values map[uint32]domain.ControlValue
	var inputs []domain.InputInfo
	var formats []domain.FormatDesc
	var active domain.CaptureConfig

	err := e.sess.Do(ctx, func(h device.Handle) error {
		ds, err := h.EnumerateControls(ctx)
		if err != nil {
			return err
		}

		descs = ds
		values = map[uint32]domain.ControlValue{}

		ids := make([]uint32, 0, len(ds))

		for _, d := range ds {
			if d.Editable() {
				ids = append(ids, d.ID)
			}
		}

		vs, err := h.ReadControls(ctx, ids)
		if err != nil {
			return err
		}

		for _, v := range vs {
			values[v.ID] = v
		}

		if inputs, err = h.ListInputs(ctx); err != nil {
			return err
		}

		if formats, err = h.FormatSizes(ctx); err != nil {
			return err
		}

		active, err = h.ActiveCaptureConfig(ctx)

		return err
	})
	if err != nil {
		a.log.Warn("snapshot failed", "device", key, "err", err)

		return
	}

	a.bus.Publish(ControlsSnapshot{Key: key, Descriptors: descs, Values: values})
	a.bus.Publish(FormatSnapshot{Key: key, Inputs: inputs, Formats: formats, Active: active})
}

// ApplyProfile applies one stored profile to its device in an exclusive
// mutation and publishes ApplyFinished.
func (a *App) ApplyProfile(key, name string) (profile.ApplyResult, error) {
	p, err := a.opts.Profiles.Load(key, name)
	if err != nil {
		return profile.ApplyResult{}, err
	}

	a.mu.Lock()
	e := a.devices[key]
	a.mu.Unlock()

	if e == nil || e.sess == nil {
		return profile.ApplyResult{}, fmt.Errorf("%w: %s", domain.ErrDisconnected, key)
	}

	res := a.applier.Apply(context.Background(), e.sess, key, p, a.opts.StreamOwner)

	a.bus.Publish(ApplyFinished{Key: key, Result: res})
	a.refreshSnapshots(key)

	return res, nil
}

// applyDefaultProfile runs the one-shot default-profile application used by
// both startup (apply-on-start) and reconnects. Failures surface through
// ApplyFinished gated by the notification preference and the per-key
// cooldown.
func (a *App) applyDefaultProfile(key, cause string) {
	name, ok := a.Prefs().Defaults[key]
	if !ok || name == "" {
		a.log.Info("no default profile, skipping", "device", key, "cause", cause)

		return
	}

	a.mu.Lock()
	e := a.devices[key]
	a.mu.Unlock()

	if e == nil || e.sess == nil {
		return
	}

	p, err := a.opts.Profiles.Load(key, name)
	if err != nil {
		a.log.Warn("default profile unreadable", "device", key, "profile", name, "err", err)

		return
	}

	res := a.applier.Apply(context.Background(), e.sess, key, p, a.opts.StreamOwner)

	a.log.Log(context.Background(), levelFor(res), "default profile applied",
		"device", key, "profile", name, "cause", cause, "status", res.Status.String())

	if res.Status == profile.StatusFull {
		a.bus.Publish(ApplyFinished{Key: key, Result: res})
		a.refreshSnapshots(key)

		return
	}

	// Issues: publish through the notification path only when enabled and
	// outside the cooldown; otherwise log only (no spam on flap loops).
	if !a.Prefs().NotifyApplyIssues || !a.notifyAllowed(key) {
		return
	}

	a.bus.Publish(ApplyFinished{Key: key, Result: res})
	a.refreshSnapshots(key)
}

func (a *App) notifyAllowed(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()

	if last, ok := a.notify[key]; ok && now.Sub(last) < a.notifyCooldown {
		return false
	}

	a.notify[key] = now

	return true
}

// SaveProfile captures the device's EFFECTIVE state into a profile
// (UI_UX_SPEC: Save stores read-back state, never unconfirmed intent).
// Only the profile-managed control set is persisted.
func (a *App) SaveProfile(key, name string) error {
	a.mu.Lock()
	e := a.devices[key]
	a.mu.Unlock()

	if e == nil || e.sess == nil {
		return fmt.Errorf("%w: %s", domain.ErrDisconnected, key)
	}

	ctx := context.Background()

	var (
		active   domain.CaptureConfig
		settings []profile.ControlSetting
	)

	err := e.sess.Do(ctx, func(h device.Handle) error {
		cfg, err := h.ActiveCaptureConfig(ctx)
		if err != nil {
			return err
		}

		active = cfg

		descs, err := h.EnumerateControls(ctx)
		if err != nil {
			return err
		}

		var ids []uint32
		keep := map[uint32]string{}

		for _, d := range descs {
			if d.Persistable() {
				ids = append(ids, d.ID)
				keep[d.ID] = d.Name
			}
		}

		values, err := h.ReadControls(ctx, ids)
		if err != nil {
			return err
		}

		settings = make([]profile.ControlSetting, 0, len(values))

		for _, v := range values {
			settings = append(settings, profile.ControlSetting{ID: v.ID, Name: keep[v.ID], Value: v})
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("read effective state: %w", err)
	}

	// The interval rides along only when the device actually reports one
	// (no timeperframe capability -> ActiveCaptureConfig returns nil).
	capture := active

	p := &profile.Profile{
		Name:     name,
		Match:    matchOf(e.identity),
		Capture:  &capture,
		Controls: settings,
	}

	if err := a.opts.Profiles.Save(key, p); err != nil {
		return err
	}

	a.log.Info("profile saved", "device", key, "profile", name)

	return nil
}

// Profiles lists stored profile names for one device.
func (a *App) Profiles(key string) ([]string, error) {
	return a.opts.Profiles.List(key)
}

// DefaultProfile returns the device's default profile name ("" = none).
func (a *App) DefaultProfile(key string) string {
	return a.Prefs().Defaults[key]
}

// SetDefaultProfile sets (or clears with "") the device default and
// persists the preference.
func (a *App) SetDefaultProfile(key, name string) error {
	a.mu.Lock()

	if a.prefs.Defaults == nil {
		a.prefs.Defaults = map[string]string{}
	}

	if name == "" {
		delete(a.prefs.Defaults, key)
	} else {
		a.prefs.Defaults[key] = name
	}

	prefs := a.prefs
	a.mu.Unlock()

	if err := a.opts.Config.Save(prefs); err != nil {
		return err
	}

	a.bus.Publish(PrefsChanged{Prefs: prefs})

	return nil
}

// UpdatePrefs replaces the preferences and persists them.
func (a *App) UpdatePrefs(prefs config.Prefs) error {
	a.mu.Lock()
	a.prefs = prefs
	a.mu.Unlock()

	if err := a.opts.Config.Save(prefs); err != nil {
		return err
	}

	a.bus.Publish(PrefsChanged{Prefs: prefs})

	return nil
}

func matchOf(id domain.DeviceIdentity) profile.Match {
	return profile.Match{
		Key:         id.Key,
		DisplayName: id.DisplayName,
		Serial:      id.Serial,
		USBVendor:   id.USBVendor,
		USBProduct:  id.USBProduct,
		ByID:        id.ByID,
		ByPath:      id.ByPath,
		Fingerprint: id.Fingerprint,
	}
}

func levelFor(res profile.ApplyResult) slog.Level {
	switch res.Status {
	case profile.StatusFull:

		return slog.LevelInfo
	case profile.StatusPartial:

		return slog.LevelWarn
	default:

		return slog.LevelError
	}
}
