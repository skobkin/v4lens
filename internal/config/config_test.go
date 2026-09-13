package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadMissingFileYieldsDefaults(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "missing", "config.yaml"))

	prefs, err := s.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	want := Default()

	if !reflect.DeepEqual(prefs, want) {
		t.Fatalf("prefs = %+v, want %+v", prefs, want)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	s := NewStore(path)

	prefs := Prefs{
		StartWithSystem:   true,
		ApplyOnStart:      false,
		NotifyApplyIssues: false,
		LogLevel:          "debug",
		LogToFile:         false,
		Defaults:          map[string]string{"dev-key-1": "Low light"},
	}

	if err := s.Save(prefs); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if !reflect.DeepEqual(got, prefs) {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, prefs)
	}

	// Value types must survive: the Defaults map and the exact log level.
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if st.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v, want 0600", st.Mode().Perm())
	}
}

func TestSaveLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "config.yaml"))

	if err := s.Save(Default()); err != nil {
		t.Fatalf("save: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}

	if len(entries) != 1 || entries[0].Name() != "config.yaml" {
		t.Fatalf("unexpected directory contents: %v", entries)
	}
}

func TestLoadMalformedYAMLIsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	if err := os.WriteFile(path, []byte("log_level: [unclosed"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if _, err := NewStore(path).Load(); err == nil {
		t.Fatal("expected an error for malformed YAML")
	}
}

func TestLoadFillsSanitizedDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")

	content := "start_with_system: true\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	prefs, err := NewStore(path).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if !prefs.StartWithSystem {
		t.Fatal("stored value lost")
	}

	if prefs.LogLevel != "info" {
		t.Fatalf("log level = %q, want defaulted %q", prefs.LogLevel, "info")
	}

	if prefs.Defaults == nil {
		t.Fatal("Defaults map must be non-nil after load")
	}
}
