package preview

import (
	"context"
	"errors"
	"fmt"
	"image"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"git.skobk.in/skobkin/v4lens/internal/device"
	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// fakeStream is a scriptable device.Stream: frames are pushed by tests,
// Stop is observed via the atomic flag.
type fakeStream struct {
	frames  chan domain.Frame
	errCh   chan error
	stopped atomic.Bool
}

func newFakeStream() *fakeStream {
	return &fakeStream{
		frames: make(chan domain.Frame),
		errCh:  make(chan error, 4),
	}
}

func (s *fakeStream) Frames() <-chan domain.Frame { return s.frames }
func (s *fakeStream) Err() <-chan error           { return s.errCh }

func (s *fakeStream) Stop() error {
	s.stopped.Store(true)

	return nil
}

// sendFrame pushes one frame; it gives up after two seconds (the test
// will fail on its own timeout assertions if the frame never landed).
func (s *fakeStream) sendFrame(f domain.Frame) {
	select {
	case s.frames <- f:
	case <-time.After(2 * time.Second):
	}
}

// endStream closes the frame channel (device gone / driver stop).
func (s *fakeStream) endStream() { close(s.frames) }

// fakeOpener hands out fresh fake streams, or fails with err when set.
// Every successful open is announced on a channel so tests can wait for
// the (asynchronous) stream lane deterministically.
type fakeOpener struct {
	mu      sync.Mutex
	err     error
	streams []*fakeStream
	opened  chan struct{}
}

func newFakeOpener() *fakeOpener {
	return &fakeOpener{opened: make(chan struct{}, 16)}
}

func (o *fakeOpener) open(_ context.Context, _ string, _ device.StreamConfig) (device.Stream, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.err != nil {
		return nil, o.err
	}

	s := newFakeStream()
	o.streams = append(o.streams, s)

	o.opened <- struct{}{}

	return s, nil
}

// waitOpened blocks until one stream has been opened (or fails the test).
func (o *fakeOpener) waitOpened(t *testing.T) *fakeStream {
	t.Helper()

	select {
	case <-o.opened:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the stream to open")
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	return o.streams[len(o.streams)-1]
}

func (o *fakeOpener) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()

	return len(o.streams)
}

// uyvyFrame builds a small UYVY frame whose bytes vary by salt.
func uyvyFrame(w, h int, salt byte) domain.Frame {
	stride := w * 2

	data := make([]byte, stride*h)

	for i := range data {
		data[i] = salt + byte(i)
	}

	return domain.Frame{
		Data: data,
		Layout: domain.FrameLayout{
			BufferType:   domain.BufferTypeVideoCapture,
			FourCC:       "UYVY",
			Width:        w,
			Height:       h,
			BytesPerLine: stride,
			SizeImage:    stride * h,
		},
	}
}

// waitState drains events until one with the wanted state arrives.
func waitState(t *testing.T, p *Pipeline, want State) Event {
	t.Helper()

	deadline := time.After(3 * time.Second)

	for {
		select {
		case ev := <-p.Events():
			if ev.State == want {

				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for state %v", want)
		}
	}
}

// startYUYVPreview starts a pipeline over a UYVY 4x2 config and returns it
// plus the opened stream.
func startUYVYPipeline(t *testing.T) (*Pipeline, *fakeOpener, *fakeStream) {
	t.Helper()

	op := newFakeOpener()
	p := NewPipeline("dev-key", op.open, nil)
	p.renderEvery = 5 * time.Millisecond

	if err := p.Start(context.Background(), domain.CaptureConfig{FourCC: "UYVY", Width: 4, Height: 2}); err != nil {
		t.Fatal(err)
	}

	return p, op, op.waitOpened(t)
}

func TestPipelineStreamsLatestFrames(t *testing.T) {
	p, _, s := startUYVYPipeline(t)
	defer p.Stop()

	// A fast producer: the render loop must surface the LATEST decoded
	// frame, not every intermediate one, and never stall the producer.
	go func() {
		for i := range 20 {
			s.sendFrame(uyvyFrame(4, 2, byte(10*i)))
		}
	}()

	deadline := time.After(3 * time.Second)

	var last Event

	for last.Seq != 20 {
		select {
		case ev := <-p.Events():
			if ev.State != StateStreaming {
				t.Fatalf("state %v (%s), want Streaming", ev.State, ev.Detail)
			}

			if ev.Img == nil {
				t.Fatal("streaming event without image")
			}

			if ev.Layout.FourCC != "UYVY" || ev.Layout.Width != 4 || ev.Layout.Height != 2 {
				t.Fatalf("layout %+v", ev.Layout)
			}

			last = ev
		case <-deadline:
			t.Fatalf("never reached seq 20 (last %d)", last.Seq)
		}
	}

	if _, ok := last.Img.(*image.YCbCr); !ok {
		t.Fatalf("published %T, want *image.YCbCr", last.Img)
	}
}

// TestPipelinePublishedPixelsImmutable holds a reference to a published
// frame across two subsequent publications — the 3-slot ring must not
// touch its pixels.
func TestPipelinePublishedPixelsImmutable(t *testing.T) {
	p, _, s := startUYVYPipeline(t)
	defer p.Stop()

	s.sendFrame(uyvyFrame(4, 2, 0))
	first := waitState(t, p, StateStreaming)

	held, ok := first.Img.(*image.YCbCr)
	if !ok {
		t.Fatalf("published %T", first.Img)
	}

	sum := func(img *image.YCbCr) uint64 {
		var acc uint64

		for _, v := range img.Y {
			acc += uint64(v)
		}

		return acc
	}

	before := sum(held)

	// Two more publications (ring size 3: slot 0 is only refilled on the
	// fourth publication).
	s.sendFrame(uyvyFrame(4, 2, 100))
	waitState(t, p, StateStreaming)
	s.sendFrame(uyvyFrame(4, 2, 200))
	waitState(t, p, StateStreaming)

	if after := sum(held); after != before {
		t.Fatalf("published image mutated: checksum %d -> %d", before, after)
	}
}

func TestPipelineFrozenRetentionAndResume(t *testing.T) {
	p, op, s := startUYVYPipeline(t)
	defer p.Stop()

	s.sendFrame(uyvyFrame(4, 2, 0))
	streaming := waitState(t, p, StateStreaming)

	// Foreign key never suspends.
	if _, owned, err := p.SuspendForReconfigure(context.Background(), "other-device"); owned || err != nil {
		t.Fatalf("foreign key: owned=%v err=%v", owned, err)
	}

	resume, owned, err := p.SuspendForReconfigure(context.Background(), "dev-key")
	if err != nil || !owned || resume == nil {
		t.Fatalf("suspend: owned=%v err=%v resumeNil=%v", owned, err, resume == nil)
	}

	frozen := waitState(t, p, StateFrozen)

	// The last displayed frame is retained through the freeze.
	if frozen.Img != streaming.Img {
		t.Fatal("frozen event lost the last displayed frame")
	}

	if !s.stopped.Load() {
		t.Fatal("suspend must stop (release) the stream fd")
	}

	// Resuming reopens the stream with the new configuration.
	if err := resume(context.Background(), domain.CaptureConfig{FourCC: "UYVY", Width: 8, Height: 4}); err != nil {
		t.Fatal(err)
	}

	s2 := op.waitOpened(t)

	if op.count() != 2 {
		t.Fatalf("opener called %d times after resume, want 2", op.count())
	}

	s2.sendFrame(uyvyFrame(8, 4, 7))
	ev := waitState(t, p, StateStreaming)

	if ev.Layout.Width != 8 || ev.Layout.Height != 4 {
		t.Fatalf("resumed layout %+v, want 8x4", ev.Layout)
	}
}

func TestPipelineUnsupportedFourCCNeverOpensStream(t *testing.T) {
	op := newFakeOpener()
	p := NewPipeline("dev-key", op.open, nil)
	p.renderEvery = 5 * time.Millisecond

	if err := p.Start(context.Background(), domain.CaptureConfig{FourCC: "H264", Width: 640, Height: 480}); err != nil {
		t.Fatal(err)
	}

	ev := waitState(t, p, StateUnsupported)

	if !strings.Contains(ev.Detail, "H264") {
		t.Fatalf("detail %q must name the FOURCC", ev.Detail)
	}

	if op.count() != 0 {
		t.Fatalf("unsupported config must not open a stream (opened %d)", op.count())
	}

	// Controls keep working: the pipeline is not stuck in a running state.
	p.Stop()

	if st, _ := p.State(); st != StateStopped {
		t.Fatalf("state %v after Stop", st)
	}
}

func TestPipelineOpenErrorsClassified(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want State
	}{
		{
			// External owner holds the device (e.g. EBUSY from stream setup).
			name: "busy",
			err:  fmt.Errorf("stream setup: %w", domain.ErrBusy),
			want: StateBusy,
		},
		{
			// e.g. v4l rejecting MPLANE capture buffer types.
			name: "unsupported",
			err:  fmt.Errorf("%w: MPLANE capture buffer type not supported for streaming", domain.ErrUnsupported),
			want: StateUnsupported,
		},
		{
			name: "other",
			err:  errors.New("device vanished"),
			want: StateError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			op := &fakeOpener{err: tc.err}
			p := NewPipeline("dev-key", op.open, nil)
			p.renderEvery = 5 * time.Millisecond

			if err := p.Start(context.Background(), domain.CaptureConfig{FourCC: "UYVY", Width: 4, Height: 2}); err != nil {
				t.Fatal(err)
			}

			ev := waitState(t, p, tc.want)

			if ev.Detail == "" {
				t.Fatal("failure state must carry the error detail")
			}
		})
	}
}

// TestPipelineDecodeErrorKeepsLastImage: a corrupt frame must not kill the
// stream; the next good frame publishes again.
func TestPipelineDecodeErrorKeepsLastImage(t *testing.T) {
	p, _, s := startUYVYPipeline(t)
	defer p.Stop()

	s.sendFrame(uyvyFrame(4, 2, 0))
	first := waitState(t, p, StateStreaming)

	// Truncated frame: decode fails, no state event, pipeline continues.
	bad := uyvyFrame(4, 2, 50)
	bad.Data = bad.Data[:5]
	s.sendFrame(bad)

	s.sendFrame(uyvyFrame(4, 2, 90))
	next := waitState(t, p, StateStreaming)

	if next.Seq <= first.Seq {
		t.Fatalf("seq did not advance: %d -> %d", first.Seq, next.Seq)
	}

	if st, _ := p.State(); st != StateStreaming {
		t.Fatalf("state %v after one corrupt frame, want Streaming", st)
	}
}

func TestPipelineStopIsFinal(t *testing.T) {
	p, _, s := startUYVYPipeline(t)

	s.sendFrame(uyvyFrame(4, 2, 0))
	waitState(t, p, StateStreaming)

	p.Stop()

	ev := waitState(t, p, StateStopped)

	if ev.Img != nil {
		t.Fatal("stopped event must not carry a frame")
	}

	if !s.stopped.Load() {
		t.Fatal("Stop must stop the stream")
	}

	if err := p.Start(context.Background(), domain.CaptureConfig{FourCC: "UYVY", Width: 4, Height: 2}); err == nil {
		t.Fatal("Start after Stop must fail (pipelines are single-use)")
	}
}

// TestPipelineStreamEndedReports: the frame channel closing without our
// stop/suspend is an error, not silence.
func TestPipelineStreamEndedReports(t *testing.T) {
	p, _, s := startUYVYPipeline(t)
	defer p.Stop()

	s.sendFrame(uyvyFrame(4, 2, 0))
	waitState(t, p, StateStreaming)

	s.endStream()

	ev := waitState(t, p, StateError)

	if !strings.Contains(ev.Detail, "stream ended") {
		t.Fatalf("detail %q, want stream-ended explanation", ev.Detail)
	}
}

// TestPipelineSuspendWithoutStream: suspending before anything streams is
// a no-op (owned=false) — applies proceed without touching preview.
func TestPipelineSuspendWithoutStream(t *testing.T) {
	op := newFakeOpener()
	p := NewPipeline("dev-key", op.open, nil)

	resume, owned, err := p.SuspendForReconfigure(context.Background(), "dev-key")
	if err != nil || owned || resume != nil {
		t.Fatalf("suspend without stream: resumeNil=%v owned=%v err=%v", resume == nil, owned, err)
	}
}

func TestManagerStreamOwnerContract(t *testing.T) {
	op := newFakeOpener()
	m := NewManager(op.open, nil)

	// No pipeline yet: the apply engine proceeds untouched.
	if _, owned, err := m.SuspendForReconfigure(context.Background(), "dev-key"); owned || err != nil {
		t.Fatalf("empty manager: owned=%v err=%v", owned, err)
	}

	p := m.PipelineFor("dev-key")

	// Created but never started: still not owned.
	if _, owned, _ := m.SuspendForReconfigure(context.Background(), "dev-key"); owned {
		t.Fatal("idle pipeline must not claim ownership")
	}

	if err := p.Start(context.Background(), domain.CaptureConfig{FourCC: "UYVY", Width: 4, Height: 2}); err != nil {
		t.Fatal(err)
	}

	// Deterministic readiness: stream open AND first frame published.
	s := op.waitOpened(t)
	s.sendFrame(uyvyFrame(4, 2, 0))
	waitState(t, p, StateStreaming)

	resume, owned, err := m.SuspendForReconfigure(context.Background(), "dev-key")
	if err != nil || !owned || resume == nil {
		t.Fatalf("streaming pipeline: owned=%v err=%v", owned, err)
	}

	waitState(t, p, StateFrozen)

	// StopKey discards the pipeline; a subsequent PipelineFor builds fresh.
	m.StopKey("dev-key")

	p2 := m.PipelineFor("dev-key")
	if p2 == p {
		t.Fatal("StopKey must discard the stopped pipeline")
	}

	m.StopAll()
}
