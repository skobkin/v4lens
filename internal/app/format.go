package app

import (
	"context"
	"errors"
	"fmt"

	"git.skobk.in/skobkin/v4lens/internal/device"
	"git.skobk.in/skobkin/v4lens/internal/domain"
	"git.skobk.in/skobkin/v4lens/internal/profile"
)

// sessionFor returns the live session of one device.
func (a *App) sessionFor(key string) (*device.Session, error) {
	a.mu.Lock()
	e := a.devices[key]
	a.mu.Unlock()

	if e == nil || e.sess == nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrDisconnected, key)
	}

	return e.sess, nil
}

// Refresh republishes the control and format snapshots for one device
// (used when the UI selects a device that connected earlier).
func (a *App) Refresh(key string) {
	a.refreshSnapshots(key)
}

// ApplyFormat applies a staged capture configuration (input + format +
// size + optional exact interval) as ONE transaction inside an exclusive
// mutation — the same engine path profile capture applies use, including
// StreamOwner suspend/resume. EBUSY from an external owner surfaces as a
// wrapped domain.ErrBusy. The UI refreshes from the published snapshots
// afterwards, never from the staged intent.
func (a *App) ApplyFormat(key string, cfg domain.CaptureConfig) (domain.CaptureConfig, error) {
	sess, err := a.sessionFor(key)
	if err != nil {
		return domain.CaptureConfig{}, err
	}

	ctx := context.Background()

	mut, err := sess.BeginMutation(ctx, "apply-format")
	if err != nil {
		return domain.CaptureConfig{}, err
	}

	defer mut.Cancel()

	effective, out := profile.ApplyCapture(ctx, mut, key, cfg, a.opts.StreamOwner)

	mut.Finish(nil)

	switch {
	case out.Status == profile.StepFailed:
		return effective, errors.New(out.Reason)
	case out.Attempted && out.Status == profile.StepSkipped:
		return effective, fmt.Errorf("%w: %s", domain.ErrBusy, out.Reason)
	}

	a.refreshSnapshots(key)

	return effective, nil
}

// WriteControl performs one manual control write as a session short op.
func (a *App) WriteControl(key string, id uint32, v domain.ControlValue) (domain.WriteResult, error) {
	sess, err := a.sessionFor(key)
	if err != nil {
		return domain.WriteResult{}, err
	}

	ctx := context.Background()

	var wr domain.WriteResult

	err = sess.Do(ctx, func(h device.Handle) error {
		res, err := h.WriteControl(ctx, id, v)
		if err != nil {
			return err
		}

		wr = res

		return nil
	})
	if err != nil {
		return domain.WriteResult{}, err
	}

	return wr, nil
}

// WriteControlExclusive routes a control write through an exclusive
// mutation and refreshes snapshots afterwards. MODIFY_LAYOUT controls MUST
// use this path: the write can invalidate buffer layout, so it may never
// run as an ordinary live write next to an active stream.
func (a *App) WriteControlExclusive(key string, id uint32, v domain.ControlValue) (domain.WriteResult, error) {
	sess, err := a.sessionFor(key)
	if err != nil {
		return domain.WriteResult{}, err
	}

	ctx := context.Background()

	mut, err := sess.BeginMutation(ctx, "write-modify-layout")
	if err != nil {
		return domain.WriteResult{}, err
	}

	defer mut.Cancel()

	var wr domain.WriteResult

	err = mut.Do(func(h device.Handle) error {
		res, werr := h.WriteControl(ctx, id, v)
		if werr != nil {
			return werr
		}

		wr = res

		return nil
	})

	mut.Finish(err)

	if err != nil {
		return domain.WriteResult{}, err
	}

	// Layout change may alter formats/capabilities: refresh honestly.
	a.refreshSnapshots(key)

	return wr, nil
}

// QueryIntervals lazily enumerates frame-interval capabilities for one
// concrete FOURCC+W+H (ENUM_FRAMEINTERVALS requires the concrete triple).
func (a *App) QueryIntervals(key string, cc domain.FOURCC, w, h uint32) ([]domain.IntervalCap, error) {
	sess, err := a.sessionFor(key)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()

	var caps []domain.IntervalCap

	err = sess.Do(ctx, func(dh device.Handle) error {
		cs, err := dh.FrameIntervals(ctx, cc, w, h)
		if err != nil {
			return err
		}

		caps = cs

		return nil
	})
	if err != nil {
		return nil, err
	}

	return caps, nil
}

// TryCaptureConfig validates a staged configuration with TRY_FMT and
// returns the driver-normalized shape (panel-mode validation only — the
// control fd never mutates capture config).
func (a *App) TryCaptureConfig(key string, cfg domain.CaptureConfig) (domain.CaptureConfig, error) {
	sess, err := a.sessionFor(key)
	if err != nil {
		return domain.CaptureConfig{}, err
	}

	ctx := context.Background()

	var resolved domain.CaptureConfig

	err = sess.Do(ctx, func(h device.Handle) error {
		r, err := h.TryCaptureConfig(ctx, cfg)
		if err != nil {
			return err
		}

		resolved = r

		return nil
	})
	if err != nil {
		return domain.CaptureConfig{}, err
	}

	return resolved, nil
}

// DeleteProfile removes a stored profile.
func (a *App) DeleteProfile(key, name string) error {
	return a.opts.Profiles.Delete(key, name)
}

// RenameProfile renames a stored profile and updates the default reference
// so the default survives the rename.
func (a *App) RenameProfile(key, oldName, newName string) error {
	if err := a.opts.Profiles.Rename(key, oldName, newName); err != nil {
		return err
	}

	if a.Prefs().Defaults[key] != oldName {
		return nil
	}

	return a.SetDefaultProfile(key, newName)
}

// ProfileStore exposes the profile store for read-only UI queries that do
// not warrant an App method (e.g. loading a profile for the dirty diff).
func (a *App) ProfileStore() *profile.Store {
	return a.opts.Profiles
}
