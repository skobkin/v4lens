package viewmodel

import (
	"context"
	"testing"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

func testCaps() ([]domain.InputInfo, []domain.FormatDesc, domain.CaptureConfig) {
	inputs := []domain.InputInfo{
		{Index: 0, Name: "Camera"},
		{Index: 1, Name: "Component"},
	}

	formats := []domain.FormatDesc{
		{
			FourCC: "MJPG",
			Sizes: []domain.FrameSize{
				{Type: domain.FrameSizeDiscrete, Width: 1920, Height: 1080},
				{Type: domain.FrameSizeDiscrete, Width: 1280, Height: 720},
			},
		},
		{
			FourCC: "UYVY",
			Sizes: []domain.FrameSize{
				{Type: domain.FrameSizeDiscrete, Width: 640, Height: 480},
			},
		},
		{
			FourCC: "NV12",
			Sizes: []domain.FrameSize{
				{Type: domain.FrameSizeStepwise, MinW: 160, MaxW: 1920, StepW: 2, MinH: 120, MaxH: 1080, StepH: 2},
			},
		},
	}

	active := domain.CaptureConfig{
		Input: 0, FourCC: "MJPG", Width: 1920, Height: 1080,
		Interval: &domain.FrameInterval{Num: 1, Den: 60},
	}

	return inputs, formats, active
}

func TestFormatVMInitialStageMatchesActive(t *testing.T) {
	inputs, formats, active := testCaps()

	vm := NewFormatVM(inputs, formats, active, true, nil, nil)

	if vm.Dirty() {
		t.Fatal("fresh VM must not be dirty")
	}

	if got := vm.Staged(); !got.Equal(active) {
		t.Fatalf("staged = %+v, want active", got)
	}
}

func TestFormatVMCascadeSelectsValidCombos(t *testing.T) {
	inputs, formats, active := testCaps()

	vm := NewFormatVM(inputs, formats, active, true, nil, nil)

	// MJPG: two discrete sizes.
	if err := vm.SelectFormat("MJPG"); err != nil {
		t.Fatal(err)
	}

	choices := vm.SizeChoices()
	if len(choices) != 2 {
		t.Fatalf("size choices = %d, want 2", len(choices))
	}

	if err := vm.SelectSize(1280, 720); err != nil {
		t.Fatal(err)
	}

	if vm.Staged().Width != 1280 || vm.Staged().Height != 720 {
		t.Fatalf("staged size wrong: %+v", vm.Staged())
	}

	if !vm.Dirty() {
		t.Fatal("staged size differs from active -> dirty")
	}

	// A size the device never reported is rejected.
	if err := vm.SelectSize(100, 100); err == nil {
		t.Fatal("invalid size accepted")
	}

	// Switching formats resets to that format's first capability.
	if err := vm.SelectFormat("UYVY"); err != nil {
		t.Fatal(err)
	}

	if vm.Staged().Width != 640 || vm.Staged().Height != 480 {
		t.Fatalf("format switch must pick its first size: %+v", vm.Staged())
	}
}

func TestFormatVMStepwiseBounds(t *testing.T) {
	inputs, formats, active := testCaps()

	vm := NewFormatVM(inputs, formats, active, true, nil, nil)

	if err := vm.SelectFormat("NV12"); err != nil {
		t.Fatal(err)
	}

	// Current 1280x720 fits the stepwise bounds.
	if err := vm.SelectSize(1280, 720); err != nil {
		t.Fatal(err)
	}

	if vm.Staged().Width != 1280 || vm.Staged().Height != 720 {
		t.Fatalf("stepwise in-bounds selection lost: %+v", vm.Staged())
	}

	// Out-of-bounds rejected.
	if err := vm.SelectSize(9999, 720); err == nil {
		t.Fatal("out-of-bounds size accepted")
	}

	// Discrete sizes remain for discrete formats; NV12 has none.
	for _, s := range vm.SizeChoices() {
		if s.Type != domain.FrameSizeStepwise {
			t.Fatalf("unexpected choice %+v", s)
		}
	}
}

func TestFormatVMIntervalsLazyAndExact(t *testing.T) {
	inputs, formats, active := testCaps()

	var queried [3]any

	provider := func(_ context.Context, cc domain.FOURCC, w, h uint32) ([]domain.IntervalCap, error) {
		queried = [3]any{cc, w, h}

		return []domain.IntervalCap{
			{Type: domain.IntervalDiscrete, Min: domain.FrameInterval{Num: 1, Den: 60}},
			{Type: domain.IntervalDiscrete, Min: domain.FrameInterval{Num: 1001, Den: 60000}},
		}, nil
	}

	vm := NewFormatVM(inputs, formats, active, true, provider, nil)

	ivs, err := vm.Intervals(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(ivs) != 2 || queried[0] != domain.FOURCC("MJPG") || queried[1] != uint32(1920) {
		t.Fatalf("lazy query wrong: queried=%v ivs=%d", queried, len(ivs))
	}

	// Exact rational interval staging (never rounded).
	if err := vm.SelectInterval(domain.FrameInterval{Num: 1001, Den: 60000}); err != nil {
		t.Fatal(err)
	}

	if got := vm.Staged().Interval; got == nil || *got != (domain.FrameInterval{Num: 1001, Den: 60000}) {
		t.Fatalf("exact interval lost: %+v", got)
	}

	// 0/0 is never valid.
	if err := vm.SelectInterval(domain.FrameInterval{}); err == nil {
		t.Fatal("0/0 interval accepted")
	}
}

func TestFormatVMNoTimePerFrameRemovesIntervals(t *testing.T) {
	inputs, formats, active := testCaps()
	active.Interval = nil

	vm := NewFormatVM(inputs, formats, active, false, nil, nil)

	if _, err := vm.Intervals(context.Background()); err == nil {
		t.Fatal("interval query must fail without timeperframe support")
	}

	if err := vm.SelectInterval(domain.FrameInterval{Num: 1, Den: 30}); err == nil {
		t.Fatal("interval staging must fail without timeperframe support")
	}
}

func TestFormatVMStagedDifferentInputUncertain(t *testing.T) {
	inputs, formats, active := testCaps()

	vm := NewFormatVM(inputs, formats, active, true, nil, nil)

	vm.SelectInput(1)

	if !vm.Uncertain {
		t.Fatal("staged input != active input must be uncertain")
	}

	if vm.SizeChoices() != nil {
		t.Fatal("size choices are unknowable for a different input")
	}

	if _, err := vm.Intervals(context.Background()); err == nil {
		t.Fatal("interval query must refuse while uncertain")
	}

	// Format selection keeps the choice but invents no sizes.
	if err := vm.SelectFormat("UYVY"); err != nil {
		t.Fatal(err)
	}

	if vm.Staged().Width != 1920 || vm.Staged().Height != 1080 {
		t.Fatalf("uncertain staging must keep previous size: %+v", vm.Staged())
	}

	// Selecting the active input again resolves the uncertainty.
	vm.SelectInput(0)

	if vm.Uncertain {
		t.Fatal("staged input == active input must not be uncertain")
	}
}

func TestFormatVMResolvedNormalizesThroughDevice(t *testing.T) {
	inputs, formats, active := testCaps()

	validator := func(_ context.Context, cfg domain.CaptureConfig) (domain.CaptureConfig, error) {
		out := cfg
		out.Width = 1920 // pretend the driver rounds up to its max
		out.Height = 1080

		return out, nil
	}

	vm := NewFormatVM(inputs, formats, active, true, nil, validator)

	if err := vm.SelectFormat("NV12"); err != nil {
		t.Fatal(err)
	}

	if err := vm.SelectSize(500, 500); err != nil {
		t.Fatal(err)
	}

	got, err := vm.Resolved(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if got.Width != 1920 || got.Height != 1080 {
		t.Fatalf("resolved = %dx%d, want device-normalized 1920x1080", got.Width, got.Height)
	}
}

func TestFPSLabel(t *testing.T) {
	if got := FPSLabel(domain.FrameInterval{Num: 1, Den: 60}); got != "60 fps" {
		t.Fatalf("60 fps label = %q", got)
	}

	if got := FPSLabel(domain.FrameInterval{Num: 1001, Den: 60000}); got != "59.94 fps" {
		t.Fatalf("59.94 label = %q", got)
	}

	if got := FPSLabel(domain.FrameInterval{}); got != "?" {
		t.Fatalf("0/0 label = %q", got)
	}
}
