package profile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"git.skobk.in/skobkin/v4lens/internal/device"
	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// Status aggregates an apply run: full success, some failures, or nothing
// applied.
type Status int8

const (
	StatusFull Status = iota
	StatusPartial
	StatusTotalFailure
)

// String returns a short status name.
func (s Status) String() string {
	switch s {
	case StatusFull:

		return "full"
	case StatusPartial:

		return "partial"
	default:

		return "total-failure"
	}
}

// StepStatus is one requested setting's outcome.
type StepStatus int8

const (
	StepApplied StepStatus = iota
	StepSkipped
	StepFailed
)

// String returns a short step-status name.
func (s StepStatus) String() string {
	switch s {
	case StepApplied:

		return "applied"
	case StepSkipped:

		return "skipped"
	default:

		return "failed"
	}
}

// StepOutcome is the result of one requested control.
type StepOutcome struct {
	ID     uint32
	Name   string
	Status StepStatus
	Reason string

	// Effective is the truthful read-back when one exists (nil for
	// WRITE_ONLY and skipped/failed steps).
	Effective *domain.ControlValue
}

// CaptureOutcome is the result of the capture-config part of an apply.
type CaptureOutcome struct {
	Attempted bool
	Status    StepStatus
	Reason    string
	Effective *domain.CaptureConfig
}

// ApplyResult is the structured outcome driving notifications and dirty
// state (ARCHITECTURE §7 step 10).
type ApplyResult struct {
	Key     string
	Profile string
	Capture CaptureOutcome
	Steps   []StepOutcome
	Status  Status
}

// StreamOwner coordinates the apply with our own preview stream
// (ARCHITECTURE §7 steps 3/5): when we own the stream, format changes
// suspend it first and resume with the effective new configuration.
// owned=false means no own stream exists and nothing is suspended.
type StreamOwner interface {
	SuspendForReconfigure(ctx context.Context, key string) (
		resume func(ctx context.Context, newCfg domain.CaptureConfig) error, owned bool, err error)
}

// DefaultRetryPasses bounds the dependency retry: controls that failed
// because a dependency had not flipped yet are retried after a refresh,
// at most this many total passes. No rollback, no infinite retry.
const DefaultRetryPasses = 2

// Applier applies profiles inside a Session exclusive mutation.
type Applier struct {
	RetryPasses int
	log         *slog.Logger
}

// NewApplier builds an applier with DefaultRetryPasses.
func NewApplier(log *slog.Logger) *Applier {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Applier{RetryPasses: DefaultRetryPasses, log: log}
}

// Apply runs the ARCHITECTURE §7 sequence inside one exclusive session
// mutation: identity check, capture transaction (skipped with a recorded
// reason on EBUSY from an external owner), control re-enumeration, the
// profiled control writes with bounded dependency retry, and truthful
// read-backs in the result. Manual writes cannot interleave.
func (a *Applier) Apply(ctx context.Context, sess *device.Session, key string, p *Profile, owner StreamOwner) ApplyResult {
	res := ApplyResult{Key: key, Profile: p.Name}

	// Step 1: verify the profile belongs to this stable device identity.
	if p.Match.Key != "" && p.Match.Key != key {
		res.Status = StatusTotalFailure
		res.Capture = CaptureOutcome{Status: StepSkipped, Reason: "profile belongs to a different device"}

		return res
	}

	mut, err := sess.BeginMutation(ctx, "apply-profile:"+p.Name)
	if err != nil {
		res.Status = StatusTotalFailure
		res.Capture = CaptureOutcome{Status: StepFailed, Reason: err.Error()}

		return res
	}
	defer mut.Cancel()

	a.applyCapture(ctx, mut, key, p, owner, &res)
	a.applyControls(ctx, mut, p, &res)

	// Step 10: aggregate the structured result.
	a.aggregate(&res)

	mut.Finish(nil)

	return res
}

// applyCapture covers steps 2-5 for a profile: delegate to the shared
// capture transaction and record the outcome.
func (a *Applier) applyCapture(ctx context.Context, mut *device.Mutation, key string, p *Profile, owner StreamOwner, res *ApplyResult) {
	if p.Capture == nil {
		res.Capture = CaptureOutcome{Status: StepSkipped, Reason: "profile does not manage capture"}

		return
	}

	_, res.Capture = ApplyCapture(ctx, mut, key, *p.Capture, owner)
}

// ApplyCapture runs the capture-configuration transaction (ARCHITECTURE
// §7 steps 2-5) for want inside the exclusive mutation mut: read the
// current config, skip when it already matches, suspend our own preview,
// run the whole transaction (S_INPUT + S_FMT + S_PARM + read-backs) on a
// short-lived configuration fd, and resume our stream with the effective
// configuration. EBUSY from an external owner yields an
// attempted-but-skipped outcome; callers continue with controls.
func ApplyCapture(ctx context.Context, mut *device.Mutation, key string, want domain.CaptureConfig, owner StreamOwner) (domain.CaptureConfig, CaptureOutcome) {
	var current domain.CaptureConfig

	if err := mut.Do(func(h device.Handle) error {
		c, err := h.ActiveCaptureConfig(ctx)
		if err != nil {
			return err
		}

		current = c

		return nil
	}); err != nil {
		return domain.CaptureConfig{}, CaptureOutcome{Status: StepFailed, Reason: fmt.Sprintf("read current capture config: %v", err)}
	}

	// Step 2: nothing to do when the device already matches.
	if want.Equal(current) {
		return current, CaptureOutcome{Status: StepSkipped, Reason: "capture config already matches"}
	}

	// Step 3: suspend our own preview before the format transaction.
	// (resume is nil when we do not own a stream or the suspend failed.)
	resume, _, reason := suspendForReconfigure(ctx, owner, key)

	// Step 4: the whole transaction on a short-lived configuration fd.
	var effective domain.CaptureConfig

	err := mut.Do(func(h device.Handle) error {
		cc, ok := h.(device.CaptureConfigurer)
		if !ok {
			return fmt.Errorf("backend cannot configure capture")
		}

		eff, err := cc.ConfigureDetached(ctx, want)
		if err != nil {
			return err
		}

		effective = eff

		return nil
	})

	switch {
	case err == nil:
		// Step 5: resume our own stream with the effective configuration
		// (resume is nil when we never owned one).
		return effective, CaptureOutcome{
			Attempted: true,
			Status:    StepApplied,
			Reason:    resumeStream(ctx, resume, effective, reason),
			Effective: &effective,
		}
	case errors.Is(err, domain.ErrBusy):
		// EBUSY from an external owner: record and continue with controls.
		return current, CaptureOutcome{
			Attempted: true,
			Status:    StepSkipped,
			Reason:    "device busy — capture configuration held by another application",
		}
	default:

		return domain.CaptureConfig{}, CaptureOutcome{Attempted: true, Status: StepFailed, Reason: err.Error()}
	}
}

// resumeStream restarts our own preview with the effective configuration,
// appending any failure to the outcome reason.
func resumeStream(ctx context.Context, resume func(context.Context, domain.CaptureConfig) error, effective domain.CaptureConfig, reason string) string {
	if resume == nil {
		return reason
	}

	if rerr := resume(ctx, effective); rerr != nil {
		r := fmt.Sprintf("applied, but preview restart failed: %v", rerr)
		if reason != "" {
			r = reason + "; " + r
		}

		return r
	}

	return reason
}

func suspendForReconfigure(ctx context.Context, owner StreamOwner, key string) (func(context.Context, domain.CaptureConfig) error, bool, string) {
	if owner == nil {
		return nil, false, ""
	}

	resume, owned, err := owner.SuspendForReconfigure(ctx, key)
	if err != nil {
		return nil, false, fmt.Sprintf("preview suspend failed: %v", err)
	}

	if !owned {
		return nil, false, ""
	}

	return resume, true, ""
}

// applyControls covers steps 6-9: re-enumerate (format changes can change
// availability/defaults), write the profiled controls, retry
// dependency-blocked ones for a bounded number of passes, and keep the
// truthful effective values.
func (a *Applier) applyControls(ctx context.Context, mut *device.Mutation, p *Profile, res *ApplyResult) {
	if len(p.Controls) == 0 {
		return
	}

	descs, err := a.enumerate(ctx, mut)
	if err != nil {
		res.Steps = failedSteps(p.Controls, err.Error())

		return
	}

	passes := a.RetryPasses
	if passes <= 0 {
		passes = DefaultRetryPasses
	}

	pending := p.Controls

	for pass := range passes {
		byID := make(map[uint32]domain.ControlDescriptor, len(descs))
		for _, d := range descs {
			byID[d.ID] = d
		}

		var retry []ControlSetting

		for _, st := range pending {
			out := a.applyOne(ctx, mut, byID, st)
			res.Steps = append(res.Steps, out)

			if out.Status == StepFailed {
				retry = append(retry, st)
			}
		}

		pending = retry

		if len(pending) == 0 {
			return
		}

		if pass == passes-1 {
			break
		}

		// Step 8: refresh descriptors so dependency flags reflect the
		// mode-changing writes of the previous pass.
		if descs, err = a.enumerate(ctx, mut); err != nil {
			res.Steps = append(res.Steps, failedSteps(pending, err.Error())...)

			return
		}
	}
}

func (a *Applier) enumerate(ctx context.Context, mut *device.Mutation) ([]domain.ControlDescriptor, error) {
	var descs []domain.ControlDescriptor

	err := mut.Do(func(h device.Handle) error {
		ds, err := h.EnumerateControls(ctx)
		if err != nil {
			return err
		}

		descs = ds

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("enumerate controls: %w", err)
	}

	return descs, nil
}

func (a *Applier) applyOne(ctx context.Context, mut *device.Mutation, byID map[uint32]domain.ControlDescriptor, st ControlSetting) StepOutcome {
	out := StepOutcome{ID: st.ID, Name: st.Name}

	d, ok := byID[st.ID]
	if !ok {
		out.Status = StepSkipped
		out.Reason = "control not present on device"

		return out
	}

	if !d.Editable() {
		out.Status = StepSkipped
		out.Reason = "control is not writable (read-only or unsupported shape)"

		return out
	}

	var wr domain.WriteResult

	err := mut.Do(func(h device.Handle) error {
		r, err := h.WriteControl(ctx, st.ID, st.Value)
		if err != nil {
			return err
		}

		wr = r

		return nil
	})

	if err != nil {
		out.Status = StepFailed
		out.Reason = err.Error()

		return out
	}

	out.Status = StepApplied
	out.Effective = wr.Effective

	return out
}

func failedSteps(settings []ControlSetting, reason string) []StepOutcome {
	out := make([]StepOutcome, 0, len(settings))

	for _, st := range settings {
		out = append(out, StepOutcome{ID: st.ID, Name: st.Name, Status: StepFailed, Reason: reason})
	}

	return out
}

// finish computes the aggregate status from the capture outcome and the
// control steps (step 10).
func (a *Applier) aggregate(res *ApplyResult) {
	applied, failed := 0, 0

	for _, st := range res.Steps {
		switch st.Status {
		case StepApplied:
			applied++
		case StepFailed:
			failed++
		}
	}

	if res.Capture.Attempted {
		switch res.Capture.Status {
		case StepApplied:
			applied++
		case StepSkipped:
			// An attempted-but-skipped capture (EBUSY from an external
			// owner) is requested state that did not get applied — the
			// result is honestly partial.
			failed++
		case StepFailed:
			failed++
		}
	}

	switch {
	case applied == 0 && failed > 0:
		res.Status = StatusTotalFailure
	case failed > 0:
		res.Status = StatusPartial
	default:
		res.Status = StatusFull
	}
}
