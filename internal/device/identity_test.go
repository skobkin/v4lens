package device

import (
	"strings"
	"testing"
)

func TestBuildKeyDeterministicAndSafe(t *testing.T) {
	src := `usb-Synthetic_Cam_4K_TEST-PW513-0001-video-index0`

	a := BuildKey(src)
	b := BuildKey(src)

	if a != b {
		t.Fatalf("non-deterministic: %q vs %q", a, b)
	}

	if a == src {
		t.Fatal("key must be transformed, not the raw source")
	}

	// Slug part is filesystem-safe; hash part present after the last '-'.
	if idx := strings.LastIndex(a, "-"); idx <= 0 || len(a[idx+1:]) != 8 {
		t.Fatalf("key shape wrong: %q", a)
	}

	for _, c := range a {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' {
			continue
		}

		t.Fatalf("unsafe character %q in %q", c, a)
	}
}

func TestBuildKeyDifferentiatesSources(t *testing.T) {
	keys := map[string]bool{
		BuildKey("usb-serial-AAA"): true,
		BuildKey("usb-serial-BBB"): true,
		BuildKey("usb-serial-BBB"): true,
	}

	if len(keys) != 2 {
		t.Fatalf("distinct sources collided: %v", keys)
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Simple Name", "simple-name"},
		{"  trim  me  ", "trim-me"},
		{"weird///path\\stuff", "weird-path-stuff"},
		{"", "device"},
	}

	for _, tt := range tests {
		if got := slugify(tt.in, 48); got != tt.want {
			t.Fatalf("slugify(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	long := slugify(strings.Repeat("x", 100), 16)

	if len(long) != 16 {
		t.Fatalf("slug not capped: %d", len(long))
	}
}

func TestPickIdentitySourceTiers(t *testing.T) {
	if s := pickIdentitySource("by-id-entry", "1a86", "7523", "TEST-0001", "by-path-entry", "/sys/x"); s.tier != 1 || s.value != "by-id-entry" {
		t.Fatalf("tier1: %+v", s)
	}

	s := pickIdentitySource("", "1A86", "7523", "TEST-0001", "by-path-entry", "/sys/x")
	if s.tier != 2 || !strings.Contains(s.value, "1a86") || !strings.Contains(s.value, "TEST-0001") {
		t.Fatalf("tier2: %+v", s)
	}

	if s := pickIdentitySource("", "", "", "", "by-path-entry", "/sys/x"); s.tier != 3 || s.value != "by-path-entry" {
		t.Fatalf("tier3: %+v", s)
	}

	s = pickIdentitySource("", "", "", "", "", "/sys/devices/virtual/video4linux/video0")
	if s.tier != 4 || s.fingerprint == "" {
		t.Fatalf("tier4: %+v", s)
	}
}
