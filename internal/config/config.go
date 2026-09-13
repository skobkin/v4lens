// Package config stores V4Lens application preferences (config.yaml):
// the MVP preference set plus per-device default-profile references
// (ARCHITECTURE §4, UI_UX_SPEC §10).
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"git.skobk.in/skobkin/v4lens/internal/platform"
)

// Prefs is the persisted preference set. Field order and names are part of
// the on-disk format; extend additively so older files keep loading.
type Prefs struct {
	// StartWithSystem mirrors the autostart entry (D008).
	StartWithSystem bool `yaml:"start_with_system"`

	// ApplyOnStart applies each connected device's default profile at startup.
	ApplyOnStart bool `yaml:"apply_profile_on_start"`

	// NotifyApplyIssues enables desktop notifications for meaningful
	// partial/default-profile apply failures.
	NotifyApplyIssues bool `yaml:"notify_on_apply_issues"`

	// LogLevel is one of debug/info/warn/error.
	LogLevel string `yaml:"log_level"`

	// LogToFile routes logs to the XDG state log file instead of stderr.
	LogToFile bool `yaml:"log_to_file"`

	// Defaults maps a stable device key to its default profile name.
	Defaults map[string]string `yaml:"default_profiles,omitempty"`
}

// Default returns the preference set used when no file exists yet.
func Default() Prefs {
	return Prefs{
		StartWithSystem:   false,
		ApplyOnStart:      true,
		NotifyApplyIssues: true,
		LogLevel:          "info",
		LogToFile:         true,
		Defaults:          map[string]string{},
	}
}

// Store loads and saves prefs.yaml content at one fixed path. It is safe for
// use from one goroutine at a time; callers serialize via the app layer.
type Store struct {
	path string
}

// NewStore returns a store for path (normally Dirs.AppConfigFile()).
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Path returns the backing file path (diagnostics).
func (s *Store) Path() string {
	return s.path
}

// Load reads the preference file. A missing file yields Default() with a nil
// error so first launch behaves like a fresh install.
func (s *Store) Load() (Prefs, error) {
	data, err := os.ReadFile(s.path) //nolint:gosec // path comes from the XDG configuration owned by the user

	switch {
	case os.IsNotExist(err):
		return Default(), nil
	case err != nil:
		return Prefs{}, fmt.Errorf("read config: %w", err)
	}

	var prefs Prefs

	if err := yaml.Unmarshal(data, &prefs); err != nil {
		return Prefs{}, fmt.Errorf("parse config %s: %w", s.path, err)
	}

	return prefs.sanitized(), nil
}

// Save writes prefs atomically with 0600 permissions.
func (s *Store) Save(prefs Prefs) error {
	data, err := yaml.Marshal(prefs.sanitized())
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	if err := platform.WriteFileAtomic(s.path, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	return nil
}

// sanitized normalizes user-editable free-form fields so the rest of the
// application can rely on sane values.
func (p Prefs) sanitized() Prefs {
	if p.LogLevel == "" {
		p.LogLevel = "info"
	}

	if p.Defaults == nil {
		p.Defaults = map[string]string{}
	}

	return p
}
