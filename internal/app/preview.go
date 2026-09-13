package app

import (
	"context"
	"fmt"

	"git.skobk.in/skobkin/v4lens/internal/device"
	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// OpenStream opens the streaming lane for one device. It deliberately does
// NOT run on the control-lane session worker: stream setup (the capture
// transaction on a short-lived fd plus the MMAP open) must never stall
// slider writes, and OpenStream only reads the handle's immutable node
// path — no control-fd state is touched.
func (a *App) OpenStream(ctx context.Context, key string, cfg device.StreamConfig) (device.Stream, error) {
	a.mu.Lock()
	e := a.devices[key]
	a.mu.Unlock()

	if e == nil || e.sess == nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrDisconnected, key)
	}

	return e.sess.Handle().OpenStream(ctx, cfg)
}

// ActiveCapture reads the effective capture configuration from the control
// fd (short op) — the configuration a preview starts from.
func (a *App) ActiveCapture(ctx context.Context, key string) (domain.CaptureConfig, error) {
	a.mu.Lock()
	e := a.devices[key]
	a.mu.Unlock()

	if e == nil || e.sess == nil {
		return domain.CaptureConfig{}, fmt.Errorf("%w: %s", domain.ErrDisconnected, key)
	}

	var cfg domain.CaptureConfig

	err := e.sess.Do(ctx, func(h device.Handle) error {
		var err error

		cfg, err = h.ActiveCaptureConfig(ctx)

		return err
	})

	return cfg, err
}
