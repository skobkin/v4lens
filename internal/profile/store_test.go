package profile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

func testProfile(name string) *Profile {
	iv := domain.FrameInterval{Num: 1001, Den: 60000} // exact 59.94 fps
	str := "hello \"yaml\": ok"

	return &Profile{
		Name: name,
		Match: Match{
			Key:         "test-pw513-0001-abcd1234",
			DisplayName: "Synthetic Cam 4K",
			Serial:      "TEST-PW513-0001",
			USBVendor:   "1a86",
			USBProduct:  "7523",
		},
		Capture: &domain.CaptureConfig{
			Input:    1,
			FourCC:   "MJPG",
			Width:    1920,
			Height:   1080,
			Interval: &iv,
		},
		Controls: []ControlSetting{
			{ID: 0x00980900, Name: "Brightness", Value: domain.NewIntValue(0x00980900, domain.TypeInteger, 0)},
			{ID: 0x00980901, Name: "Contrast", Value: domain.NewIntValue(0x00980901, domain.TypeInteger, 127)},
			{ID: 0x00980902, Name: "Saturation", Value: domain.NewIntValue(0x00980902, domain.TypeInteger, 64)},
			{ID: 0x00980905, Name: "White Balance Auto", Value: domain.NewBoolValue(0x00980905, false)},
			{ID: 0x0098090c, Name: "Backlight Comp", Value: domain.NewIntValue(0x0098090c, domain.TypeInteger64, 1<<40)},
			{ID: 0x00980913, Name: "Label", Value: domain.NewStringValue(0x00980913, str)},
			{ID: 0x00980914, Name: "Scene Mode", Value: domain.NewIntValue(0x00980914, domain.TypeMenu, 2)},
			{ID: 0x00980915, Name: "Test Pattern", Value: domain.NewIntValue(0x00980915, domain.TypeBitmask, 0xDEADBEEF)},
		},
	}
}

func TestStoreRoundTripPreservesTypes(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "profiles"), nil)

	want := testProfile("Video Calls")

	if err := s.Save("dev-key", want); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := s.Load("dev-key", "Video Calls")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got.Name != want.Name || got.Match.Key != want.Match.Key {
		t.Fatalf("header mismatch: %+v", got)
	}

	if got.Capture == nil || want.Capture == nil {
		t.Fatalf("capture mismatch: %v vs %v", got.Capture, want.Capture)
	}

	if !got.Capture.Equal(*want.Capture) {
		t.Fatalf("capture changed: %+v vs %+v", got.Capture, want.Capture)
	}

	if got.Capture.Interval == nil || *got.Capture.Interval != (domain.FrameInterval{Num: 1001, Den: 60000}) {
		t.Fatalf("exact rational interval lost: %+v", got.Capture.Interval)
	}

	if len(got.Controls) != len(want.Controls) {
		t.Fatalf("control count: %d vs %d", len(got.Controls), len(want.Controls))
	}

	for i, w := range want.Controls {
		g := got.Controls[i]

		if g.ID != w.ID || !sameValue(w.Value, g.Value) {
			t.Fatalf("control %d: want %+v got %+v", i, w, g)
		}
	}
}

func TestStoreZeroValuesSurvive(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "profiles"), nil)

	p := &Profile{
		Name:  "Zeroes",
		Match: Match{Key: "k"},
		Controls: []ControlSetting{
			{ID: 1, Name: "Bool off", Value: domain.NewBoolValue(1, false)},
			{ID: 2, Name: "Int zero", Value: domain.NewIntValue(2, domain.TypeInteger, 0)},
			{ID: 3, Name: "Empty string", Value: domain.NewStringValue(3, "")},
		},
	}

	if err := s.Save("k", p); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := s.Load("k", "Zeroes")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	for i, w := range p.Controls {
		if !sameValue(w.Value, got.Controls[i].Value) {
			t.Fatalf("zero value lost on %q: %+v vs %+v", w.Name, w.Value, got.Controls[i].Value)
		}
	}
}

func TestStorePermissionsAndAtomicity(t *testing.T) {
	root := filepath.Join(t.TempDir(), "profiles")
	s := NewStore(root, nil)

	if err := s.Save("k", testProfile("P")); err != nil {
		t.Fatalf("save: %v", err)
	}

	path, err := s.Path("k", "P")
	if err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("profile perms = %o, want 600", perm)
	}

	// No temp leftovers in the device dir.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected exactly one file, got %d", len(entries))
	}
}

func TestStoreListDeleteRename(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "profiles"), nil)

	for _, n := range []string{"Beta", "alpha", "Gamma"} {
		p := testProfile(n)
		p.Match.Key = "k"

		if err := s.Save("k", p); err != nil {
			t.Fatalf("save %q: %v", n, err)
		}
	}

	names, err := s.List("k")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"Beta", "Gamma", "alpha"}

	for i, w := range want {
		if names[i] != w {
			t.Fatalf("list[%d] = %q, want %q (full: %v)", i, names[i], w, names)
		}
	}

	if names, _ := s.List("missing"); len(names) != 0 {
		t.Fatalf("missing device must list empty, got %v", names)
	}

	if err := s.Rename("k", "Beta", "Renamed"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	names, _ = s.List("k")

	if names[0] != "Gamma" || names[1] != "Renamed" || names[2] != "alpha" {
		t.Fatalf("after rename: %v", names)
	}

	p, err := s.Load("k", "Renamed")
	if err != nil {
		t.Fatal(err)
	}

	if p.Name != "Renamed" {
		t.Fatalf("renamed profile keeps stale name %q", p.Name)
	}

	if err := s.Delete("k", "Renamed"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := s.Load("k", "Renamed"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted profile still loads: %v", err)
	}
}

func TestStoreMalformedAndInvalid(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "profiles"), nil)

	if err := s.Save("k", testProfile("ok")); err != nil {
		t.Fatal(err)
	}

	// Corrupt the file directly.
	path, _ := s.Path("k", "ok")

	if err := os.WriteFile(path, []byte("schema_version: [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Load("k", "ok"); err == nil {
		t.Fatal("malformed YAML must error")
	}

	// Type/value mismatch: integer control without an int value.
	if err := os.WriteFile(path, []byte("controls:\n  - id: 1\n    type: integer\n    name: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Load("k", "ok"); err == nil {
		t.Fatal("integer control without value must error")
	}

	// Invalid interval 0/0 never parses back.
	raw := "capture:\n  fourcc: MJPG\n  width: 640\n  height: 480\n  interval: {num: 0, den: 0}\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Load("k", "ok"); err == nil {
		t.Fatal("0/0 interval must be rejected")
	}

	// Unusable profile names never map to paths.
	if _, err := s.Path("k", "///"); err == nil {
		t.Fatal("empty slug must error")
	}

	if _, err := s.Path("k", ""); err == nil {
		t.Fatal("empty name must error")
	}
}

func TestStoreNameSlug(t *testing.T) {
	f, err := fileName("Video Calls")
	if err != nil || f != "Video-Calls.yaml" {
		t.Fatalf("simple slug: %q %v", f, err)
	}

	f, err = fileName("a/b:c*d?")
	if err != nil || f != "a-b-c-d.yaml" {
		t.Fatalf("unsafe chars slug: %q %v", f, err)
	}

	long := stringOf(100)

	f, err = fileName(long)
	if err != nil {
		t.Fatal(err)
	}

	if len(f) != 64+5 || f[:64] != long[:64] {
		t.Fatalf("long name not capped: len=%d", len(f))
	}

	if _, err := fileName("///"); err == nil {
		t.Fatal("empty slug must error")
	}
}

func stringOf(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}

	return string(b)
}
