package ui

import (
	"testing"

	"fyne.io/fyne/v2/container"

	"git.skobk.in/skobkin/v4lens/internal/domain"
	"git.skobk.in/skobkin/v4lens/internal/ui/viewmodel"
)

// The Input picker shows the plain input name; the numeric index is
// internal bookkeeping, not something the user picks by.
func TestInputLabelShowsNameOnly(t *testing.T) {
	if got := inputLabel(domain.InputInfo{Index: 0, Name: "Camera 1"}); got != "Camera 1" {
		t.Fatalf("inputLabel = %q, want the plain name", got)
	}

	// Nameless inputs still need a readable, pickable label.
	if got := inputLabel(domain.InputInfo{Index: 2}); got != "Input 2" {
		t.Fatalf("nameless inputLabel = %q", got)
	}
}

// The frame-rate picker must show the actual current frame rate once the
// lazy interval query completes — not its placeholder.
func TestFormatTabShowsActiveFrameRate(t *testing.T) {
	newTestEnv(t)

	q := dispatchQueue(make(chan func()))

	tab := &FormatTab{box: container.NewVBox(), Dispatch: q.dispatch}
	tab.Lookup = func() string { return "key" }
	tab.QueryIntervals = func(string, domain.FOURCC, uint32, uint32) ([]domain.IntervalCap, error) {
		return []domain.IntervalCap{
			{Type: domain.IntervalDiscrete, Discrete: []domain.FrameInterval{
				{Num: 1, Den: 30},
				{Num: 1, Den: 60},
			}},
		}, nil
	}

	tab.SetSnapshot(
		[]domain.InputInfo{{Index: 0, Name: "Camera 1"}},
		[]domain.FormatDesc{{FourCC: "UYVY", Sizes: []domain.FrameSize{
			{Type: domain.FrameSizeDiscrete, Width: 1280, Height: 720},
		}}},
		domain.CaptureConfig{
			FourCC: "UYVY", Width: 1280, Height: 720,
			Interval: &domain.FrameInterval{Num: 1, Den: 30},
		},
	)
	q.expect() // lazy interval load completion

	want := viewmodel.FPSLabel(domain.FrameInterval{Num: 1, Den: 30}) + "  (1/30)"

	if tab.fpsSel.Selected != want {
		t.Fatalf("rate picker selection = %q, want %q", tab.fpsSel.Selected, want)
	}

	// Staging an exact interval no option offers must not leave a stale
	// rate displayed — the placeholder returns.
	tab.fpsEntry.OnSubmitted("1001/60000")

	if tab.fpsSel.Selected != "" {
		t.Fatalf("rate picker selection = %q, want placeholder after staging an off-list interval", tab.fpsSel.Selected)
	}
}
