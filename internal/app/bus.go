// Package app provides V4Lens orchestration: the typed event bus the UI
// subscribes to, per-device session lifecycle over discovery events,
// apply-on-start and one-shot reconnect default-profile application, and
// profile persistence driven by effective device state.
package app

import (
	"sync"

	"git.skobk.in/skobkin/v4lens/internal/config"
	"git.skobk.in/skobkin/v4lens/internal/domain"
	"git.skobk.in/skobkin/v4lens/internal/profile"
)

// Event is one typed application event. Consumers type-switch on the
// concrete struct; the unexported method seals the interface.
type Event interface {
	eventMarker()
}

// DevicesChanged announces the current device list and their states.
type DevicesChanged struct {
	Devices []DeviceView
}

// ControlsSnapshot carries the full control tree plus current values for
// one device (read-backs, never user intent).
type ControlsSnapshot struct {
	Key         string
	Descriptors []domain.ControlDescriptor
	Values      map[uint32]domain.ControlValue
}

// FormatSnapshot carries input/format capabilities and the effective
// capture configuration for one device.
type FormatSnapshot struct {
	Key     string
	Inputs  []domain.InputInfo
	Formats []domain.FormatDesc
	Active  domain.CaptureConfig
}

// ApplyFinished reports the structured outcome of one profile apply.
type ApplyFinished struct {
	Key    string
	Result profile.ApplyResult
}

// PreviewState announces preview lifecycle changes for one device
// (populated by the preview integration; payload refined in that phase).
type PreviewState struct {
	Key   string
	State string
}

// PrefsChanged announces new persisted preferences.
type PrefsChanged struct {
	Prefs config.Prefs
}

// ActivationRequested asks the app to raise its main window (second-instance
// activation; populated by the platform integration in its phase).
type ActivationRequested struct{}

func (DevicesChanged) eventMarker()      {}
func (ControlsSnapshot) eventMarker()    {}
func (FormatSnapshot) eventMarker()      {}
func (ApplyFinished) eventMarker()       {}
func (PreviewState) eventMarker()        {}
func (PrefsChanged) eventMarker()        {}
func (ActivationRequested) eventMarker() {}

// DeviceView is one device's identity and truthful connection state.
type DeviceView struct {
	Identity  domain.DeviceIdentity
	Status    domain.DeviceStatus
	Connected bool
}

// Bus is the typed publish/subscribe hub. Publishers never block; the
// slowest subscriber's overflow drops its OLDEST queued events (state
// snapshots re-publish on change, so a dropped stale event is safe).
type Bus struct {
	mu   sync.Mutex
	next int
	subs map[int]chan Event
}

// NewBus builds an empty bus.
func NewBus() *Bus {
	return &Bus{subs: map[int]chan Event{}}
}

// Subscribe registers a subscriber and returns its event channel plus an
// unsubscribe func.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)

	b.mu.Lock()
	id := b.next
	b.next++
	b.subs[id] = ch
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		delete(b.subs, id)
		close(ch)
		b.mu.Unlock()
	}
}

// Publish delivers the event to every subscriber without blocking.
func (b *Bus) Publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default:
			// Drop the oldest event to make room; never block publishers.
			select {
			case <-ch:
			default:
			}

			select {
			case ch <- ev:
			default:
			}
		}
	}
}
