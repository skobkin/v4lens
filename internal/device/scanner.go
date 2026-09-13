package device

import (
	"log/slog"
	"sync"
	"time"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// DeviceEventKind distinguishes how a device appeared in the scanner.
type DeviceEventKind int8

const (
	// DeviceAddedInitial marks a device present on the FIRST scan — initial
	// discovery, never a reconnect (it does not receive a reconnect
	// application on top of startup's Apply-profile-on-start).
	DeviceAddedInitial DeviceEventKind = iota

	// DeviceReconnected marks a device previously observed connected, later
	// disconnected, and present again in the current process — the genuine
	// reconnect case (one-shot default-profile application).
	DeviceReconnected

	// DeviceRemoved marks a previously present device as gone.
	DeviceRemoved
)

// DeviceEvent announces one identity transition.
type DeviceEvent struct {
	Kind     DeviceEventKind
	Identity domain.DeviceIdentity
}

// Scanner polls sysfs-first discovery on a fixed interval, diffs the result
// against the known set and emits lifecycle events. Reconnect semantics
// (ARCHITECTURE + plan): only connected→disconnected→connected inside the
// current process is a reconnect; first-scan presence is initial discovery.
type Scanner struct {
	roots    Roots
	interval time.Duration
	log      *slog.Logger

	events chan DeviceEvent
	trig   chan struct{}
	stop   chan struct{}

	mu       sync.Mutex
	known    map[string]bool
	everSeen map[string]bool

	wg sync.WaitGroup
}

// NewScanner builds a scanner; interval <= 0 means the documented 2s poll.
func NewScanner(roots Roots, log *slog.Logger, interval time.Duration) *Scanner {
	if interval <= 0 {
		interval = 2 * time.Second
	}

	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Scanner{
		roots:    roots,
		interval: interval,
		log:      log,
		events:   make(chan DeviceEvent, 32),
		trig:     make(chan struct{}, 1),
		stop:     make(chan struct{}),
		known:    map[string]bool{},
		everSeen: map[string]bool{},
	}
}

// Events returns the lifecycle event stream.
func (sc *Scanner) Events() <-chan DeviceEvent {
	return sc.events
}

// Start begins polling; the first scan runs immediately.
func (sc *Scanner) Start() {
	sc.wg.Add(1)

	go sc.loop()
}

// Stop ends polling and waits for the goroutine.
func (sc *Scanner) Stop() {
	close(sc.stop)
	sc.wg.Wait()
}

// TriggerScan requests an out-of-band rescan (window focus, tray open).
// Non-blocking; coalesces with pending triggers.
func (sc *Scanner) TriggerScan() {
	select {
	case sc.trig <- struct{}{}:
	default:
	}
}

func (sc *Scanner) loop() {
	defer sc.wg.Done()

	sc.scan()

	ticker := time.NewTicker(sc.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			sc.scan()
		case <-sc.trig:
			sc.scan()
		case <-sc.stop:

			return
		}
	}
}

func (sc *Scanner) scan() {
	identities, err := Discover(sc.roots, sc.log)
	if err != nil {
		sc.log.Warn("device discovery failed", "err", err)

		return
	}

	found := map[string]domain.DeviceIdentity{}

	for _, id := range identities {
		found[id.Key] = id
	}

	sc.mu.Lock()
	defer sc.mu.Unlock()

	// Newly present devices.
	for key, id := range found {
		if sc.known[key] {
			continue
		}

		kind := DeviceAddedInitial

		if sc.everSeen[key] {
			kind = DeviceReconnected
			sc.log.Info("device reconnected", "key", key, "name", id.DisplayName)
		} else {
			sc.log.Info("device discovered", "key", key, "name", id.DisplayName)
		}

		sc.known[key] = true
		sc.everSeen[key] = true

		sc.emit(DeviceEvent{Kind: kind, Identity: id})
	}

	// Vanished devices: unmark known but KEEP everSeen so a later
	// reappearance is classified as a reconnect.
	for key := range sc.known {
		if _, ok := found[key]; ok {
			continue
		}

		delete(sc.known, key)
		sc.log.Info("device disconnected", "key", key)

		sc.emit(DeviceEvent{Kind: DeviceRemoved, Identity: domain.DeviceIdentity{Key: key}})
	}
}

func (sc *Scanner) emit(ev DeviceEvent) {
	select {
	case sc.events <- ev:
	default:
		sc.log.Warn("device event dropped (consumer too slow)", "kind", ev.Kind, "key", ev.Identity.Key)
	}
}
