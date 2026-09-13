package viewmodel

import (
	"context"
	"fmt"
	"math"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// IntervalProvider lazily queries frame intervals for one concrete
// FOURCC+W+H (ENUM_FRAMEINTERVALS requires the concrete triple).
type IntervalProvider func(ctx context.Context, cc domain.FOURCC, w, h uint32) ([]domain.IntervalCap, error)

// SizeValidator resolves a staged config against the device (TRY_FMT
// path); used to normalize stepwise/continuous selections to concrete
// driver-validated values.
type SizeValidator func(ctx context.Context, cfg domain.CaptureConfig) (domain.CaptureConfig, error)

// FormatVM is the Fyne-free staged format-change model. Selections are
// capability-dependent: resolutions come from the selected format, frame
// rates load lazily for the concrete FOURCC+W+H, and a staged input that
// differs from the active one flags downstream choices as unvalidated
// (formats/sizes describe the CURRENTLY ACTIVE input only).
type FormatVM struct {
	Inputs  []domain.InputInfo
	Formats []domain.FormatDesc
	Active  domain.CaptureConfig

	// TimePerFrame reflects the device capability: false removes the frame
	// rate row entirely (an interval is never stored for such devices).
	TimePerFrame bool

	intervals IntervalProvider
	try       SizeValidator

	staged domain.CaptureConfig

	// Uncertain is true while the staged input differs from the active
	// one: downstream format/size/rate choices cannot be validated until
	// Apply, because capabilities were only enumerated for the active input.
	Uncertain bool
}

// NewFormatVM builds the model; intervals and try may be nil only in
// devices/tests that never query them.
func NewFormatVM(inputs []domain.InputInfo, formats []domain.FormatDesc, active domain.CaptureConfig, timePerFrame bool, intervals IntervalProvider, try SizeValidator) *FormatVM {
	vm := &FormatVM{
		Inputs:       inputs,
		Formats:      formats,
		Active:       active,
		TimePerFrame: timePerFrame,
		intervals:    intervals,
		try:          try,
	}

	vm.staged = active
	vm.staged.Input = active.Input

	return vm
}

// SetActive resets the model to a new effective device state (after an
// apply or a snapshot refresh); staged values return to the active ones.
func (vm *FormatVM) SetActive(active domain.CaptureConfig, inputs []domain.InputInfo, formats []domain.FormatDesc) {
	vm.Active = active
	vm.Inputs = inputs
	vm.Formats = formats
	vm.staged = active
	vm.staged.Input = active.Input
	vm.Uncertain = false
}

// Staged returns the current staged configuration.
func (vm *FormatVM) Staged() domain.CaptureConfig {
	return vm.staged
}

// Dirty reports whether staged values differ from the active ones.
func (vm *FormatVM) Dirty() bool {
	return !vm.staged.Equal(vm.Active)
}

// SelectInput stages an input. Selecting a different input than the active
// one marks all downstream choices unvalidated until Apply.
func (vm *FormatVM) SelectInput(idx uint32) {
	vm.staged.Input = idx
	vm.Uncertain = idx != vm.Active.Input
}

// SelectFormat stages a pixel format and resets staged size/interval to the
// first capability of that format (never a made-up combination).
func (vm *FormatVM) SelectFormat(cc domain.FOURCC) error {
	if vm.Uncertain {
		// With another input staged we cannot know valid sizes yet; keep
		// the choice but refuse to invent size/rate values.
		vm.staged.FourCC = cc

		return nil
	}

	spec := vm.formatOf(cc)
	if spec == nil {
		return fmt.Errorf("format %s was not reported by the device", cc)
	}

	vm.staged.FourCC = cc

	if len(spec.Sizes) == 0 {
		vm.staged.Width, vm.staged.Height = vm.Active.Width, vm.Active.Height

		return nil
	}

	vm.applySizeCap(spec.Sizes[0])

	return nil
}

func (vm *FormatVM) formatOf(cc domain.FOURCC) *domain.FormatDesc {
	for i := range vm.Formats {
		if vm.Formats[i].FourCC == cc {
			return &vm.Formats[i]
		}
	}

	return nil
}

func (vm *FormatVM) applySizeCap(s domain.FrameSize) {
	switch s.Type {
	case domain.FrameSizeDiscrete:
		vm.staged.Width, vm.staged.Height = s.Width, s.Height
	default:
		// Stepwise/continuous: stage the current value if it fits, else the
		// minimum; Apply normalizes through the device.
		vm.staged.Width = clampCap(vm.staged.Width, s.MinW, s.MaxW)
		vm.staged.Height = clampCap(vm.staged.Height, s.MinH, s.MaxH)
	}
}

func clampCap(v, min, max uint32) uint32 {
	if v < min || v > max {
		return min
	}

	return v
}

// SelectSize stages a concrete W/H. Discrete choices must be device-reported;
// stepwise/continuous bounds are checked here and normalized on Apply.
func (vm *FormatVM) SelectSize(w, h uint32) error {
	if vm.Uncertain {
		vm.staged.Width, vm.staged.Height = w, h

		return nil
	}

	spec := vm.formatOf(vm.staged.FourCC)
	if spec == nil {
		return fmt.Errorf("format %s was not reported by the device", vm.staged.FourCC)
	}

	for _, s := range spec.Sizes {
		switch s.Type {
		case domain.FrameSizeDiscrete:
			if s.Width == w && s.Height == h {
				vm.staged.Width, vm.staged.Height = w, h

				return nil
			}
		default:
			if w >= s.MinW && w <= s.MaxW && h >= s.MinH && h <= s.MaxH {
				vm.staged.Width, vm.staged.Height = w, h

				return nil
			}
		}
	}

	return fmt.Errorf("%dx%d is not a reported size for %s", w, h, vm.staged.FourCC)
}

// SizeChoices returns the selectable sizes for the staged format: discrete
// entries as-is, plus a bounded entry for stepwise/continuous formats.
func (vm *FormatVM) SizeChoices() []domain.FrameSize {
	if vm.Uncertain {
		return nil
	}

	spec := vm.formatOf(vm.staged.FourCC)
	if spec == nil {
		return nil
	}

	return spec.Sizes
}

// Intervals lazily queries the interval capabilities for the staged
// FOURCC+W+H. Returns a domain error-shaped failure when the query is not
// possible (e.g. unvalidated staged input, no timeperframe support).
func (vm *FormatVM) Intervals(ctx context.Context) ([]domain.IntervalCap, error) {
	if !vm.TimePerFrame {
		return nil, fmt.Errorf("device does not support configurable frame intervals")
	}

	if vm.Uncertain {
		return nil, fmt.Errorf("select Apply to validate staged input first")
	}

	if vm.intervals == nil {
		return nil, fmt.Errorf("interval query unavailable")
	}

	return vm.intervals(ctx, vm.staged.FourCC, vm.staged.Width, vm.staged.Height)
}

// SelectInterval stages an exact rational interval. 0/0 is never accepted.
func (vm *FormatVM) SelectInterval(iv domain.FrameInterval) error {
	if !vm.TimePerFrame {
		return fmt.Errorf("device does not support configurable frame intervals")
	}

	if !iv.Valid() {
		return fmt.Errorf("invalid frame interval %d/%d", iv.Num, iv.Den)
	}

	ivCopy := iv
	vm.staged.Interval = &ivCopy

	return nil
}

// Resolved returns the staged config normalized through the device's
// TRY-shaped validator when one is available and the selection is
// validated; the returned config is what Apply should submit.
func (vm *FormatVM) Resolved(ctx context.Context) (domain.CaptureConfig, error) {
	if vm.Uncertain || vm.try == nil {
		return vm.staged, nil
	}

	resolved, err := vm.try(ctx, vm.staged)
	if err != nil {
		return domain.CaptureConfig{}, err
	}

	return resolved, nil
}

// FPSLabel renders an interval as the conventional derived rate, e.g.
// "60 fps" or "59.94 fps" (storage stays exact).
func FPSLabel(iv domain.FrameInterval) string {
	if iv.Num == 0 || iv.Den == 0 {
		return "?"
	}

	fps := float64(iv.Den) / float64(iv.Num)

	if fps == math.Trunc(fps) {
		return fmt.Sprintf("%.0f fps", fps)
	}

	return fmt.Sprintf("%.2f fps", fps)
}
