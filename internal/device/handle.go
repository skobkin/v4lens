// Package device provides V4Lens's device seam: the backend interfaces
// (implemented by internal/v4l and internal/device/fakebackend), sysfs-first
// discovery with persistent identity, the per-device Session with exclusive
// mutations, and the hotplug scanner with reconnect semantics.
package device

import (
	"context"
	"io"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// StreamConfig parameterizes OpenStream. Capture carries the whole capture
// transaction unit; BufferCount is the requested MMAP buffer count
// (backend may clamp).
type StreamConfig struct {
	Capture     domain.CaptureConfig
	BufferCount int
}

// Stream is one active capture stream. Frames deliver V4Lens-owned copies
// of frame bytes (never driver MMAP memory) with their effective layout.
type Stream interface {
	Frames() <-chan domain.Frame
	Err() <-chan error
	Stop() error
}

// Handle is the consumer-side backend contract. The long-lived control fd
// behind a real Handle stays in panel mode: it never mutates capture
// configuration — capture changes flow through ConfigureDetached (detached
// short-lived fd) or OpenStream (own stream setup), never through this
// handle's control fd (fd-ownership model, DECISION_LOG D013).
type Handle interface {
	io.Closer

	// Node returns the device node path, diagnostics only.
	Node() string

	// DeviceCaps classifies capture capabilities (single-planar vs MPLANE).
	DeviceCaps(ctx context.Context) (domain.CaptureCaps, error)

	// EnumerateControls returns the full control tree including compound
	// descriptors, with faithful int64 metadata and expanded menu items.
	EnumerateControls(ctx context.Context) ([]domain.ControlDescriptor, error)

	// ReadControls reads the current values for the given ids, in order.
	ReadControls(ctx context.Context, ids []uint32) ([]domain.ControlValue, error)

	// WriteControl writes one control; the WriteResult carries the truthful
	// Effective read-back when one exists (never the requested value).
	WriteControl(ctx context.Context, id uint32, value domain.ControlValue) (domain.WriteResult, error)

	// ListInputs enumerates inputs without ever issuing S_INPUT.
	ListInputs(ctx context.Context) ([]domain.InputInfo, error)

	// FormatSizes lists formats+sizes for the currently active input.
	FormatSizes(ctx context.Context) ([]domain.FormatDesc, error)

	// FrameIntervals lazily queries intervals for a concrete FOURCC+W+H.
	FrameIntervals(ctx context.Context, cc domain.FOURCC, w, h uint32) ([]domain.IntervalCap, error)

	// ActiveCaptureConfig reads the effective G_INPUT/G_FMT/G_PARM state.
	ActiveCaptureConfig(ctx context.Context) (domain.CaptureConfig, error)

	// TryCaptureConfig validates a staged config (TRY_FMT only).
	TryCaptureConfig(ctx context.Context, cfg domain.CaptureConfig) (domain.CaptureConfig, error)

	// OpenStream opens the streaming lane; the whole capture transaction is
	// applied as part of stream setup on the stream-owning path.
	OpenStream(ctx context.Context, cfg StreamConfig) (Stream, error)
}

// CaptureConfigurer performs the whole capture transaction on a detached
// short-lived fd when V4Lens owns no stream. It is always invoked inside a
// Session exclusive mutation.
type CaptureConfigurer interface {
	ConfigureDetached(ctx context.Context, cfg domain.CaptureConfig) (domain.CaptureConfig, error)
}

// NodeClass is the discovery-time classification of one node.
type NodeClass struct {
	Card         string // QUERYCAP card, display-name candidate
	SinglePlanar bool
	MPlane       bool
	Metadata     bool
}

// CaptureCaps converts the classification to domain caps.
func (c NodeClass) CaptureCaps() domain.CaptureCaps {
	return domain.CaptureCaps{SinglePlanar: c.SinglePlanar, MPlane: c.MPlane}
}

// Role derives the node role from the classification.
func (c NodeClass) Role() domain.NodeRole {
	switch {
	case c.SinglePlanar || c.MPlane:

		return domain.RoleCapture
	case c.Metadata:

		return domain.RoleMetadata
	default:

		return domain.RoleOther
	}
}

// NodeClassifier classifies one /dev/video node for discovery. It abstracts
// the backend's QUERYCAP so the device package does not depend on the V4L2
// implementation.
type NodeClassifier func(node string) (NodeClass, error)
