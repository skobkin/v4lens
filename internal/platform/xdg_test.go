package platform

import (
	"os"
	"path/filepath"
	"testing"
)

// lookup builds an EnvLookup from a plain map.
func lookup(env map[string]string) EnvLookup {
	return func(name string) string {
		return env[name]
	}
}

func TestConfigHome(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"env absolute wins", map[string]string{"XDG_CONFIG_HOME": "/xdg/cfg", "HOME": "/home/u"}, "/xdg/cfg"},
		{"env relative ignored", map[string]string{"XDG_CONFIG_HOME": "rel/path", "HOME": "/home/u"}, "/home/u/.config"},
		{"env empty falls back", map[string]string{"XDG_CONFIG_HOME": "", "HOME": "/home/u"}, "/home/u/.config"},
		{"env unset falls back", map[string]string{"HOME": "/home/u"}, "/home/u/.config"},
		{"no home no path", map[string]string{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConfigHome(lookup(tt.env)); got != tt.want {
				t.Fatalf("ConfigHome = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStateHome(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"env absolute wins", map[string]string{"XDG_STATE_HOME": "/xdg/state", "HOME": "/home/u"}, "/xdg/state"},
		{"env relative ignored", map[string]string{"XDG_STATE_HOME": "state", "HOME": "/home/u"}, "/home/u/.local/state"},
		{"fallback is .local/state not .local/share", map[string]string{"HOME": "/home/u"}, "/home/u/.local/state"},
		{"no home no path", map[string]string{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StateHome(lookup(tt.env)); got != tt.want {
				t.Fatalf("StateHome = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRuntimeDir(t *testing.T) {
	if got := RuntimeDir(lookup(map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"}), 1000); got != "/run/user/1000" {
		t.Fatalf("RuntimeDir env = %q", got)
	}

	got := RuntimeDir(lookup(map[string]string{}), 1000)

	if want := filepath.Join(os.TempDir(), "v4lens-runtime-1000"); got != want {
		t.Fatalf("RuntimeDir fallback = %q, want %q", got, want)
	}
}

func TestDirs(t *testing.T) {
	d := ForTest(lookup(map[string]string{"XDG_CONFIG_HOME": "/cfg", "XDG_STATE_HOME": "/st"}), 42)

	checks := map[string]struct{ got, want string }{
		"config dir":    {d.AppConfigDir(), "/cfg/v4lens"},
		"config file":   {d.AppConfigFile(), "/cfg/v4lens/config.yaml"},
		"profiles root": {d.ProfilesRoot(), "/cfg/v4lens/profiles"},
		"autostart dir": {d.AutostartDir(), "/cfg/autostart"},
		"state dir":     {d.AppStateDir(), "/st/v4lens"},
		"log file":      {d.LogFile(), "/st/v4lens/v4lens.log"},
	}

	for name, c := range checks {
		t.Run(name, func(t *testing.T) {
			if c.got != c.want {
				t.Fatalf("got %q, want %q", c.got, c.want)
			}
		})
	}
}

func TestWriteFileAtomic(t *testing.T) {
	t.Run("writes content with permissions and no leftovers", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "nested", "config.yaml")

		if err := WriteFileAtomic(path, []byte("hello"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}

		got, err := os.ReadFile(path) //nolint:gosec // test-local temp path
		if err != nil {
			t.Fatalf("read back: %v", err)
		}

		if string(got) != "hello" {
			t.Fatalf("content = %q", got)
		}

		st, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}

		if st.Mode().Perm() != 0o600 {
			t.Fatalf("perm = %v, want 0600", st.Mode().Perm())
		}

		entries, err := os.ReadDir(filepath.Dir(path))
		if err != nil {
			t.Fatalf("readdir: %v", err)
		}

		if len(entries) != 1 || entries[0].Name() != "config.yaml" {
			t.Fatalf("leftover temp files: %v", entries)
		}
	})

	t.Run("failed write leaves no temp files", func(t *testing.T) {
		dir := t.TempDir()
		blocker := filepath.Join(dir, "blocker")

		if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
			t.Fatalf("setup: %v", err)
		}

		// The target lives beneath a regular file, so MkdirAll must fail.
		path := filepath.Join(blocker, "config.yaml")

		if err := WriteFileAtomic(path, []byte("hello"), 0o600); err == nil {
			t.Fatal("expected error when a parent path is a regular file")
		}
	})

	t.Run("overwrite replaces content atomically", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")

		if err := WriteFileAtomic(path, []byte("one"), 0o600); err != nil {
			t.Fatalf("first write: %v", err)
		}

		if err := WriteFileAtomic(path, []byte("two"), 0o600); err != nil {
			t.Fatalf("second write: %v", err)
		}

		got, _ := os.ReadFile(path) //nolint:gosec // test-local temp path

		if string(got) != "two" {
			t.Fatalf("content = %q, want %q", got, "two")
		}
	})
}
