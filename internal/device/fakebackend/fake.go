// Package fakebackend provides the scriptable in-memory device backend used
// as the CI truth for all device-facing logic: typed controls with flag and
// dependency semantics, write-error injection, capture-config EBUSY, lazy
// interval capabilities, driver-stride frame layouts and MPLANE mode.
package fakebackend

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"git.skobk.in/skobkin/v4lens/internal/device"
	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// ControlSpec declares one fake control.
type ControlSpec struct {
	ID      uint32
	Name    string
	Type    domain.ControlType
	Min     int64
	Max     int64
	Step    int64
	Default int64

	MenuItems []domain.MenuItem
	Flags     domain.ControlFlag
	Elems     int // 0 means scalar

	// Value / Str are the current values.
	Value int64
	Str   string

	// DependsOn + ActiveWhen model dependency INACTIVE: this control is
	// inactive while the master control's value differs from ActiveWhen.
	DependsOn  uint32
	ActiveWhen int64

	// WriteErr fails every write with this error (injection).
	WriteErr error

	// OnWrite runs after a successful write (UPDATE/dependency simulation).
	OnWrite func(b *Backend, id uint32)
}

// FormatSpec declares one fake pixel format with its size + interval
// capabilities and frame layout.
type FormatSpec struct {
	FourCC      domain.FOURCC
	Description string
	Sizes       []domain.FrameSize

	// Intervals maps "WxH" to the interval capability list (lazy data:
	// FrameIntervals looks entries up per concrete FOURCC+W+H).
	Intervals map[string][]domain.IntervalCap

	// BytesPerLine is the driver stride; larger than tightly-packed
	// simulates driver padding (decoders must honor it). 0 means
	// compressed (no stride reported).
	BytesPerLine func(w, h uint32) int

	// FrameBytes is the buffer size for one frame at WxH.
	FrameBytes func(w, h uint32) int
}

// Options builds a Backend.
type Options struct {
	Node string
	Card string

	Caps domain.CaptureCaps // zero value = single-planar capture

	Controls []ControlSpec
	Formats  []FormatSpec
	Inputs   []domain.InputInfo

	ActiveInput uint32
	Active      domain.CaptureConfig

	// TimePerFrame false = device without V4L2_CAP_TIMEPERFRAME (interval
	// stays nil and S_PARM-equivalent is skipped).
	TimePerFrame bool

	// CaptureBusy fails every capture-configuration mutation with this
	// error (nil = allowed).
	CaptureBusy error

	// StreamBusy fails OpenStream like an external owner would.
	StreamBusy bool

	// FrameInterval drives fake stream production (default 30 fps).
	FrameInterval time.Duration
}

// Backend is the fake device.Handle + CaptureConfigurer.
type Backend struct {
	opts Options

	mu        sync.Mutex
	controls  map[uint32]*ControlSpec
	streaming bool
	stream    *Stream
	seq       uint32
}

// New builds a backend from opts.
func New(opts Options) *Backend {
	b := &Backend{opts: opts, controls: map[uint32]*ControlSpec{}}

	for i := range opts.Controls {
		spec := opts.Controls[i]

		if spec.Elems == 0 {
			spec.Elems = 1
		}

		if spec.Step == 0 && spec.Type != domain.TypeButton {
			spec.Step = 1
		}

		b.controls[spec.ID] = &spec
	}

	return b
}

// Node returns the fake node path.
func (b *Backend) Node() string {
	return b.opts.Node
}

// Close is a no-op for the fake.
func (b *Backend) Close() error {
	b.StopStream()

	return nil
}

// DeviceCaps reports the configured capture capabilities.
func (b *Backend) DeviceCaps(_ context.Context) (domain.CaptureCaps, error) {
	return b.opts.Caps, nil
}

// EnumerateControls builds descriptors from the specs, resolving dependency
// INACTIVE flags live.
func (b *Backend) EnumerateControls(_ context.Context) ([]domain.ControlDescriptor, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]domain.ControlDescriptor, 0, len(b.controls))

	for _, spec := range b.controls {
		out = append(out, b.describe(spec))
	}

	return out, nil
}

func (b *Backend) describe(spec *ControlSpec) domain.ControlDescriptor {
	d := domain.ControlDescriptor{
		ID:        spec.ID,
		Name:      spec.Name,
		Type:      spec.Type,
		Min:       spec.Min,
		Max:       spec.Max,
		Step:      spec.Step,
		Default:   spec.Default,
		MenuItems: spec.MenuItems,
		ElemSize:  elemSizeOf(spec.Type),
		Elems:     spec.Elems,
		Flags:     spec.Flags,
		RawFlags:  uint32(spec.Flags),
	}

	d.CurFlags = d.Flags

	if spec.DependsOn != 0 {
		if master, ok := b.controls[spec.DependsOn]; ok && master.Value != spec.ActiveWhen {
			d.CurFlags |= domain.FlagInactive
		}
	}

	return d
}

func elemSizeOf(t domain.ControlType) int {
	switch t {
	case domain.TypeU8:

		return 1
	case domain.TypeU16:

		return 2
	case domain.TypeString:

		return 32
	default:

		return 4
	}
}

// ReadControls reads current values, refreshing dependency flags.
func (b *Backend) ReadControls(_ context.Context, ids []uint32) ([]domain.ControlValue, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]domain.ControlValue, 0, len(ids))

	for _, id := range ids {
		spec, ok := b.controls[id]
		if !ok {
			return nil, fmt.Errorf("fake: unknown control 0x%08x", id)
		}

		out = append(out, b.valueOf(spec))
	}

	return out, nil
}

func (b *Backend) valueOf(spec *ControlSpec) domain.ControlValue {
	switch spec.Type {
	case domain.TypeBoolean:

		return domain.NewBoolValue(spec.ID, spec.Value != 0)
	case domain.TypeString:

		return domain.NewStringValue(spec.ID, spec.Str)
	case domain.TypeButton:

		return domain.NewIntValue(spec.ID, spec.Type, 0)
	default:

		return domain.NewIntValue(spec.ID, spec.Type, spec.Value)
	}
}

// WriteControl applies fake kernel semantics: rejection when read-only or
// unsupported, clamping to min/max/step, dependency flags refresh, injected
// errors, and the truthful WriteResult (Effective nil for WRITE_ONLY).
func (b *Backend) WriteControl(_ context.Context, id uint32, value domain.ControlValue) (domain.WriteResult, error) {
	b.mu.Lock()

	spec, ok := b.controls[id]

	if !ok {
		b.mu.Unlock()

		return domain.WriteResult{}, fmt.Errorf("fake: unknown control 0x%08x", id)
	}

	if spec.Type == domain.TypeUnsupported || spec.Elems > 1 {
		b.mu.Unlock()

		return domain.WriteResult{}, domain.ErrUnsupported
	}

	if spec.Flags.Has(domain.FlagReadOnly) {
		b.mu.Unlock()

		return domain.WriteResult{}, fmt.Errorf("fake: control 0x%08x is read-only", id)
	}

	if spec.WriteErr != nil {
		b.mu.Unlock()

		return domain.WriteResult{}, spec.WriteErr
	}

	// dependency-inactive controls reject writes like the kernel does for
	// drivers that mark them inactive (EINVAL-shaped)
	if spec.DependsOn != 0 {
		if master, ok := b.controls[spec.DependsOn]; ok && master.Value != spec.ActiveWhen {
			b.mu.Unlock()

			return domain.WriteResult{}, fmt.Errorf("fake: control 0x%08x is inactive", id)
		}
	}

	switch spec.Type {
	case domain.TypeBoolean:
		v := int64(0)

		if value.Bool {
			v = 1
		}

		spec.Value = v
	case domain.TypeString:
		spec.Str = value.Str
	case domain.TypeButton:
		// buttons take no value
	default:
		spec.Value = clamp(value.Int, spec.Min, spec.Max, spec.Step)
	}

	res := domain.WriteResult{Accepted: true}

	if !spec.Flags.Has(domain.FlagWriteOnly) {
		eff := b.valueOf(spec)
		res.Effective = &eff
	}

	hook := spec.OnWrite

	b.mu.Unlock()

	if hook != nil {
		hook(b, id)
	}

	return res, nil
}

func clamp(v, min, max, step int64) int64 {
	if v < min {
		v = min
	}

	if v > max {
		v = max
	}

	if step > 1 {
		if r := (v - min) % step; r != 0 {
			v -= r
		}
	}

	return v
}

// ListInputs returns the configured inputs.
func (b *Backend) ListInputs(_ context.Context) ([]domain.InputInfo, error) {
	return append([]domain.InputInfo(nil), b.opts.Inputs...), nil
}

// FormatSizes returns the configured format capabilities.
func (b *Backend) FormatSizes(_ context.Context) ([]domain.FormatDesc, error) {
	out := make([]domain.FormatDesc, 0, len(b.opts.Formats))

	for _, f := range b.opts.Formats {
		out = append(out, domain.FormatDesc{FourCC: f.FourCC, Description: f.Description, Sizes: f.Sizes})
	}

	return out, nil
}

// FrameIntervals looks up the lazy interval data for a concrete FOURCC+W+H.
func (b *Backend) FrameIntervals(_ context.Context, cc domain.FOURCC, w, h uint32) ([]domain.IntervalCap, error) {
	for _, f := range b.opts.Formats {
		if f.FourCC != cc {
			continue
		}

		items, ok := f.Intervals[sizeKey(w, h)]
		if !ok {
			return nil, fmt.Errorf("fake: no intervals for %s %dx%d", cc, w, h)
		}

		return items, nil
	}

	return nil, fmt.Errorf("fake: unknown format %s", cc)
}

func sizeKey(w, h uint32) string {
	return fmt.Sprintf("%dx%d", w, h)
}

// ActiveCaptureConfig returns the effective state; Interval is nil unless
// the fake advertises timeperframe support (a real G_PARM always reports
// the current interval when the capability exists, so the fake defaults to
// 1/30 when none was configured).
func (b *Backend) ActiveCaptureConfig(_ context.Context) (domain.CaptureConfig, error) {
	cfg := b.opts.Active
	cfg.Input = b.opts.ActiveInput

	switch {
	case !b.opts.TimePerFrame:
		cfg.Interval = nil
	case cfg.Interval != nil:
		iv := *cfg.Interval
		cfg.Interval = &iv
	default:
		iv := domain.FrameInterval{Num: 1, Den: 30}
		cfg.Interval = &iv
	}

	return cfg, nil
}

// TryCaptureConfig validates without mutating: format must exist, the size
// must be a discrete match or within stepwise bounds.
func (b *Backend) TryCaptureConfig(_ context.Context, cfg domain.CaptureConfig) (domain.CaptureConfig, error) {
	spec, err := b.formatSpec(cfg.FourCC)
	if err != nil {
		return domain.CaptureConfig{}, err
	}

	normalized, ok := validateSize(spec, cfg.Width, cfg.Height)
	if !ok {
		return domain.CaptureConfig{}, fmt.Errorf("fake: %s does not support %dx%d", cfg.FourCC, cfg.Width, cfg.Height)
	}

	out := domain.CaptureConfig{
		Input:  cfg.Input,
		FourCC: cfg.FourCC,
		Width:  normalized[0],
		Height: normalized[1],
	}

	if cfg.Interval != nil && b.opts.TimePerFrame {
		iv := *cfg.Interval
		out.Interval = &iv
	}

	return out, nil
}

func (b *Backend) formatSpec(cc domain.FOURCC) (*FormatSpec, error) {
	for i := range b.opts.Formats {
		if b.opts.Formats[i].FourCC == cc {
			return &b.opts.Formats[i], nil
		}
	}

	return nil, fmt.Errorf("fake: unknown format %s", cc)
}

// validateSize resolves W/H against the format's sizes: exact discrete
// match, or stepwise clamping. Returns the validated pair.
func validateSize(spec *FormatSpec, w, h uint32) ([2]uint32, bool) {
	for _, s := range spec.Sizes {
		if s.Type == domain.FrameSizeDiscrete {
			if s.Width == w && s.Height == h {
				return [2]uint32{w, h}, true
			}

			continue
		}

		if w < s.MinW || w > s.MaxW || h < s.MinH || h > s.MaxH {
			continue
		}

		if s.StepW > 1 {
			if r := (w - s.MinW) % s.StepW; r != 0 {
				w -= r
			}
		}

		if s.StepH > 1 {
			if r := (h - s.MinH) % s.StepH; r != 0 {
				h -= r
			}
		}

		return [2]uint32{w, h}, true
	}

	return [2]uint32{w, h}, false
}

// ConfigureDetached performs the fake capture transaction; it fails with
// domain.ErrBusy when CaptureBusy is set or when the fake stream owns the
// device (like an external owner would).
func (b *Backend) ConfigureDetached(_ context.Context, cfg domain.CaptureConfig) (domain.CaptureConfig, error) {
	b.mu.Lock()
	streaming := b.streaming
	busy := b.opts.CaptureBusy
	b.mu.Unlock()

	if busy != nil {
		return domain.CaptureConfig{}, fmt.Errorf("%w: %w", domain.ErrBusy, busy)
	}

	if streaming {
		return domain.CaptureConfig{}, fmt.Errorf("%w: capture config while stream active", domain.ErrBusy)
	}

	if _, err := b.formatSpec(cfg.FourCC); err != nil {
		return domain.CaptureConfig{}, err
	}

	spec, _ := b.formatSpec(cfg.FourCC)

	normalized, ok := validateSize(spec, cfg.Width, cfg.Height)
	if !ok {
		return domain.CaptureConfig{}, fmt.Errorf("fake: %s does not support %dx%d", cfg.FourCC, cfg.Width, cfg.Height)
	}

	b.mu.Lock()

	b.opts.ActiveInput = cfg.Input
	b.opts.Active = domain.CaptureConfig{
		Input:  cfg.Input,
		FourCC: cfg.FourCC,
		Width:  normalized[0],
		Height: normalized[1],
	}

	if b.opts.TimePerFrame && cfg.Interval != nil && cfg.Interval.Valid() {
		iv := *cfg.Interval
		b.opts.Active.Interval = &iv
	} else {
		b.opts.Active.Interval = nil
	}

	effective := b.opts.Active
	effective.Input = b.opts.ActiveInput

	b.mu.Unlock()

	return effective, nil
}

// OpenStream starts the fake stream; MPLANE-only devices report
// ErrUnsupported and an external-owner StreamBusy reports ErrBusy.
func (b *Backend) OpenStream(_ context.Context, cfg device.StreamConfig) (device.Stream, error) {
	if b.opts.StreamBusy {
		return nil, fmt.Errorf("%w: fake external owner holds the stream", domain.ErrBusy)
	}

	if !b.opts.Caps.SinglePlanar {
		if b.opts.Caps.MPlane {
			return nil, fmt.Errorf("%w: MPLANE capture buffer type not supported for streaming", domain.ErrUnsupported)
		}

		return nil, fmt.Errorf("%w: no video-capture capability", domain.ErrUnsupported)
	}

	b.mu.Lock()

	if b.streaming {
		b.mu.Unlock()

		return nil, fmt.Errorf("%w: fake stream already active", domain.ErrBusy)
	}

	spec, err := b.formatSpec(cfg.Capture.FourCC)
	if err != nil {
		b.mu.Unlock()

		return nil, err
	}

	normalized, ok := validateSize(spec, cfg.Capture.Width, cfg.Capture.Height)
	if !ok {
		b.mu.Unlock()

		return nil, fmt.Errorf("fake: %s does not support %dx%d", cfg.Capture.FourCC, cfg.Capture.Width, cfg.Capture.Height)
	}

	// The fake applies the whole capture transaction at stream open.
	b.opts.ActiveInput = cfg.Capture.Input
	b.opts.Active = domain.CaptureConfig{
		Input:  cfg.Capture.Input,
		FourCC: cfg.Capture.FourCC,
		Width:  normalized[0],
		Height: normalized[1],
	}

	if b.opts.TimePerFrame && cfg.Capture.Interval != nil && cfg.Capture.Interval.Valid() {
		iv := *cfg.Capture.Interval
		b.opts.Active.Interval = &iv
	} else {
		b.opts.Active.Interval = nil
	}

	iv := 33 * time.Millisecond

	if b.opts.FrameInterval > 0 {
		iv = b.opts.FrameInterval
	}

	bpl := 0

	if spec.BytesPerLine != nil {
		bpl = spec.BytesPerLine(normalized[0], normalized[1])
	}

	frames := int(0)

	if spec.FrameBytes != nil {
		frames = spec.FrameBytes(normalized[0], normalized[1])
	}

	s := &Stream{
		frames: make(chan domain.Frame, 4),
		errCh:  make(chan error, 1),
		stopCh: make(chan struct{}),
		layout: domain.FrameLayout{
			BufferType:   domain.BufferTypeVideoCapture,
			FourCC:       cfg.Capture.FourCC,
			Width:        int(normalized[0]),
			Height:       int(normalized[1]),
			BytesPerLine: bpl,
			SizeImage:    frames,
		},
	}

	// Stream stop clears the ownership so detached configuration works again.
	s.onStop = func() {
		b.mu.Lock()

		if b.stream == s {
			b.streaming = false
			b.stream = nil
		}

		b.mu.Unlock()
	}

	b.streaming = true
	b.stream = s
	b.seq = 0

	go s.produce(iv, &b.seq, &b.mu, func() int { return b.frameBytesLocked(spec) })

	b.mu.Unlock()

	return s, nil
}

func (b *Backend) frameBytesLocked(spec *FormatSpec) int {
	w := b.opts.Active.Width
	h := b.opts.Active.Height

	if spec.FrameBytes != nil {
		return spec.FrameBytes(w, h)
	}

	return int(w) * int(h) * 2
}

// StopStream force-stops any fake stream (test helper and Close path).
func (b *Backend) StopStream() {
	b.mu.Lock()
	s := b.stream
	b.streaming = false
	b.stream = nil
	b.mu.Unlock()

	if s != nil {
		_ = s.Stop()
	}
}

// Streaming reports whether the fake stream is active.
func (b *Backend) Streaming() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.streaming
}

// Stream is the fake device.Stream producing synthetic frames honoring the
// configured driver stride.
type Stream struct {
	frames chan domain.Frame
	errCh  chan error
	layout domain.FrameLayout

	onStop   func()
	stopOnce sync.Once
	stopCh   chan struct{}
}

func (s *Stream) produce(interval time.Duration, seq *uint32, mu *sync.Mutex, bytes func() int) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			mu.Lock()
			n := *seq
			*seq++
			size := bytes()
			mu.Unlock()

			data := make([]byte, max(size, 1))

			for i := range data {
				data[i] = byte(n + uint32(i)) //nolint:gosec // deliberate wraparound pattern for synthetic frames
			}

			layout := s.layout
			layout.BytesUsed = len(data)

			select {
			case s.frames <- domain.Frame{Data: data, Layout: layout, Sequence: n}:
			case <-s.stopCh:

				return
			default:
				// full queue: drop
			}
		case <-s.stopCh:

			return
		}
	}
}

// Frames returns the fake frame channel.
func (s *Stream) Frames() <-chan domain.Frame {
	return s.frames
}

// Err returns the fake error channel.
func (s *Stream) Err() <-chan error {
	return s.errCh
}

// Stop ends production. Idempotent.
func (s *Stream) Stop() error {
	s.stopOnce.Do(func() {
		close(s.stopCh)
		close(s.frames)
		close(s.errCh)

		if s.onStop != nil {
			s.onStop()
		}
	})

	return nil
}

// FindControl returns the spec with the given id (test helper).
func (b *Backend) FindControl(id uint32) (ControlSpec, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	spec, ok := b.controls[id]

	if !ok {
		return ControlSpec{}, false
	}

	return *spec, true
}

// String renders the fake node for logs.
func (b *Backend) String() string {
	return strings.TrimSpace(b.opts.Node + " (fake)")
}
