package v4l

import (
	"context"
	"errors"
	"fmt"
	"sync"

	go4device "github.com/vladimirvivien/go4vl/device"
	"golang.org/x/sys/unix"

	"git.skobk.in/skobkin/v4lens/internal/device"
	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// streamFrameQueue bounds the Stream's frame channel. The capture loop
// never blocks on a slow consumer: on a full queue the incoming frame is
// dropped and the driver buffer was already released (prompt MMAP return).
const streamFrameQueue = 4

// Stream is a V4Lens-owned MMAP stream via go4vl. Frames carry V4Lens-owned
// copies of the pixels plus the effective FrameLayout; the driver buffer is
// released immediately after the copy (copy-then-Release, D013).
type Stream struct {
	dev    *go4device.Device
	frames chan domain.Frame
	errCh  chan error
	layout domain.FrameLayout

	ctx      context.Context
	cancel   context.CancelFunc
	doneOnce sync.Once
	doneCh   <-chan struct{}
	wg       sync.WaitGroup

	stopMu  sync.Mutex
	stopped bool
	stopErr error
}

// OpenStream opens the streaming lane: the full capture transaction runs on
// a short-lived setup fd (closing releases ownership), then a plain go4vl
// open inherits the effective format and starts MMAP streaming. The control
// fd is never used for capture configuration.
func (h *Handle) OpenStream(ctx context.Context, cfg device.StreamConfig) (device.Stream, error) {
	caps, err := h.DeviceCaps(ctx)
	if err != nil {
		return nil, err
	}

	if !caps.SinglePlanar {
		if caps.MPlane {
			return nil, fmt.Errorf("%w: MPLANE capture buffer type not supported for streaming; controls and profiles keep working", domain.ErrUnsupported)
		}

		return nil, fmt.Errorf("%w: node has no video-capture capability", domain.ErrUnsupported)
	}

	// Full S_INPUT/S_FMT/S_PARM transaction on a setup fd that closes
	// immediately (Phase 0: a format applied before open is inherited by
	// the subsequently opened streaming fd, and a foreign S_FMT while a
	// stream is active gets EBUSY — external owners stay protected).
	effective, err := ConfigureDetached(h.node, cfg.Capture)
	if err != nil {
		return nil, fmt.Errorf("stream setup: %w", err)
	}

	bufCount := cfg.BufferCount

	if bufCount <= 0 {
		bufCount = 2
	}

	dev, err := go4device.Open(h.node, go4device.WithBufferSize(uint32(bufCount)))
	if err != nil {
		return nil, fmt.Errorf("stream open %s: %w", h.node, err)
	}

	pix, err := dev.GetPixFormat()
	if err != nil {
		_ = dev.Close()

		return nil, fmt.Errorf("stream verify G_FMT: %w", err)
	}

	// go4l2.FourCCType is a uint32 alias, so the comparison is direct.
	if pix.PixelFormat != FourCC(string(effective.FourCC)) ||
		pix.Width != effective.Width || pix.Height != effective.Height {
		_ = dev.Close()

		return nil, fmt.Errorf("stream open: driver negotiated %s %dx%d, expected %s %dx%d",
			FourCCString(pix.PixelFormat), pix.Width, pix.Height,
			effective.FourCC, effective.Width, effective.Height)
	}

	streamCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	s := &Stream{
		dev:    dev,
		frames: make(chan domain.Frame, streamFrameQueue),
		errCh:  make(chan error, 1),
		layout: layoutFromPix(PixFormat{
			Width:        pix.Width,
			Height:       pix.Height,
			FourCC:       FourCCString(pix.PixelFormat),
			BytesPerLine: pix.BytesPerLine,
			SizeImage:    pix.SizeImage,
		}, caps),
		ctx:    streamCtx,
		cancel: cancel,
	}

	if err := dev.Start(streamCtx); err != nil {
		cancel()
		_ = dev.Close()

		return nil, fmt.Errorf("stream start: %w", err)
	}

	s.wg.Add(1)

	go s.consume()

	return s, nil
}

// consume copies frames (copy-then-Release) and publishes them with a
// bounded drop-when-full policy so the capture loop never blocks on the UI.
func (s *Stream) consume() {
	defer s.wg.Done()

	// Sender-owned channel closure: consume is the only sender on both
	// channels, so closing them here (never in Stop) cannot race a send.
	defer close(s.errCh)
	defer close(s.frames)

	frames := s.dev.GetFrames()
	errs := s.dev.GetError()

	for {
		select {
		case fr, ok := <-frames:
			if !ok {
				return
			}

			layout := s.layout
			layout.BytesUsed = len(fr.Data)

			data := make([]byte, len(fr.Data))
			copy(data, fr.Data)

			seq := fr.Sequence
			fr.Release()

			select {
			case s.frames <- domain.Frame{Data: data, Layout: layout, Sequence: seq}:
			default:
				// Consumer is behind; drop this frame, keep buffers flowing.
			}
		case err, ok := <-errs:
			if !ok {
				return
			}

			s.publishErr(err)

			return
		case <-s.done():
			return
		}
	}
}

// done lazily exposes the stream context cancellation as a channel.
func (s *Stream) done() <-chan struct{} {
	s.doneOnce.Do(func() {
		s.doneCh = s.ctx.Done()
	})

	return s.doneCh
}

func (s *Stream) publishErr(err error) {
	select {
	case s.errCh <- classifyStreamErr(err):
	default:
	}
}

// Frames returns the bounded frame channel.
func (s *Stream) Frames() <-chan domain.Frame {
	return s.frames
}

// Err returns the error channel (first error wins; closed when the
// stream's consume loop ends, including on Stop).
func (s *Stream) Err() <-chan error {
	return s.errCh
}

// Stop ends streaming and releases the go4vl device. Idempotent.
func (s *Stream) Stop() error {
	s.stopMu.Lock()
	defer s.stopMu.Unlock()

	if s.stopped {
		return s.stopErr
	}

	s.stopped = true

	s.cancel()

	s.stopErr = s.dev.Stop()

	if err := s.dev.Close(); err != nil && s.stopErr == nil {
		s.stopErr = fmt.Errorf("stream close: %w", err)
	}

	// consume exits on the cancelled context (or closed driver channels)
	// and closes the frame/error channels itself; wait so no send is still
	// in flight when Stop returns.
	s.wg.Wait()

	return s.stopErr
}

// classifyStreamErr maps driver errors to the domain sentinels.
func classifyStreamErr(err error) error {
	if errors.Is(err, unix.ENODEV) {
		return fmt.Errorf("%w: %w", domain.ErrDisconnected, err)
	}

	return err
}

// layoutFromPix builds the effective FrameLayout from the driver-reported
// pix format — never from recomputed assumptions (D013). BytesPerLine is 0
// for compressed formats (the driver reports no stride); decoders consume
// the layout as-is.
func layoutFromPix(pix PixFormat, caps domain.CaptureCaps) domain.FrameLayout {
	bufType := domain.BufferTypeOther

	switch {
	case caps.SinglePlanar:
		bufType = domain.BufferTypeVideoCapture
	case caps.MPlane:
		bufType = domain.BufferTypeVideoCaptureMPlane
	}

	return domain.FrameLayout{
		BufferType:   bufType,
		FourCC:       domain.FOURCC(pix.FourCC),
		Width:        int(pix.Width),
		Height:       int(pix.Height),
		BytesPerLine: int(pix.BytesPerLine),
		SizeImage:    int(pix.SizeImage),
	}
}
