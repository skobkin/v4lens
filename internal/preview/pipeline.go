package preview

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"git.skobk.in/skobkin/v4lens/internal/device"
	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// State is the truthful preview lifecycle state.
type State int8

const (
	// StateStopped means no preview (initial or after Stop).
	StateStopped State = iota
	// StateStreaming means frames are decoding and rendering.
	StateStreaming
	// StateFrozen means our own stream was suspended for a format
	// reconfiguration; the last displayed frame is retained.
	StateFrozen
	// StateUnsupported means the capture config cannot be previewed
	// (no decoder, or a capture buffer type the MVP cannot stream).
	StateUnsupported
	// StateBusy means the stream could not be opened because an external
	// owner holds the device.
	StateBusy
	// StateError means preview failed (stream or decode errors).
	StateError
)

// String renders the state for logs and UI.
func (s State) String() string {
	switch s {
	case StateStreaming:

		return "Streaming"
	case StateFrozen:

		return "Frozen"
	case StateUnsupported:

		return "Unsupported"
	case StateBusy:

		return "Busy"
	case StateError:

		return "Error"
	default:

		return "Stopped"
	}
}

// Event is one preview update for the UI. Img is the latest decoded image
// (nil unless Streaming/Frozen). Published pixels are immutable until the
// 3-slot ring reuses their slot — three publications later; receivers must
// re-load the newest event every render tick and never write into images.
type Event struct {
	Key    string
	State  State
	Detail string // human-readable detail (unavailable FOURCC, error text)

	Img    image.Image
	Seq    uint64
	Layout domain.FrameLayout
}

// Opener opens one device stream (wired to the app layer, which resolves
// the key to its Handle).
type Opener func(ctx context.Context, key string, cfg device.StreamConfig) (device.Stream, error)

// DefaultRenderInterval is the UI render ceiling (~30 fps).
const DefaultRenderInterval = 33 * time.Millisecond

// pubFrame is one latest-slot publication.
type pubFrame struct {
	img    image.Image
	seq    uint64
	layout domain.FrameLayout
}

// Pipeline runs the streaming lane for one device: capture loop (prompt
// drain + decode) → bounded 3-slot publication ring → latest-slot atomic
// swap → render ticker ≤30 fps → events. Format reconfiguration suspends
// (Frozen, stream fd released) and resumes the stream — the concrete
// implementation behind the app-layer StreamOwner contract.
type Pipeline struct {
	key         string
	open        Opener
	log         *slog.Logger
	renderEvery time.Duration

	events chan Event

	mu      sync.Mutex
	stream  device.Stream
	genStop chan struct{}
	state   State
	detail  string

	// gen counts generations (bumped on start AND on suspend to invalidate
	// the previous capture/render loops); active says one is up.
	gen    int
	active bool

	latest atomic.Pointer[pubFrame]

	stopCh  chan struct{}
	stopOne sync.Once
	wg      sync.WaitGroup
}

// NewPipeline builds a pipeline for one device key.
func NewPipeline(key string, open Opener, log *slog.Logger) *Pipeline {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Pipeline{
		key:         key,
		open:        open,
		log:         log,
		renderEvery: DefaultRenderInterval,
		events:      make(chan Event, 8),
		stopCh:      make(chan struct{}),
	}
}

// Events returns the bounded event stream (drop-oldest under pressure).
func (p *Pipeline) Events() <-chan Event {
	return p.events
}

// State returns the current state and detail.
func (p *Pipeline) State() (State, string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.state, p.detail
}

func (p *Pipeline) setStateLocked(state State, detail string) {
	p.state = state
	p.detail = detail
}

// emit publishes an event without ever blocking the producer.
func (p *Pipeline) emit(ev Event) {
	ev.Key = p.key

	select {
	case p.events <- ev:
	default:
		// Drop the oldest event to make room; the render ticker republishes
		// the latest frame anyway.
		select {
		case <-p.events:
		default:
		}

		select {
		case p.events <- ev:
		default:
		}
	}
}

// Start begins previewing cfg. Unsupported FOURCCs never open a stream;
// the pipeline reports StateUnsupported honestly (controls keep working).
func (p *Pipeline) Start(ctx context.Context, cfg domain.CaptureConfig) error {
	if !Supported(cfg.FourCC) {
		p.mu.Lock()

		if p.active {
			p.mu.Unlock()

			return errors.New("preview already running")
		}

		p.setStateLocked(StateUnsupported, "Preview unavailable for "+string(cfg.FourCC)+" (no decoder)")
		detail := p.detail
		p.mu.Unlock()

		p.emit(Event{State: StateUnsupported, Detail: detail})

		return nil
	}

	return p.startGeneration(ctx, cfg)
}

// startGeneration opens the stream and launches capture + render loops
// for a new generation (initial start or post-suspend resume). Pipelines
// are single-use: after Stop, restart goes through a fresh Pipeline (the
// Manager owns that lifecycle).
func (p *Pipeline) startGeneration(ctx context.Context, cfg domain.CaptureConfig) error {
	p.mu.Lock()

	if p.active {
		p.mu.Unlock()

		return errors.New("preview already running")
	}

	select {
	case <-p.stopCh:
		p.mu.Unlock()

		return errors.New("preview stopped")
	default:
	}

	p.gen++
	gen := p.gen

	p.active = true

	stop := make(chan struct{})
	p.genStop = stop

	p.setStateLocked(StateStreaming, "")
	p.mu.Unlock()

	p.wg.Add(1)

	go func() {
		defer p.wg.Done()
		p.run(ctx, gen, stop, cfg)
	}()

	return nil
}

// run opens the stream and starts the loops for generation gen.
func (p *Pipeline) run(ctx context.Context, gen int, stop chan struct{}, cfg domain.CaptureConfig) {
	st, err := p.open(ctx, p.key, device.StreamConfig{Capture: cfg, BufferCount: 4})
	if err != nil {
		var state State

		switch {
		case errors.Is(err, domain.ErrBusy):
			state = StateBusy
		case errors.Is(err, domain.ErrUnsupported):
			state = StateUnsupported
		default:
			state = StateError
		}

		p.mu.Lock()
		p.setStateLocked(state, err.Error())
		detail := p.detail
		p.mu.Unlock()

		p.emit(Event{State: state, Detail: detail})

		return
	}

	p.mu.Lock()

	select {
	case <-stop: // suspended/stopped while the fd was being set up
		p.mu.Unlock()

		_ = st.Stop()

		return
	default:
	}

	p.stream = st
	p.mu.Unlock()

	p.wg.Add(2)

	go func() {
		defer p.wg.Done()
		p.captureLoop(gen, stop, st)
	}()

	go func() {
		defer p.wg.Done()
		p.renderLoop(stop)
	}()
}

// captureLoop drains frames promptly (never blocking the driver buffer
// return), decodes into the bounded publication ring and stores the
// newest decoded frame in the latest slot.
func (p *Pipeline) captureLoop(gen int, stop chan struct{}, st device.Stream) {
	var (
		slots [3]image.Image
		next  int
		seq   uint64
	)

	suspended := func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()

		return gen != p.gen
	}

	for {
		select {
		case <-stop:

			return
		case <-p.stopCh:

			return
		case err, ok := <-st.Err():
			if ok && err != nil {
				p.mu.Lock()
				p.setStateLocked(StateError, err.Error())
				p.mu.Unlock()

				p.emit(Event{State: StateError, Detail: err.Error(), Img: p.currentImage(), Seq: seq})
			}
		case f, ok := <-st.Frames():
			if !ok {
				// Stream closed: device gone, suspended or stopped.
				if !suspended() && !p.isStopped() {
					p.mu.Lock()
					p.setStateLocked(StateError, "stream ended unexpectedly")
					p.mu.Unlock()

					p.emit(Event{State: StateError, Detail: "stream ended", Seq: seq})
				}

				return
			}

			if suspended() {
				return
			}

			img, derr := p.decodeFrame(f, &slots, &next, &seq)
			if derr != nil {
				continue // state/detail already updated for the UI
			}

			seq++

			p.latest.Store(&pubFrame{img: img, seq: seq, layout: f.Layout})
		}
	}
}

// decodeFrame decodes one frame, reusing a publication slot when the
// decoder supports in-place refill (bounded at 3 buffers; a slot is only
// refilled three publications after it was published).
func (p *Pipeline) decodeFrame(f domain.Frame, slots *[3]image.Image, next *int, seq *uint64) (image.Image, error) {
	dec := For(f.Layout.FourCC)

	if dec == nil {
		p.mu.Lock()
		p.setStateLocked(StateUnsupported, "Preview unavailable for "+string(f.Layout.FourCC)+" (no decoder)")
		detail := p.detail
		p.mu.Unlock()

		p.emit(Event{State: StateUnsupported, Detail: detail, Seq: *seq})

		return nil, fmt.Errorf("no decoder for %s", f.Layout.FourCC)
	}

	if rd, ok := dec.(ReusableDecoder); ok {
		img, err := rd.DecodeInto(f.Data, f.Layout, slots[*next])
		if err != nil {
			return nil, err
		}

		slots[*next] = img
		*next = (*next + 1) % len(slots)

		return img, nil
	}

	// Fresh-image decoders (MJPEG): publication immutability is trivial.
	return dec.Decode(f.Data, f.Layout)
}

func (p *Pipeline) isStopped() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.state == StateStopped
}

// renderLoop republishes the newest frame at the render ceiling (~30 fps).
func (p *Pipeline) renderLoop(stop chan struct{}) {
	ticker := time.NewTicker(p.renderEvery)
	defer ticker.Stop()

	var lastSeq uint64

	for {
		select {
		case <-stop:

			return
		case <-p.stopCh:

			return
		case <-ticker.C:
			pf := p.latest.Load()

			if pf == nil || pf.seq == lastSeq {
				continue
			}

			lastSeq = pf.seq

			p.emit(Event{State: StateStreaming, Img: pf.img, Seq: pf.seq, Layout: pf.layout})
		}
	}
}

// SuspendForReconfigure freezes preview for a format transaction: the
// stream fd is released, the last displayed frame is retained (Frozen).
// The returned resume func reopens the stream with the new configuration.
func (p *Pipeline) SuspendForReconfigure(ctx context.Context, key string) (
	func(context.Context, domain.CaptureConfig) error, bool, error,
) {
	if key != p.key {
		return nil, false, nil
	}

	p.mu.Lock()

	if !p.active || p.stream == nil {
		p.mu.Unlock()

		return nil, false, nil
	}

	st := p.stream
	p.stream = nil

	oldStop := p.genStop
	p.genStop = nil

	p.gen++
	p.active = false

	p.setStateLocked(StateFrozen, "Reconfiguring format…")

	p.mu.Unlock()

	if oldStop != nil {
		close(oldStop)
	}

	// Release the stream fd: the whole capture transaction then runs on
	// the configuration fd (or the reopened stream fd on resume).
	_ = st.Stop()

	p.emit(Event{State: StateFrozen, Detail: "Reconfiguring format…", Img: p.currentImage()})

	resume := func(ctx context.Context, newCfg domain.CaptureConfig) error {
		return p.startGeneration(ctx, newCfg)
	}

	return resume, true, nil
}

// Stop tears the preview down. The final event carries StateStopped;
// after Stop the pipeline is single-use (restart via a fresh Pipeline).
func (p *Pipeline) Stop() {
	p.stopOne.Do(func() { close(p.stopCh) })

	p.mu.Lock()
	st := p.stream
	p.stream = nil
	stop := p.genStop
	p.genStop = nil

	p.active = false
	p.setStateLocked(StateStopped, "")
	p.mu.Unlock()

	if stop != nil {
		close(stop)
	}

	if st != nil {
		_ = st.Stop()
	}

	p.wg.Wait()

	p.emit(Event{State: StateStopped})
}

func (p *Pipeline) currentImage() image.Image {
	if pf := p.latest.Load(); pf != nil {
		return pf.img
	}

	return nil
}
