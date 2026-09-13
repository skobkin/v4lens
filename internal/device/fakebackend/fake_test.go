package fakebackend

import (
	"context"
	"errors"
	"testing"
	"time"

	"git.skobk.in/skobkin/v4lens/internal/device"
	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// StreamConfigOf wraps a capture config into a stream config.
func StreamConfigOf(cfg domain.CaptureConfig) device.StreamConfig {
	return device.StreamConfig{Capture: cfg}
}

const (
	cidWBMode    = uint32(0x0098090c)
	cidWBTemp    = uint32(0x0098091a)
	cidBright    = uint32(0x00980900)
	cidWriteOnly = uint32(0x00980999)
)

func testBackend(t *testing.T, mutate func(*Options)) *Backend {
	t.Helper()

	opts := Options{
		Node:         "/dev/fake-video0",
		Card:         "Synthetic Cam",
		Caps:         domain.CaptureCaps{SinglePlanar: true},
		TimePerFrame: true,
		ActiveInput:  0,
		Active:       domain.CaptureConfig{Input: 0, FourCC: "UYVY", Width: 1280, Height: 720},
		Formats: []FormatSpec{{
			FourCC:      "UYVY",
			Description: "Packed 4:2:2",
			Sizes: []domain.FrameSize{
				{Type: domain.FrameSizeDiscrete, Width: 640, Height: 480},
				{Type: domain.FrameSizeDiscrete, Width: 1280, Height: 720},
			},
			Intervals: map[string][]domain.IntervalCap{
				"1280x720": {
					{Type: domain.IntervalDiscrete, Min: domain.FrameInterval{Num: 1, Den: 60}},
					{Type: domain.IntervalDiscrete, Min: domain.FrameInterval{Num: 1, Den: 30}},
				},
			},
			BytesPerLine: func(w, _ uint32) int { return int(w) * 2 },
			FrameBytes:   func(w, h uint32) int { return int(w*h) * 2 },
		}},
	}

	if mutate != nil {
		mutate(&opts)
	}

	return New(opts)
}

func TestControlWriteClampAndReadBack(t *testing.T) {
	b := testBackend(t, func(o *Options) {
		o.Controls = append(o.Controls, ControlSpec{ID: cidBright, Name: "Brightness", Type: domain.TypeInteger, Min: 1, Max: 19, Step: 1, Default: 10, Value: 6})
	})

	ctx := context.Background()

	// Kernel clamps: writing above max is accepted and clamped, read-back
	// is the effective value.
	res, err := b.WriteControl(ctx, cidBright, domain.NewIntValue(cidBright, domain.TypeInteger, 500))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	if !res.Accepted || res.Effective == nil || res.Effective.Int != 19 {
		t.Fatalf("clamp result wrong: %+v", res)
	}
}

func TestControlWriteOnlyNoReadBack(t *testing.T) {
	b := testBackend(t, func(o *Options) {
		o.Controls = append(o.Controls, ControlSpec{
			ID: cidWriteOnly, Name: "WO", Type: domain.TypeInteger,
			Min: 0, Max: 10, Step: 1, Flags: domain.FlagWriteOnly,
		})
	})

	res, err := b.WriteControl(context.Background(), cidWriteOnly, domain.NewIntValue(cidWriteOnly, domain.TypeInteger, 5))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	// The requested value is NEVER substituted as a read-back.
	if !res.Accepted || res.Effective != nil {
		t.Fatalf("write-only result must have no Effective: %+v", res)
	}
}

func TestControlDependencyInactive(t *testing.T) {
	b := testBackend(t, func(o *Options) {
		o.Controls = append(o.Controls,
			ControlSpec{ID: cidWBMode, Name: "WB Auto", Type: domain.TypeBoolean, Value: 1},
			ControlSpec{ID: cidWBTemp, Name: "WB Temp", Type: domain.TypeInteger, Min: 2800, Max: 10000, Step: 1, DependsOn: cidWBMode, ActiveWhen: 0},
		)
	})

	ctx := context.Background()

	descs, err := b.EnumerateControls(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, d := range descs {
		if d.ID == cidWBTemp && !d.CurFlags.Has(domain.FlagInactive) {
			t.Fatal("WB temp must be inactive while auto is on")
		}
	}

	// Master off -> slave active, write succeeds and returns read-back.
	if _, err := b.WriteControl(ctx, cidWBMode, domain.NewBoolValue(cidWBMode, false)); err != nil {
		t.Fatalf("master write: %v", err)
	}

	res, err := b.WriteControl(ctx, cidWBTemp, domain.NewIntValue(cidWBTemp, domain.TypeInteger, 4500))
	if err != nil {
		t.Fatalf("slave write must succeed after master change: %v", err)
	}

	if res.Effective == nil || res.Effective.Int != 4500 {
		t.Fatalf("slave read-back wrong: %+v", res)
	}
}

func TestControlWriteErrorInjection(t *testing.T) {
	boom := errors.New("boom")

	b := testBackend(t, func(o *Options) {
		o.Controls = append(o.Controls, ControlSpec{ID: cidBright, Type: domain.TypeInteger, Min: 0, Max: 10, Step: 1, WriteErr: boom})
	})

	_, err := b.WriteControl(context.Background(), cidBright, domain.NewIntValue(cidBright, domain.TypeInteger, 3))
	if !errors.Is(err, boom) {
		t.Fatalf("injected error lost: %v", err)
	}
}

func TestCaptureConfigBusy(t *testing.T) {
	b := testBackend(t, func(o *Options) {
		o.CaptureBusy = errors.New("EBUSY")
	})

	_, err := b.ConfigureDetached(context.Background(), domain.CaptureConfig{FourCC: "UYVY", Width: 640, Height: 480})
	if !errors.Is(err, domain.ErrBusy) {
		t.Fatalf("capture busy must surface domain.ErrBusy: %v", err)
	}
}

func TestCaptureConfigValidatesAndReadsBack(t *testing.T) {
	b := testBackend(t, nil)

	ctx := context.Background()

	// Unknown format rejected.
	if _, err := b.ConfigureDetached(ctx, domain.CaptureConfig{FourCC: "XXXX", Width: 640, Height: 480}); err == nil {
		t.Fatal("unknown format must fail")
	}

	// Valid config: exact read-back incl. interval.
	cfg := domain.CaptureConfig{
		FourCC:   "UYVY",
		Width:    1280,
		Height:   720,
		Interval: &domain.FrameInterval{Num: 1, Den: 60},
	}

	eff, err := b.ConfigureDetached(ctx, cfg)
	if err != nil {
		t.Fatalf("configure: %v", err)
	}

	if !eff.Equal(cfg) {
		t.Fatalf("effective = %+v want %+v", eff, cfg)
	}

	got, err := b.ActiveCaptureConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if !got.Equal(eff) {
		t.Fatalf("active = %+v want %+v", got, eff)
	}
}

func TestTimePerFrameUnsupported(t *testing.T) {
	b := testBackend(t, func(o *Options) { o.TimePerFrame = false })

	eff, err := b.ConfigureDetached(context.Background(), domain.CaptureConfig{
		FourCC:   "UYVY",
		Width:    640,
		Height:   480,
		Interval: &domain.FrameInterval{Num: 1, Den: 60},
	})

	if err != nil {
		t.Fatalf("interval must be ignored, not rejected: %v", err)
	}

	if eff.Interval != nil {
		t.Fatalf("interval must stay nil without timeperframe: %+v", eff.Interval)
	}
}

func TestLazyIntervals(t *testing.T) {
	b := testBackend(t, nil)

	ctx := context.Background()

	ivs, err := b.FrameIntervals(ctx, "UYVY", 1280, 720)
	if err != nil {
		t.Fatalf("lazy intervals: %v", err)
	}

	if len(ivs) != 2 || ivs[0].Type != domain.IntervalDiscrete {
		t.Fatalf("intervals wrong: %+v", ivs)
	}

	// Query for an unlisted concrete size fails (like EINVAL).
	if _, err := b.FrameIntervals(ctx, "UYVY", 640, 480); err == nil {
		t.Fatal("missing interval data must fail")
	}
}

func TestMPlaneReported(t *testing.T) {
	b := testBackend(t, func(o *Options) {
		o.Caps = domain.CaptureCaps{MPlane: true}
	})

	// Controls keep working.
	if _, err := b.EnumerateControls(context.Background()); err != nil {
		t.Fatalf("controls must work on MPLANE devices: %v", err)
	}

	// Streaming refuses explicitly.
	_, err := b.OpenStream(context.Background(), StreamConfigOf(domain.CaptureConfig{FourCC: "UYVY", Width: 1280, Height: 720}))
	if !errors.Is(err, domain.ErrUnsupported) {
		t.Fatalf("MPLANE stream must be ErrUnsupported: %v", err)
	}
}

func TestStreamLifecycleAndStride(t *testing.T) {
	b := testBackend(t, nil)

	s, err := b.OpenStream(context.Background(), StreamConfigOf(domain.CaptureConfig{
		FourCC:   "UYVY",
		Width:    1280,
		Height:   720,
		Interval: &domain.FrameInterval{Num: 1, Den: 60},
	}))

	if err != nil {
		t.Fatalf("open stream: %v", err)
	}

	// ConfigureDetached now conflicts with the (own) stream.
	if _, err := b.ConfigureDetached(context.Background(), domain.CaptureConfig{FourCC: "UYVY", Width: 640, Height: 480}); !errors.Is(err, domain.ErrBusy) {
		t.Fatalf("own-stream conflict must be ErrBusy: %v", err)
	}

	// Frames arrive with the driver stride honored.
	select {
	case fr := <-s.Frames():
		if fr.Layout.BytesPerLine != 2560 {
			t.Fatalf("stride wrong: %d", fr.Layout.BytesPerLine)
		}

		if fr.Layout.Width != 1280 || fr.Layout.Height != 720 || fr.Layout.BytesUsed != len(fr.Data) {
			t.Fatalf("layout wrong: %+v", fr.Layout)
		}
	case <-time.After(time.Second):
		t.Fatal("no frame produced")
	}

	if err := s.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// Ownership released: detached config works again.
	if _, err := b.ConfigureDetached(context.Background(), domain.CaptureConfig{FourCC: "UYVY", Width: 640, Height: 480}); err != nil {
		t.Fatalf("after stop, detached config must work: %v", err)
	}
}

func TestStreamFramesAreOwnedCopies(t *testing.T) {
	b := testBackend(t, nil)

	s, err := b.OpenStream(context.Background(), StreamConfigOf(domain.CaptureConfig{FourCC: "UYVY", Width: 640, Height: 480}))
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = s.Stop() }()

	var last domain.Frame

	for range 3 {
		select {
		case fr := <-s.Frames():
			// Mutate the frame copy: the producer must never share memory.
			fr.Data[0] ^= 0xFF
			last = fr
		case <-time.After(time.Second):
			t.Fatal("frame timeout")
		}
	}

	if len(last.Data) == 0 {
		t.Fatal("no frames collected")
	}
}
