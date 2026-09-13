// Package platform provides Linux desktop integration primitives shared
// across V4Lens: XDG base-directory resolution, atomic file writes and the
// single-instance / activation / autostart plumbing added in later phases.
package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvLookup resolves an environment variable name to its value.
type EnvLookup func(name string) string

// OSLookup is the production EnvLookup backed by the process environment.
func OSLookup(name string) string {
	return os.Getenv(name)
}

// homeDir resolves $HOME per the XDG base-directory specification. An empty
// result means the caller has no usable home fallback.
func homeDir(lookup EnvLookup) string {
	return strings.TrimSpace(lookup("HOME"))
}

// absEnv returns the variable value when set to an absolute path. The XDG
// specification requires absolute paths; relative values are ignored so the
// documented fallback applies.
func absEnv(lookup EnvLookup, name string) string {
	v := strings.TrimSpace(lookup(name))

	if v != "" && filepath.IsAbs(v) {
		return filepath.Clean(v)
	}

	return ""
}

// ConfigHome resolves the XDG config home: $XDG_CONFIG_HOME or $HOME/.config.
// Returns "" when neither the variable nor $HOME is usable.
func ConfigHome(lookup EnvLookup) string {
	if v := absEnv(lookup, "XDG_CONFIG_HOME"); v != "" {
		return v
	}

	if home := homeDir(lookup); home != "" {
		return filepath.Join(home, ".config")
	}

	return ""
}

// StateHome resolves the XDG state home: $XDG_STATE_HOME or $HOME/.local/state.
// Logs belong here, not under .local/share (DECISION_LOG D010). Returns ""
// when neither the variable nor $HOME is usable.
func StateHome(lookup EnvLookup) string {
	if v := absEnv(lookup, "XDG_STATE_HOME"); v != "" {
		return v
	}

	if home := homeDir(lookup); home != "" {
		return filepath.Join(home, ".local", "state")
	}

	return ""
}

// RuntimeDir resolves the XDG runtime directory: $XDG_RUNTIME_DIR or, when
// unset, a /tmp directory private to the user. The fallback does not meet the
// full XDG runtime-dir contract (no session supervision); it is only a
// best-effort location for lock/activation sockets.
func RuntimeDir(lookup EnvLookup, uid int) string {
	if v := absEnv(lookup, "XDG_RUNTIME_DIR"); v != "" {
		return v
	}

	return filepath.Join(os.TempDir(), fmt.Sprintf("v4lens-runtime-%d", uid))
}

// Dirs resolves V4Lens application paths through one environment view.
// The zero value is not usable; construct with NewDirs or Dirs{Lookup: ...}.
type Dirs struct {
	Lookup EnvLookup
	UID    int
}

// NewDirs returns a Dirs bound to the process environment.
func NewDirs() Dirs {
	return Dirs{Lookup: OSLookup, UID: os.Getuid()}
}

// ForTest returns a Dirs bound to a custom lookup (used by tests and docs).
func ForTest(lookup EnvLookup, uid int) Dirs {
	return Dirs{Lookup: lookup, UID: uid}
}

func (d Dirs) configHome() string {
	return ConfigHome(d.Lookup)
}

func (d Dirs) stateHome() string {
	return StateHome(d.Lookup)
}

// AppConfigDir is $XDG_CONFIG_HOME/v4lens (ARCHITECTURE §4).
func (d Dirs) AppConfigDir() string {
	return filepath.Join(d.configHome(), "v4lens")
}

// AppConfigFile is the path of config.yaml (preferences and per-device
// default-profile references).
func (d Dirs) AppConfigFile() string {
	return filepath.Join(d.AppConfigDir(), "config.yaml")
}

// ProfilesRoot is $XDG_CONFIG_HOME/v4lens/profiles; per-device profile
// directories live directly beneath it.
func (d Dirs) ProfilesRoot() string {
	return filepath.Join(d.AppConfigDir(), "profiles")
}

// AutostartDir is $XDG_CONFIG_HOME/autostart — the only place V4Lens writes
// its autostart entry (D009); never $XDG_DATA_HOME/applications.
func (d Dirs) AutostartDir() string {
	return filepath.Join(d.configHome(), "autostart")
}

// AppStateDir is $XDG_STATE_HOME/v4lens.
func (d Dirs) AppStateDir() string {
	return filepath.Join(d.stateHome(), "v4lens")
}

// LogFile is $XDG_STATE_HOME/v4lens/v4lens.log (ARCHITECTURE §12).
func (d Dirs) LogFile() string {
	return filepath.Join(d.AppStateDir(), "v4lens.log")
}

// AppRuntimeDir is the V4Lens-private runtime directory (single-instance
// lock and activation state): $XDG_RUNTIME_DIR/v4lens.
func (d Dirs) AppRuntimeDir() string {
	return filepath.Join(RuntimeDir(d.Lookup, d.UID), "v4lens")
}
