// Package profile provides the per-device YAML profile store, the
// profile-managed control-set rules, the dirty comparison against effective
// device state, and the bounded best-effort apply engine (ARCHITECTURE §7).
package profile

import (
	"fmt"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// SchemaVersion is the profile file format version.
const SchemaVersion = 1

// Match carries the device-identity metadata stored inside a profile so the
// profile directory/hash can be verified against the intended hardware
// (ARCHITECTURE §3). Matching is keyed on the stable identity Key; the rest
// is verification/diagnostic metadata.
type Match struct {
	Key         string `yaml:"key"`
	DisplayName string `yaml:"display_name,omitempty"`
	Serial      string `yaml:"serial,omitempty"`
	USBVendor   string `yaml:"usb_vendor,omitempty"`
	USBProduct  string `yaml:"usb_product,omitempty"`
	ByID        string `yaml:"by_id,omitempty"`
	ByPath      string `yaml:"by_path,omitempty"`
	Fingerprint string `yaml:"fingerprint,omitempty"`
}

// ControlSetting is one profiled control value. Name rides along for
// human-readable diffs and diagnostics only — identity is the numeric ID.
type ControlSetting struct {
	ID    uint32
	Name  string
	Value domain.ControlValue
}

// Profile is one saved device state: the desired capture configuration and
// the profile-managed control set (writable state controls only, see
// domain.ControlDescriptor.Persistable).
type Profile struct {
	SchemaVersion int
	Name          string
	Match         Match

	// Capture is nil when the profile does not manage the capture
	// configuration. Interval is present only when the device supports
	// configurable timeperframe.
	Capture *domain.CaptureConfig

	Controls []ControlSetting
}

// ---- type-preserving YAML representation ----
//
// The YAML layer uses pointer fields so zero values (bool false, int 0,
// empty string) round-trip exactly; a missing value or a value that does
// not match the declared type is a malformed file, not a silent default.

type profileYAML struct {
	SchemaVersion int           `yaml:"schema_version"`
	Name          string        `yaml:"name"`
	Match         Match         `yaml:"match"`
	Capture       *captureYAML  `yaml:"capture,omitempty"`
	Controls      []settingYAML `yaml:"controls,omitempty"`
}

type captureYAML struct {
	Input    uint32        `yaml:"input"`
	FourCC   string        `yaml:"fourcc"`
	Width    uint32        `yaml:"width"`
	Height   uint32        `yaml:"height"`
	Interval *intervalYAML `yaml:"interval,omitempty"`
}

type intervalYAML struct {
	Num uint32 `yaml:"num"`
	Den uint32 `yaml:"den"`
}

type settingYAML struct {
	ID   uint32  `yaml:"id"`
	Name string  `yaml:"name"`
	Type string  `yaml:"type"`
	Bool *bool   `yaml:"bool,omitempty"`
	Int  *int64  `yaml:"int,omitempty"`
	Str  *string `yaml:"str,omitempty"`
}

func captureToYAML(c *domain.CaptureConfig) *captureYAML {
	if c == nil {
		return nil
	}

	out := &captureYAML{
		Input:  c.Input,
		FourCC: string(c.FourCC),
		Width:  c.Width,
		Height: c.Height,
	}

	if c.Interval != nil {
		out.Interval = &intervalYAML{Num: c.Interval.Num, Den: c.Interval.Den}
	}

	return out
}

func captureFromYAML(c *captureYAML) (*domain.CaptureConfig, error) {
	if c == nil {
		return nil, nil
	}

	out := &domain.CaptureConfig{
		Input:  c.Input,
		FourCC: domain.FOURCC(c.FourCC),
		Width:  c.Width,
		Height: c.Height,
	}

	if c.Interval != nil {
		iv := domain.FrameInterval{Num: c.Interval.Num, Den: c.Interval.Den}
		if !iv.Valid() {
			return nil, fmt.Errorf("profile %q: invalid frame interval %d/%d", c.FourCC, iv.Num, iv.Den)
		}

		out.Interval = &iv
	}

	return out, nil
}

func settingToYAML(s ControlSetting) settingYAML {
	out := settingYAML{ID: s.ID, Name: s.Name, Type: s.Value.Type.String()}

	switch s.Value.Type {
	case domain.TypeBoolean:
		b := s.Value.Bool
		out.Bool = &b
	case domain.TypeString:
		str := s.Value.Str
		out.Str = &str
	default:
		i := s.Value.Int
		out.Int = &i
	}

	return out
}

func settingFromYAML(s settingYAML) (ControlSetting, error) {
	t, err := parseControlType(s.Type)
	if err != nil {
		return ControlSetting{}, fmt.Errorf("control 0x%08x: %w", s.ID, err)
	}

	v := domain.ControlValue{ID: s.ID, Type: t}

	switch t {
	case domain.TypeBoolean:
		if s.Bool == nil {
			return ControlSetting{}, fmt.Errorf("control 0x%08x: boolean value missing", s.ID)
		}

		v.Bool = *s.Bool
	case domain.TypeString:
		if s.Str == nil {
			return ControlSetting{}, fmt.Errorf("control 0x%08x: string value missing", s.ID)
		}

		v.Str = *s.Str
	default:
		if s.Int == nil {
			return ControlSetting{}, fmt.Errorf("control 0x%08x: integer value missing", s.ID)
		}

		v.Int = *s.Int
	}

	return ControlSetting{ID: s.ID, Name: s.Name, Value: v}, nil
}

func parseControlType(s string) (domain.ControlType, error) {
	for _, t := range []domain.ControlType{
		domain.TypeInteger, domain.TypeInteger64, domain.TypeBoolean,
		domain.TypeMenu, domain.TypeIntegerMenu, domain.TypeString,
		domain.TypeBitmask, domain.TypeU8, domain.TypeU16, domain.TypeU32,
	} {
		if t.String() == s {
			return t, nil
		}
	}

	return 0, fmt.Errorf("unknown control type %q", s)
}

func profileToYAML(p *Profile) profileYAML {
	out := profileYAML{
		SchemaVersion: p.SchemaVersion,
		Name:          p.Name,
		Match:         p.Match,
		Capture:       captureToYAML(p.Capture),
	}

	for _, s := range p.Controls {
		out.Controls = append(out.Controls, settingToYAML(s))
	}

	return out
}

func profileFromYAML(in profileYAML) (*Profile, error) {
	capture, err := captureFromYAML(in.Capture)
	if err != nil {
		return nil, err
	}

	p := &Profile{
		SchemaVersion: in.SchemaVersion,
		Name:          in.Name,
		Match:         in.Match,
		Capture:       capture,
	}

	for _, s := range in.Controls {
		setting, err := settingFromYAML(s)
		if err != nil {
			return nil, err
		}

		p.Controls = append(p.Controls, setting)
	}

	return p, nil
}
