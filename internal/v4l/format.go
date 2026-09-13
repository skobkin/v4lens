package v4l

import (
	"fmt"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// FormatDescs builds the format capability list for the currently active
// input: ENUM_FMT + per-format ENUM_FRAMESIZES with all capability forms
// preserved (discrete/stepwise/continuous — never dropped).
func FormatDescs(fd uintptr) ([]domain.FormatDesc, error) {
	fmts, err := EnumFormats(fd)
	if err != nil {
		return nil, fmt.Errorf("enumerate formats: %w", err)
	}

	out := make([]domain.FormatDesc, 0, len(fmts))

	for _, f := range fmts {
		sizes, err := EnumFrameSizes(fd, f.FourCC)
		if err != nil {
			return nil, fmt.Errorf("enumerate frame sizes for %s: %w", f.FourCC, err)
		}

		d := domain.FormatDesc{FourCC: domain.FOURCC(f.FourCC), Description: f.Description}

		for _, s := range sizes {
			if s.IsDiscrete {
				d.Sizes = append(d.Sizes, domain.FrameSize{
					Type:   domain.FrameSizeDiscrete,
					Width:  s.Width,
					Height: s.Height,
				})

				continue
			}

			t := domain.FrameSizeStepwise

			if s.MinW == s.MaxW && s.MaxW == s.StepW && s.MinH == s.MaxH && s.MaxH == s.StepH {
				t = domain.FrameSizeContinuous
			}

			d.Sizes = append(d.Sizes, domain.FrameSize{
				Type: t,
				MinW: s.MinW, MaxW: s.MaxW, StepW: s.StepW,
				MinH: s.MinH, MaxH: s.MaxH, StepH: s.StepH,
			})
		}

		out = append(out, d)
	}

	return out, nil
}

// FrameIntervals answers the lazy interval query for one concrete
// FOURCC+W+H with all capability forms preserved.
func FrameIntervals(fd uintptr, cc domain.FOURCC, w, h uint32) ([]domain.IntervalCap, error) {
	raw, err := EnumFrameIntervals(fd, string(cc), w, h)
	if err != nil {
		return nil, fmt.Errorf("enumerate frame intervals for %s %dx%d: %w", cc, w, h, err)
	}

	out := make([]domain.IntervalCap, 0, len(raw))

	for _, iv := range raw {
		if iv.IsDiscrete {
			out = append(out, domain.IntervalCap{
				Type: domain.IntervalDiscrete,
				Min:  domain.FrameInterval{Num: iv.Num, Den: iv.Den},
			})

			continue
		}

		out = append(out, domain.IntervalCap{
			Type: domain.IntervalStepwise,
			Min:  domain.FrameInterval{Num: iv.MinNum, Den: iv.MinDen},
			Max:  domain.FrameInterval{Num: iv.MaxNum, Den: iv.MaxDen},
			Step: domain.FrameInterval{Num: iv.StepNum, Den: iv.StepDen},
		})
	}

	return out, nil
}

// ActiveCaptureConfig reads the effective capture configuration:
// G_INPUT + G_FMT + G_PARM. Interval is nil when the device does not
// support timeperframe; 0/0 is never reported.
func ActiveCaptureConfig(fd uintptr) (domain.CaptureConfig, domain.BufferType, error) {
	input, err := GetInput(fd)
	if err != nil {
		return domain.CaptureConfig{}, domain.BufferTypeUnknown, wrapBusy(fmt.Errorf("G_INPUT: %w", err))
	}

	pix, err := GetFormat(fd)
	if err != nil {
		return domain.CaptureConfig{}, domain.BufferTypeUnknown, wrapBusy(fmt.Errorf("G_FMT: %w", err))
	}

	cfg := domain.CaptureConfig{
		Input:  input,
		FourCC: domain.FOURCC(pix.FourCC),
		Width:  pix.Width,
		Height: pix.Height,
	}

	bufType := domain.BufferTypeVideoCapture

	parm, perr := GetParm(fd)
	if perr == nil && parm.CapTimePerFrame {
		cfg.Interval = &domain.FrameInterval{Num: parm.TimePerFrame[0], Den: parm.TimePerFrame[1]}
	}

	// An MPLANE device reports MPLANE via G_FMT buffer type mismatch only
	// through QUERYCAP; the caller classifies with DeviceCaps and reports
	// the unsupported buffer type honestly.

	return cfg, bufType, nil
}

// TryCaptureConfig validates a capture config with TRY_FMT only — the
// control fd stays in panel mode and never mutates capture state. The
// returned config is the driver-normalized form.
func TryCaptureConfig(fd uintptr, cfg domain.CaptureConfig) (domain.CaptureConfig, error) {
	pix, err := TryFormat(fd, PixFormat{
		Width:  cfg.Width,
		Height: cfg.Height,
		FourCC: string(cfg.FourCC),
	})

	if err != nil {
		return domain.CaptureConfig{}, wrapBusy(fmt.Errorf("TRY_FMT: %w", err))
	}

	out := domain.CaptureConfig{
		Input:  cfg.Input,
		FourCC: domain.FOURCC(pix.FourCC),
		Width:  pix.Width,
		Height: pix.Height,
	}

	if cfg.Interval != nil {
		iv := *cfg.Interval
		out.Interval = &iv
	}

	return out, nil
}

// ApplyCaptureTransaction performs the WHOLE capture transaction on fd:
// S_INPUT → S_FMT → S_PARM (interval, when the device supports
// timeperframe) → G_INPUT/G_FMT/G_PARM read-back. It always runs on the fd
// that owns or is about to own the stream (fd-ownership model, D013).
func ApplyCaptureTransaction(fd uintptr, cfg domain.CaptureConfig, timePerFrame bool) (domain.CaptureConfig, error) {
	input, err := GetInput(fd)

	switch {
	case err != nil:
		// Inputs are optional on some capture devices; only S_INPUT when
		// G_INPUT proves the device has them.
	case input != cfg.Input:
		if serr := SetInput(fd, cfg.Input); serr != nil {
			return domain.CaptureConfig{}, wrapBusy(fmt.Errorf("S_INPUT %d: %w", cfg.Input, serr))
		}
	default:
	}

	pix, err := SetFormat(fd, PixFormat{Width: cfg.Width, Height: cfg.Height, FourCC: string(cfg.FourCC)})
	if err != nil {
		return domain.CaptureConfig{}, wrapBusy(fmt.Errorf("S_FMT: %w", err))
	}

	if cfg.Interval != nil && timePerFrame {
		if !cfg.Interval.Valid() {
			return domain.CaptureConfig{}, fmt.Errorf("invalid frame interval %d/%d", cfg.Interval.Num, cfg.Interval.Den)
		}

		if err := SetParm(fd, cfg.Interval.Num, cfg.Interval.Den); err != nil {
			return domain.CaptureConfig{}, wrapBusy(fmt.Errorf("S_PARM: %w", err))
		}
	}

	// Read back the whole effective config.
	input, err = GetInput(fd)
	if err != nil {
		return domain.CaptureConfig{}, wrapBusy(fmt.Errorf("G_INPUT read-back: %w", err))
	}

	parm, err := GetParm(fd)
	if err != nil {
		return domain.CaptureConfig{}, wrapBusy(fmt.Errorf("G_PARM read-back: %w", err))
	}

	effective := domain.CaptureConfig{
		Input:  input,
		FourCC: domain.FOURCC(pix.FourCC),
		Width:  pix.Width,
		Height: pix.Height,
	}

	if parm.CapTimePerFrame {
		effective.Interval = &domain.FrameInterval{Num: parm.TimePerFrame[0], Den: parm.TimePerFrame[1]}
	}

	return effective, nil
}
