package v4l

import (
	"context"
	"fmt"
	"os"
	"sync"

	"git.skobk.in/skobkin/v4lens/internal/device"
	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// Handle implements the device.Handle contract over one long-lived control
// fd kept in panel mode: controls, capability queries, G_* reads and
// TRY_FMT validation only — it NEVER mutates capture configuration
// (fd-ownership model, D013).
type Handle struct {
	node string
	file *os.File

	mu    sync.RWMutex
	descs map[uint32]domain.ControlDescriptor
}

// NewHandle opens the node's control fd.
func NewHandle(node string) (*Handle, error) {
	file, err := os.OpenFile(node, os.O_RDWR, 0) //nolint:gosec // V4L2 device node path from sysfs discovery
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", node, err)
	}

	return &Handle{node: node, file: file, descs: map[uint32]domain.ControlDescriptor{}}, nil
}

// Node returns the device node path (diagnostics only).
func (h *Handle) Node() string {
	return h.node
}

// Close releases the control fd.
func (h *Handle) Close() error {
	return h.file.Close()
}

func (h *Handle) fd() uintptr {
	return h.file.Fd()
}

// DeviceCaps classifies the node's capture capabilities from QUERYCAP.
func (h *Handle) DeviceCaps(_ context.Context) (domain.CaptureCaps, error) {
	caps, err := QueryCap(h.fd())
	if err != nil {
		return domain.CaptureCaps{}, wrapBusy(fmt.Errorf("QUERYCAP: %w", err))
	}

	single, mplane, _ := CaptureCapsOf(caps.DeviceCaps)

	return domain.CaptureCaps{SinglePlanar: single, MPlane: mplane}, nil
}

// EnumerateControls builds the full control tree and refreshes the
// descriptor cache used by Read/WriteControl.
func (h *Handle) EnumerateControls(_ context.Context) ([]domain.ControlDescriptor, error) {
	descs, err := EnumerateControls(h.fd())
	if err != nil {
		return nil, err
	}

	h.mu.Lock()
	h.descs = make(map[uint32]domain.ControlDescriptor, len(descs))

	for _, d := range descs {
		h.descs[d.ID] = d
	}

	h.mu.Unlock()

	return descs, nil
}

func (h *Handle) descriptor(id uint32) (domain.ControlDescriptor, error) {
	h.mu.RLock()
	d, ok := h.descs[id]
	h.mu.RUnlock()

	if !ok {
		return domain.ControlDescriptor{}, fmt.Errorf("control 0x%08x: %w", id, os.ErrInvalid)
	}

	return d, nil
}

// ReadControls reads the requested controls (in ids order). Unknown ids
// yield an error entry — the UI refreshes descriptors first.
func (h *Handle) ReadControls(_ context.Context, ids []uint32) ([]domain.ControlValue, error) {
	out := make([]domain.ControlValue, 0, len(ids))

	for _, id := range ids {
		d, err := h.descriptor(id)
		if err != nil {
			return nil, err
		}

		v, _, err := ReadControl(h.fd(), d)
		if err != nil {
			return nil, err
		}

		out = append(out, v)
	}

	return out, nil
}

// WriteControl writes one control and returns the truthful WriteResult.
func (h *Handle) WriteControl(_ context.Context, id uint32, value domain.ControlValue) (domain.WriteResult, error) {
	d, err := h.descriptor(id)
	if err != nil {
		return domain.WriteResult{}, err
	}

	res, err := WriteControl(h.fd(), d, value)

	if res.Effective != nil {
		// A successful readable write may have changed live flags.
		h.mu.Lock()

		if cached, ok := h.descs[id]; ok {
			if _, fresh, lerr := ReadControl(h.fd(), cached); lerr == nil {
				cached.CurFlags = fresh.CurFlags
				cached.RawCurFlg = fresh.RawCurFlg
				h.descs[id] = cached
			}
		}

		h.mu.Unlock()
	}

	return res, err
}

// ListInputs enumerates the device inputs (no S_INPUT ever — D013).
func (h *Handle) ListInputs(_ context.Context) ([]domain.InputInfo, error) {
	inputs, err := EnumInputs(h.fd())
	if err != nil {
		return nil, wrapBusy(fmt.Errorf("ENUMINPUT: %w", err))
	}

	out := make([]domain.InputInfo, 0, len(inputs))

	for _, i := range inputs {
		out = append(out, domain.InputInfo{Index: i.Index, Name: i.Name})
	}

	return out, nil
}

// FormatSizes describes formats and frame sizes for the currently active
// input.
func (h *Handle) FormatSizes(_ context.Context) ([]domain.FormatDesc, error) {
	return FormatDescs(h.fd())
}

// FrameIntervals lazily queries intervals for one concrete FOURCC+W+H.
func (h *Handle) FrameIntervals(_ context.Context, cc domain.FOURCC, w, hgt uint32) ([]domain.IntervalCap, error) {
	return FrameIntervals(h.fd(), cc, w, hgt)
}

// ActiveCaptureConfig reads the effective capture configuration.
func (h *Handle) ActiveCaptureConfig(_ context.Context) (domain.CaptureConfig, error) {
	cfg, _, err := ActiveCaptureConfig(h.fd())

	return cfg, err
}

// TryCaptureConfig validates a capture config without mutating anything.
func (h *Handle) TryCaptureConfig(_ context.Context, cfg domain.CaptureConfig) (domain.CaptureConfig, error) {
	return TryCaptureConfig(h.fd(), cfg)
}

// ConfigureDetached performs the whole capture transaction on a short-lived
// fd which closes afterwards, releasing stream ownership. Used when V4Lens
// has no own stream; external owners are never touched (their EBUSY
// surfaces as domain.ErrBusy).
func (h *Handle) ConfigureDetached(_ context.Context, cfg domain.CaptureConfig) (domain.CaptureConfig, error) {
	return ConfigureDetached(h.node, cfg)
}

// ConfigureDetached is the node-level detached transaction used both for
// detached applies and immediately before opening an own stream.
func ConfigureDetached(node string, cfg domain.CaptureConfig) (domain.CaptureConfig, error) {
	file, err := os.OpenFile(node, os.O_RDWR, 0) //nolint:gosec // short-lived configuration fd for the discovered node
	if err != nil {
		return domain.CaptureConfig{}, wrapBusy(fmt.Errorf("open %s: %w", node, err))
	}

	defer func() { _ = file.Close() }()

	caps, err := QueryCap(file.Fd())
	if err != nil {
		return domain.CaptureConfig{}, wrapBusy(fmt.Errorf("QUERYCAP: %w", err))
	}

	_, _, _ = CaptureCapsOf(caps.DeviceCaps) // classification done by caller for streaming

	tpf := supportsTimePerFrame(file.Fd())

	effective, err := ApplyCaptureTransaction(file.Fd(), cfg, tpf)
	if err != nil {
		return domain.CaptureConfig{}, err
	}

	return effective, nil
}

// supportsTimePerFrame reports whether the device's G_PARM advertises
// V4L2_CAP_TIMEPERFRAME; S_PARM is gated on this (0/0 is never sent).
func supportsTimePerFrame(fd uintptr) bool {
	parm, err := GetParm(fd)

	return err == nil && parm.CapTimePerFrame
}

// ClassifyNode opens the node briefly and classifies its role for
// discovery (capture single-planar/MPLANE, metadata, other). The fd is
// closed before returning — discovery never keeps device fds open.
func ClassifyNode(node string) (device.NodeClass, error) {
	file, err := os.OpenFile(node, os.O_RDWR, 0) //nolint:gosec // brief QUERYCAP open of the discovered node
	if err != nil {
		return device.NodeClass{}, fmt.Errorf("classify %s: %w", node, err)
	}

	defer func() { _ = file.Close() }()

	caps, err := QueryCap(file.Fd())
	if err != nil {
		return device.NodeClass{}, wrapBusy(fmt.Errorf("QUERYCAP %s: %w", node, err))
	}

	single, mplane, meta := CaptureCapsOf(caps.DeviceCaps)

	return device.NodeClass{
		Card:         caps.Card,
		SinglePlanar: single,
		MPlane:       mplane,
		Metadata:     meta,
	}, nil
}
