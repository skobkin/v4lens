package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutostartEnableDisableCycle(t *testing.T) {
	dirs, root := testDirs(t)

	if AutostartEnabled(dirs) {
		t.Fatal("fresh tree must not have autostart enabled")
	}

	if err := EnableAutostart(dirs); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, "config", "autostart", AutostartFilename)

	if !AutostartEnabled(dirs) {
		t.Fatal("autostart must be enabled after EnableAutostart")
	}

	data, err := os.ReadFile(path) // #nosec G304 -- test-owned temp path
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)

	// The entry must start hidden (background) and be a valid application
	// entry pointing at the test executable.
	for _, want := range []string{
		"[Desktop Entry]",
		"Type=Application",
		`Exec="` + strings.TrimSpace(mustExecutable(t)) + `" --background`,
		"Terminal=false",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("autostart entry missing %q:\n%s", want, content)
		}
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if perm := st.Mode().Perm(); perm != 0o644 {
		t.Fatalf("autostart perm %o, want 644", perm)
	}

	// D009: never under XDG data directories.
	if err := ValidateAutostartPath(dirs); err != nil {
		t.Fatal(err)
	}

	if strings.HasPrefix(path, filepath.Join(root, "data")) {
		t.Fatalf("autostart entry under XDG_DATA_HOME: %s", path)
	}

	// Enable is idempotent (overwrite, not append/duplicate).
	if err := EnableAutostart(dirs); err != nil {
		t.Fatal(err)
	}

	data2, err := os.ReadFile(path) // #nosec G304 -- test-owned temp path
	if err != nil {
		t.Fatal(err)
	}

	if string(data2) != content {
		t.Fatalf("re-enable changed the entry:\n%s\n---\n%s", content, data2)
	}

	if err := DisableAutostart(dirs); err != nil {
		t.Fatal(err)
	}

	if AutostartEnabled(dirs) {
		t.Fatal("autostart still enabled after DisableAutostart")
	}

	// Disabling again is a no-op.
	if err := DisableAutostart(dirs); err != nil {
		t.Fatalf("double disable: %v", err)
	}
}

func mustExecutable(t *testing.T) string {
	t.Helper()

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	return exe
}
