package logging

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in    string
		want  slog.Level
		valid bool
	}{
		{"debug", slog.LevelDebug, true},
		{"INFO", slog.LevelInfo, true},
		{"warn", slog.LevelWarn, true},
		{"warning", slog.LevelWarn, true},
		{"error", slog.LevelError, true},
		{"", slog.LevelInfo, true},
		{"loud", slog.LevelInfo, false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := ParseLevel(tt.in)

			if got != tt.want || ok != tt.valid {
				t.Fatalf("ParseLevel(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.valid)
			}
		})
	}
}

func TestNewToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "v4lens.log")

	m := New(Options{Level: "debug", ToFile: true, Path: path})
	defer func() { _ = m.Close() }()

	if m.DegradedReason() != "" {
		t.Fatalf("unexpected degradation: %s", m.DegradedReason())
	}

	m.Logger().Info("hello file", "component", "test")

	data, err := os.ReadFile(path) //nolint:gosec // test-local temp path
	if err != nil {
		t.Fatalf("read log: %v", err)
	}

	if !strings.Contains(string(data), "hello file") || !strings.Contains(string(data), "component=test") {
		t.Fatalf("record not written: %q", data)
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if st.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v, want 0600", st.Mode().Perm())
	}
}

func TestNewLevelFilteringAndSwitch(t *testing.T) {
	var buf bytes.Buffer

	m := New(Options{Level: "info", Stderr: &buf})
	defer func() { _ = m.Close() }()

	m.Logger().Debug("hidden")
	m.Logger().Info("shown")

	if strings.Contains(buf.String(), "hidden") {
		t.Fatalf("debug record leaked at info level: %q", buf.String())
	}

	if !strings.Contains(buf.String(), "shown") {
		t.Fatalf("info record missing: %q", buf.String())
	}

	if !m.SetLevel("debug") {
		t.Fatal("SetLevel(debug) reported failure")
	}

	m.Logger().Debug("visible now")

	if !strings.Contains(buf.String(), "visible now") {
		t.Fatalf("level switch did not take effect: %q", buf.String())
	}

	if m.SetLevel("nope") {
		t.Fatal("SetLevel accepted an unknown level name")
	}
}

func TestNewDegradation(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")

	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var buf bytes.Buffer

	// Log path lives beneath a regular file: opening must fail and the
	// manager must degrade to stderr instead of failing startup.
	m := New(Options{Level: "info", ToFile: true, Path: filepath.Join(blocker, "v4lens.log"), Stderr: &buf})
	defer func() { _ = m.Close() }()

	if m.DegradedReason() == "" {
		t.Fatal("expected a degradation reason")
	}

	m.Logger().Info("still logs")

	if !strings.Contains(buf.String(), "still logs") {
		t.Fatalf("degraded logger did not write to stderr sink: %q", buf.String())
	}
}
