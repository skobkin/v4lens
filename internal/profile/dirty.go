package profile

import (
	"fmt"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// DeviceSnapshot is the effective device state the dirty comparison runs
// against. Values are read-backs from the device, never user intent.
type DeviceSnapshot struct {
	Capture  domain.CaptureConfig
	Controls map[uint32]domain.ControlValue
}

// DiffKind distinguishes capture-config diffs from control diffs.
type DiffKind int8

const (
	DiffControl DiffKind = iota
	DiffCapture
)

// String returns a short diff-kind name.
func (k DiffKind) String() string {
	if k == DiffCapture {
		return "capture"
	}

	return "control"
}

// DiffEntry is one difference between a profile and effective device state.
type DiffEntry struct {
	Kind   DiffKind
	ID     uint32
	Name   string
	Detail string
}

// Dirty compares the profile's managed fields (its capture config and
// profiled control values) against the effective snapshot — never every
// discovered control. An empty result means the device matches the profile.
func Dirty(p *Profile, snap DeviceSnapshot) []DiffEntry {
	var out []DiffEntry

	if p.Capture != nil {
		out = append(out, captureDiffs(p.Capture, &snap.Capture)...)
	}

	for _, s := range p.Controls {
		v, ok := snap.Controls[s.ID]
		if !ok {
			out = append(out, DiffEntry{
				Kind:   DiffControl,
				ID:     s.ID,
				Name:   s.Name,
				Detail: "control not present on device",
			})

			continue
		}

		if !sameValue(s.Value, v) {
			out = append(out, DiffEntry{
				Kind:   DiffControl,
				ID:     s.ID,
				Name:   s.Name,
				Detail: fmt.Sprintf("profile %s, device %s", s.Value, v),
			})
		}
	}

	return out
}

func captureDiffs(want, got *domain.CaptureConfig) []DiffEntry {
	var out []DiffEntry

	add := func(format string, args ...any) {
		out = append(out, DiffEntry{
			Kind:   DiffCapture,
			Detail: fmt.Sprintf(format, args...),
		})
	}

	if want.Input != got.Input {
		add("input: profile %d, device %d", want.Input, got.Input)
	}

	if want.FourCC != got.FourCC {
		add("format: profile %s, device %s", want.FourCC, got.FourCC)
	}

	if want.Width != got.Width || want.Height != got.Height {
		add("size: profile %dx%d, device %dx%d", want.Width, want.Height, got.Width, got.Height)
	}

	switch {
	case want.Interval == nil || got.Interval == nil:
		// A missing interval on either side is only a diff when the
		// profile manages one the device cannot honor (e.g. capability
		// changed); a profile without an interval stays dirty-free.
		if want.Interval != nil && got.Interval == nil {
			add("frame interval: profile %d/%d, device reports none", want.Interval.Num, want.Interval.Den)
		}
	case *want.Interval != *got.Interval:
		add("frame interval: profile %d/%d, device %d/%d", want.Interval.Num, want.Interval.Den, got.Interval.Num, got.Interval.Den)
	}

	return out
}

func sameValue(a, b domain.ControlValue) bool {
	if a.Type != b.Type {
		return false
	}

	switch a.Type {
	case domain.TypeBoolean:

		return a.Bool == b.Bool
	case domain.TypeString:

		return a.Str == b.Str
	default:

		return a.Int == b.Int
	}
}
