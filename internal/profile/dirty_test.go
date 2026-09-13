package profile

import (
	"strings"
	"testing"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

func TestDirtyNoDiffWhenMatching(t *testing.T) {
	p := testProfile("P")

	if got := Dirty(p, matchingSnapshot(p)); len(got) != 0 {
		t.Fatalf("expected no diffs, got %v", got)
	}
}

// matchingSnapshot builds the snapshot that exactly matches testProfile.
func matchingSnapshot(p *Profile) DeviceSnapshot {
	snap := DeviceSnapshot{Capture: *p.Capture, Controls: map[uint32]domain.ControlValue{}}

	for _, s := range p.Controls {
		snap.Controls[s.ID] = s.Value
	}

	return snap
}

func TestDirtyControlValueAndMissing(t *testing.T) {
	p := testProfile("P")
	snap := matchingSnapshot(p)

	// Brightness drifted 0 -> 12; Saturation vanished from the device.
	snap.Controls[0x00980900] = domain.NewIntValue(0x00980900, domain.TypeInteger, 12)
	delete(snap.Controls, 0x00980902)

	got := Dirty(p, snap)

	if len(got) != 2 {
		t.Fatalf("expected 2 diffs, got %v", got)
	}

	if got[0].ID != 0x00980900 || !strings.Contains(got[0].Detail, "12") {
		t.Fatalf("value diff wrong: %+v", got[0])
	}

	if got[1].ID != 0x00980902 || !strings.Contains(got[1].Detail, "not present") {
		t.Fatalf("missing diff wrong: %+v", got[1])
	}
}

func TestDirtyCaptureFields(t *testing.T) {
	p := testProfile("P") // MJPG 1920x1080 input 1, interval 1001/60000

	// Format drifted.
	snap := matchingSnapshot(p)
	snap.Capture.FourCC = "UYVY"

	got := Dirty(p, snap)

	if len(got) != 1 || got[0].Kind != DiffCapture || !strings.Contains(got[0].Detail, "format") {
		t.Fatalf("format diff wrong: %v", got)
	}

	// Interval drifted (both sides present).
	snap = matchingSnapshot(p)
	iv := domain.FrameInterval{Num: 1, Den: 30}
	snap.Capture.Interval = &iv

	got = Dirty(p, snap)

	if len(got) != 1 || !strings.Contains(got[0].Detail, "interval") {
		t.Fatalf("interval diff wrong: %v", got)
	}

	// Profile manages an interval the device can no longer honor.
	snap = matchingSnapshot(p)
	snap.Capture.Interval = nil

	got = Dirty(p, snap)

	if len(got) != 1 || !strings.Contains(got[0].Detail, "none") {
		t.Fatalf("capability-lost diff wrong: %v", got)
	}

	// A profile WITHOUT a managed interval is never dirty on the device's
	// interval alone (not profile-managed).
	p2 := testProfile("Q")
	p2.Capture.Interval = nil

	snap = matchingSnapshot(p2)
	snap.Capture.Interval = &iv

	if got := Dirty(p2, snap); len(got) != 0 {
		t.Fatalf("unmanaged interval must not dirty: %v", got)
	}
}

func TestDirtyIgnoresUnmanagedControls(t *testing.T) {
	p := testProfile("P")
	snap := matchingSnapshot(p)

	// A control NOT in the profile can drift freely.
	snap.Controls[0x0199e201] = domain.NewIntValue(0x0199e201, domain.TypeInteger, 999)

	if got := Dirty(p, snap); len(got) != 0 {
		t.Fatalf("unmanaged control leaked into diff: %v", got)
	}
}
