package profile

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"git.skobk.in/skobkin/v4lens/internal/device"
	"git.skobk.in/skobkin/v4lens/internal/device/fakebackend"
	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// fakeCam builds the standard synthetic camera backend used by these tests.
func fakeCam(opts func(*fakebackend.Options)) (*fakebackend.Backend, *device.Session) {
	o := fakebackend.Options{
		Node: "/dev/video0",
		Card: "Synthetic Cam 4K",
		Controls: []fakebackend.ControlSpec{
			{ID: 0x00980900, Name: "Brightness", Type: domain.TypeInteger, Min: -64, Max: 64, Step: 1, Default: 0, Value: 0},
			{ID: 0x00980901, Name: "Contrast", Type: domain.TypeInteger, Min: 0, Max: 255, Step: 1, Default: 128, Value: 128},
			{ID: 0x00980905, Name: "White Balance Auto", Type: domain.TypeBoolean, Min: 0, Max: 1, Default: 1, Value: 1},
			{ID: 0x0098090c, Name: "White Balance Temp", Type: domain.TypeInteger, Min: 2800, Max: 6500, Step: 10, Default: 4600, Value: 4600, DependsOn: 0x00980905, ActiveWhen: 0},
			{ID: 0x00980910, Name: "Bad Control", Type: domain.TypeInteger, Min: 0, Max: 10, Default: 5, Value: 5, WriteErr: errors.New("injected write failure")},
		},
		Formats: []fakebackend.FormatSpec{{
			FourCC: "UYVY", Description: "UYVY 4:2:2",
			Sizes:     []domain.FrameSize{{Type: domain.FrameSizeDiscrete, Width: 1920, Height: 1080}, {Type: domain.FrameSizeDiscrete, Width: 640, Height: 480}},
			Intervals: map[string][]domain.IntervalCap{"1920x1080": {{Type: domain.IntervalDiscrete, Min: domain.FrameInterval{Num: 1, Den: 30}}}},
		}},
		Inputs:       []domain.InputInfo{{Index: 0, Name: "Camera"}},
		ActiveInput:  0,
		Active:       domain.CaptureConfig{FourCC: "UYVY", Width: 1920, Height: 1080},
		TimePerFrame: true,
	}

	if opts != nil {
		opts(&o)
	}

	b := fakebackend.New(o)

	return b, device.NewSession(b, nil)
}

func controlSetting(id uint32, name string, _ domain.ControlType, v int64) ControlSetting { //nolint:unparam // type kept explicit for readability at call sites
	return ControlSetting{ID: id, Name: name, Value: domain.NewIntValue(id, domain.TypeInteger, v)}
}

// outcomeOf returns the FINAL outcome recorded for one control (later
// passes retry failed controls; the last attempt is authoritative).
func outcomeOf(res ApplyResult, id uint32) (StepOutcome, bool) {
	var found StepOutcome
	var ok bool

	for _, st := range res.Steps {
		if st.ID == id {
			found, ok = st, true
		}
	}

	return found, ok
}

func TestApplyFullSuccess(t *testing.T) {
	b, sess := fakeCam(nil)
	defer func() { _ = b.Close() }()
	defer sess.Stop()

	p := &Profile{
		Name:  "P",
		Match: Match{Key: "x"},
		Capture: &domain.CaptureConfig{
			Input: 0, FourCC: "UYVY", Width: 640, Height: 480,
			Interval: &[]domain.FrameInterval{{Num: 1, Den: 30}}[0],
		},
		Controls: []ControlSetting{
			controlSetting(0x00980900, "Brightness", domain.TypeInteger, 10),
			controlSetting(0x00980901, "Contrast", domain.TypeInteger, 200),
			{ID: 0x00980905, Name: "WB Auto", Value: domain.NewBoolValue(0x00980905, false)},
		},
	}

	res := NewApplier(nil).Apply(context.Background(), sess, "x", p, nil)

	if res.Status != StatusFull {
		t.Fatalf("status = %v (%+v)", res.Status, res)
	}

	if !res.Capture.Attempted || res.Capture.Status != StepApplied || res.Capture.Effective == nil {
		t.Fatalf("capture outcome wrong: %+v", res.Capture)
	}

	if eff := res.Capture.Effective; eff.FourCC != "UYVY" || eff.Width != 640 || eff.Height != 480 {
		t.Fatalf("capture effective wrong: %+v", eff)
	}

	for _, st := range res.Steps {
		if st.Status != StepApplied || st.Effective == nil {
			t.Fatalf("step %+v not applied with read-back", st)
		}
	}

	if spec, _ := b.FindControl(0x00980900); spec.Value != 10 {
		t.Fatalf("brightness not applied: %d", spec.Value)
	}
}

func TestApplyIdentityMismatchFailsFast(t *testing.T) {
	b, sess := fakeCam(nil)
	defer func() { _ = b.Close() }()
	defer sess.Stop()

	p := &Profile{
		Name:  "P",
		Match: Match{Key: "other-device"},
		Controls: []ControlSetting{
			controlSetting(0x00980900, "Brightness", domain.TypeInteger, 10),
		},
	}

	res := NewApplier(nil).Apply(context.Background(), sess, "mine", p, nil)

	if res.Status != StatusTotalFailure {
		t.Fatalf("status = %v, want total-failure", res.Status)
	}

	if len(res.Steps) != 0 {
		t.Fatalf("mismatched profile must not attempt writes: %+v", res.Steps)
	}

	if spec, _ := b.FindControl(0x00980900); spec.Value == 10 {
		t.Fatal("write reached the device despite identity mismatch")
	}
}

func TestApplyCaptureBusyContinuesWithControls(t *testing.T) {
	b, sess := fakeCam(func(o *fakebackend.Options) {
		o.CaptureBusy = errors.New("EBUSY (simulated external owner)")
	})
	defer func() { _ = b.Close() }()
	defer sess.Stop()

	p := &Profile{
		Name:  "P",
		Match: Match{Key: "x"},
		Capture: &domain.CaptureConfig{
			FourCC: "UYVY", Width: 640, Height: 480,
		},
		Controls: []ControlSetting{
			controlSetting(0x00980900, "Brightness", domain.TypeInteger, -5),
		},
	}

	res := NewApplier(nil).Apply(context.Background(), sess, "x", p, nil)

	if res.Status != StatusPartial {
		t.Fatalf("status = %v, want partial (capture skipped, control applied)", res.Status)
	}

	if res.Capture.Status != StepSkipped || !res.Capture.Attempted {
		t.Fatalf("capture must be recorded as attempted-and-skipped: %+v", res.Capture)
	}

	if st, ok := outcomeOf(res, 0x00980900); !ok || st.Status != StepApplied {
		t.Fatalf("controls must continue after EBUSY: %+v", res)
	}
}

func TestApplyPartialWithInjectedWriteError(t *testing.T) {
	b, sess := fakeCam(nil)
	defer func() { _ = b.Close() }()
	defer sess.Stop()

	p := &Profile{
		Name:  "P",
		Match: Match{Key: "x"},
		Controls: []ControlSetting{
			controlSetting(0x00980900, "Brightness", domain.TypeInteger, 3),
			controlSetting(0x00980910, "Bad Control", domain.TypeInteger, 7),
		},
	}

	res := NewApplier(nil).Apply(context.Background(), sess, "x", p, nil)

	if res.Status != StatusPartial {
		t.Fatalf("status = %v, want partial", res.Status)
	}

	bad, _ := outcomeOf(res, 0x00980910)

	if bad.Status != StepFailed {
		t.Fatalf("injected failure not reported: %+v", bad)
	}

	good, _ := outcomeOf(res, 0x00980900)

	if good.Status != StepApplied {
		t.Fatalf("healthy control must still apply: %+v", good)
	}
}

// countingHandle counts WriteControl attempts per id so tests can assert
// the exact bounded-retry pass count.
type countingHandle struct {
	device.Handle
	counts map[uint32]*atomic.Int32
}

func (h *countingHandle) WriteControl(ctx context.Context, id uint32, v domain.ControlValue) (domain.WriteResult, error) {
	if c, ok := h.counts[id]; ok {
		c.Add(1)
	}

	return h.Handle.WriteControl(ctx, id, v)
}

func TestApplyDependencyRetryBoundedPasses(t *testing.T) {
	b, plain := fakeCam(nil)
	defer func() { _ = b.Close() }()
	defer plain.Stop()

	counts := map[uint32]*atomic.Int32{
		0x0098090c: {}, // WB Temp (dependent)
		0x00980905: {}, // WB Auto (master)
		0x00980910: {}, // Bad Control (always fails)
	}

	ch := &countingHandle{Handle: plain.Handle(), counts: counts}
	sess := device.NewSession(ch, nil)
	defer sess.Stop()

	// WB Temp is inactive while WB Auto is on (default). The profile wants
	// WB Auto OFF + WB Temp 3200 — listed in the WORST order so the temp
	// write fails on pass 1 and must succeed on the bounded retry pass 2.
	// Bad Control fails on every pass and must be attempted exactly
	// RetryPasses times — never more.
	p := &Profile{
		Name:  "P",
		Match: Match{Key: "x"},
		Controls: []ControlSetting{
			controlSetting(0x0098090c, "WB Temp", domain.TypeInteger, 3200),
			{ID: 0x00980905, Name: "WB Auto", Value: domain.NewBoolValue(0x00980905, false)},
			controlSetting(0x00980910, "Bad Control", domain.TypeInteger, 7),
		},
	}

	res := NewApplier(nil).Apply(context.Background(), sess, "x", p, nil)

	if res.Status != StatusPartial {
		t.Fatalf("status = %v (%+v)", res.Status, res)
	}

	temp, _ := outcomeOf(res, 0x0098090c)

	if temp.Status != StepApplied {
		t.Fatalf("dependent control never applied: %+v", temp)
	}

	if got := counts[0x0098090c].Load(); got != 2 {
		t.Fatalf("dependent write attempts = %d, want exactly 2 (fail + retry)", got)
	}

	if got := counts[0x00980905].Load(); got != 1 {
		t.Fatalf("master write attempts = %d, want 1 (no retry after success)", got)
	}

	if got := counts[0x00980910].Load(); got != int32(DefaultRetryPasses) {
		t.Fatalf("permanent-failure attempts = %d, want exactly %d", got, DefaultRetryPasses)
	}

	if spec, _ := b.FindControl(0x0098090c); spec.Value != 3200 {
		t.Fatalf("WB temp value wrong: %d", spec.Value)
	}

	if spec, _ := b.FindControl(0x00980905); spec.Value != 0 {
		t.Fatalf("WB auto not disabled: %d", spec.Value)
	}
}

func TestApplyMissingAndReadOnlyControlsAreSkipped(t *testing.T) {
	b, sess := fakeCam(func(o *fakebackend.Options) {
		o.Controls = append(o.Controls,
			fakebackend.ControlSpec{ID: 0x00980981, Name: "Read Only", Type: domain.TypeInteger, Min: 0, Max: 1, Value: 0, Flags: domain.FlagReadOnly},
		)
	})
	defer func() { _ = b.Close() }()
	defer sess.Stop()

	p := &Profile{
		Name:  "P",
		Match: Match{Key: "x"},
		Controls: []ControlSetting{
			controlSetting(0x0dead000, "Vanished", domain.TypeInteger, 1),
			controlSetting(0x00980981, "Read Only", domain.TypeInteger, 1),
		},
	}

	res := NewApplier(nil).Apply(context.Background(), sess, "x", p, nil)

	if res.Status != StatusFull {
		t.Fatalf("skips are not failures: status = %v", res.Status)
	}

	gone, _ := outcomeOf(res, 0x0dead000)
	ro, _ := outcomeOf(res, 0x00980981)

	if gone.Status != StepSkipped || ro.Status != StepSkipped {
		t.Fatalf("expected skips: %+v %+v", gone, ro)
	}
}

func TestApplyMatchSkipsTransaction(t *testing.T) {
	b, sess := fakeCam(nil)
	defer func() { _ = b.Close() }()
	defer sess.Stop()

	// Profile capture equals the active config (including the interval the
	// fake reports for a timeperframe device): no attempt, no suspension.
	p := &Profile{
		Name:  "P",
		Match: Match{Key: "x"},
		Capture: &domain.CaptureConfig{
			FourCC: "UYVY", Width: 1920, Height: 1080,
			Interval: &domain.FrameInterval{Num: 1, Den: 30},
		},
	}

	res := NewApplier(nil).Apply(context.Background(), sess, "x", p, nil)

	if res.Status != StatusFull {
		t.Fatalf("status = %v", res.Status)
	}

	if res.Capture.Attempted || res.Capture.Status != StepSkipped {
		t.Fatalf("matching capture must not be re-attempted: %+v", res.Capture)
	}
}

// streamOwnerStub simulates our own preview owning the stream.
type streamOwnerStub struct {
	suspended atomic.Bool
	resumed   atomic.Bool
}

func (s *streamOwnerStub) SuspendForReconfigure(_ context.Context, _ string) (
	func(context.Context, domain.CaptureConfig) error, bool, error,
) {
	s.suspended.Store(true)

	return func(context.Context, domain.CaptureConfig) error {
		s.resumed.Store(true)

		return nil
	}, true, nil
}

func TestApplySuspendsAndResumesOwnStream(t *testing.T) {
	b, sess := fakeCam(nil)
	defer func() { _ = b.Close() }()
	defer sess.Stop()

	owner := &streamOwnerStub{}

	p := &Profile{
		Name:    "P",
		Match:   Match{Key: "x"},
		Capture: &domain.CaptureConfig{FourCC: "UYVY", Width: 640, Height: 480},
	}

	res := NewApplier(nil).Apply(context.Background(), sess, "x", p, owner)

	if res.Status != StatusFull {
		t.Fatalf("status = %v (%+v)", res.Status, res)
	}

	if !owner.suspended.Load() || !owner.resumed.Load() {
		t.Fatal("own stream must be suspended and resumed around the transaction")
	}
}

func TestApplyContextCancelled(t *testing.T) {
	b, sess := fakeCam(nil)
	defer func() { _ = b.Close() }()
	defer sess.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	time.Sleep(2 * time.Millisecond)

	p := &Profile{
		Name:  "P",
		Match: Match{Key: "x"},
		Controls: []ControlSetting{
			controlSetting(0x00980900, "Brightness", domain.TypeInteger, 1),
		},
	}

	res := NewApplier(nil).Apply(ctx, sess, "x", p, nil)

	if res.Status != StatusTotalFailure {
		t.Fatalf("cancelled apply must not report success: %+v", res)
	}
}
