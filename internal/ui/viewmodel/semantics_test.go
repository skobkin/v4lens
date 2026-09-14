package viewmodel

import (
	"testing"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

func TestClassifyCuratedControls(t *testing.T) {
	cases := []struct {
		id    uint32
		tab   Tab
		group string
	}{
		{0x00980900, TabImage, GroupImage},           // Brightness
		{0x00980901, TabImage, GroupImage},           // Contrast
		{0x0098090c, TabAdvanced, GroupWhiteBalance}, // WB auto
		{0x0098090d, TabAdvanced, GroupWhiteBalance}, // Do WB
		{0x0098090e, TabAdvanced, GroupWhiteBalance}, // WB temperature
		{0x009a0901, TabImage, GroupExposure},        // Auto exposure
		{0x009a0902, TabImage, GroupExposure},        // Exposure absolute
		{0x009a090a, TabImage, GroupFocus},           // Focus absolute
		{0x009a0908, TabPTZ, GroupPTZ},               // Pan absolute
		{0x009a0909, TabPTZ, GroupPTZ},               // Tilt absolute
		{0x009a090d, TabPTZ, GroupPTZ},               // Zoom absolute
	}

	for _, tc := range cases {
		got := Classify(domain.ControlDescriptor{ID: tc.id, ClassName: "User Controls"})
		if got.Tab != tc.tab || got.Group != tc.group {
			t.Fatalf("0x%08x: got %v/%q, want %v/%q", tc.id, got.Tab, got.Group, tc.tab, tc.group)
		}
	}
}

func TestClassifyUnknownFallsToAdvanced(t *testing.T) {
	got := Classify(domain.ControlDescriptor{
		ID:        0x0199e201, // vendor class control
		ClassID:   0x01990001,
		ClassName: "UVC Vendor",
	})

	if got.Tab != TabAdvanced {
		t.Fatalf("unknown control must land in Advanced, got %v", got.Tab)
	}

	if got.Group != "UVC Vendor" {
		t.Fatalf("advanced group should come from the control class, got %q", got.Group)
	}

	// Without class metadata the fallback group is not empty.
	got = Classify(domain.ControlDescriptor{ID: 0xdeadbeef})
	if got.Group == "" {
		t.Fatal("group must never be empty")
	}
}

func TestNormalizeNumeric(t *testing.T) {
	cases := []struct {
		name            string
		v, min, max, st int64
		want            int64
		wantErr         bool
	}{
		{"in range", 6, -64, 64, 1, 6, false},
		{"clamps low", -100, -64, 64, 1, -64, false},
		{"clamps high", 100, -64, 64, 1, 64, false},
		{"snaps down", 51, 0, 100, 10, 50, false},
		{"snaps up", 54, 0, 100, 10, 50, false},
		{"exact step", 60, 0, 100, 10, 60, false},
		{"no step", 33, 0, 100, 1, 33, false},
		{"zero step", 33, 0, 100, 0, 33, false},
		{"clamped then snapped", 105, 0, 100, 10, 100, false},
		{"snapped respects max", 99, 0, 95, 10, 90, false},
		{"invalid range", 1, 10, 0, 1, 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeNumeric(tc.v, tc.min, tc.max, tc.st)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %d", got)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestPracticalSlider(t *testing.T) {
	if !PracticalSlider(-64, 64) {
		t.Fatal("brightness range should be a slider")
	}

	if PracticalSlider(5, 5) {
		t.Fatal("empty range should not be a slider")
	}

	if PracticalSlider(0, 5_000_000_000) {
		t.Fatal("huge span should not be a slider")
	}
}
