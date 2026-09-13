package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Autostart (D009): the only autostart artifact V4Lens ever writes is
// $XDG_CONFIG_HOME/autostart/v4lens.desktop — never a $XDG_DATA_HOME
// launcher.
const (
	AutostartFilename = "v4lens.desktop"
	autostartFilePerm = 0o644
)

// AutostartPath is the autostart desktop entry location for dirs.
func AutostartPath(dirs Dirs) string {
	return filepath.Join(dirs.AutostartDir(), AutostartFilename)
}

// autostartEntry renders the desktop entry. Exec is always quoted so paths
// with spaces stay spec-valid; --background keeps the main window hidden.
func autostartEntry(executable string) string {
	return fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=V4Lens
Comment=V4L2 camera configuration
Exec=%q --background
Terminal=false
`, executable)
}

// EnableAutostart writes (or overwrites) the autostart entry pointing at
// the currently running executable. Idempotent.
func EnableAutostart(dirs Dirs) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable for autostart: %w", err)
	}

	exe = filepath.Clean(exe)

	dir := dirs.AutostartDir()

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create autostart dir: %w", err)
	}

	path := filepath.Join(dir, AutostartFilename)

	// G304: path is the app-owned autostart entry under the user's config home.
	if err := os.WriteFile(path, []byte(autostartEntry(exe)), autostartFilePerm); err != nil { // #nosec G304

		return fmt.Errorf("write autostart entry: %w", err)
	}

	return nil
}

// DisableAutostart removes the autostart entry. Removing an absent entry
// is a no-op.
func DisableAutostart(dirs Dirs) error {
	err := os.Remove(AutostartPath(dirs))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove autostart entry: %w", err)
	}

	return nil
}

// AutostartEnabled reports whether the autostart entry exists.
func AutostartEnabled(dirs Dirs) bool {
	_, err := os.Stat(AutostartPath(dirs))

	return err == nil
}

// ValidateAutostartPath guards the D009 invariant: the autostart path must
// live under the XDG config home, never under XDG data directories.
func ValidateAutostartPath(dirs Dirs) error {
	path := AutostartPath(dirs)

	cfgHome := ConfigHome(dirs.Lookup)
	if cfgHome != "" && strings.HasPrefix(path, cfgHome+string(filepath.Separator)) {
		return nil
	}

	return fmt.Errorf("autostart path %q is not under the config home", path)
}
