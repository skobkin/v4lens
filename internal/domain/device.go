package domain

// NodeRole classifies one /dev/videoN endpoint of a physical device.
type NodeRole int8

const (
	// RoleCapture is a video-capture node (single-planar or MPLANE).
	RoleCapture NodeRole = iota
	// RoleMetadata is a metadata-capture node (e.g. UVC metadata streams).
	RoleMetadata
	// RoleOther covers output, radio and anything not consumable here.
	RoleOther
)

// CaptureCaps records what kind of capture a node supports. MPLANE capture
// is detected explicitly: the MVP streams single-planar only, and MPLANE
// devices are reported honestly rather than misread as single-planar.
type CaptureCaps struct {
	SinglePlanar bool
	MPlane       bool
}

// EndpointInfo describes one /dev/videoN node belonging to a physical
// device. Node is the path observed at discovery time and is diagnostics
// only — it is never persisted as identity.
type EndpointInfo struct {
	Node        string
	Index       int // stable endpoint index within the physical device
	Role        NodeRole
	CaptureCaps CaptureCaps
}

// DeviceIdentity is the stable physical-device identity. The Key is
// filesystem-safe and deterministic (used as the profile directory name);
// all match metadata lives here so profiles can verify a device still is
// the hardware they were written for (ARCHITECTURE §3).
type DeviceIdentity struct {
	// Key is the stable per-device key (slug + short content hash).
	Key string

	// DisplayName is the human-readable name (QUERYCAP card or sysfs name).
	DisplayName string

	// USB identification, empty for non-USB devices.
	USBVendor  string
	USBProduct string
	Serial     string

	// ByID is the basename of the matching /dev/v4l/by-id entry, if any.
	ByID string

	// ByPath is the basename of the best matching /dev/v4l/by-path entry,
	// used when no serial-backed identity exists.
	ByPath string

	// PhysicalPath is the sysfs device path of the physical parent
	// (e.g. /sys/devices/.../usb3/3-2), the fallback identity anchor.
	PhysicalPath string

	// Fingerprint is a weaker last-resort identity string, set when neither
	// by-id nor serial nor by-path could identify the device.
	Fingerprint string

	// Endpoints are the device's capture/metadata nodes, ordered by index.
	Endpoints []EndpointInfo
}

// InputInfo is one device input as reported by ENUMINPUT. The active input
// comes from ActiveCaptureConfig, not from here.
type InputInfo struct {
	Index uint32
	Name  string
}

// CaptureEndpoint returns the first capture-capable endpoint, or nil.
func (d DeviceIdentity) CaptureEndpoint() *EndpointInfo {
	for i := range d.Endpoints {
		if d.Endpoints[i].Role == RoleCapture {
			return &d.Endpoints[i]
		}
	}

	return nil
}

// MatchQuality expresses how strongly a profile's stored metadata matched a
// present device.
type MatchQuality int8

const (
	// MatchNone means the device was not recognized as the profile's target.
	MatchNone MatchQuality = iota
	// MatchKeyOnly means only the derived key matched (weakest usable).
	MatchKeyOnly
	// MatchMetadata means key plus at least one identity field verified.
	MatchMetadata
)

// DeviceStatus is the truthful, evidence-derived device state shown in the
// UI. Busy appears only when an operation result classified it so.
type DeviceStatus int8

const (
	StatusDisconnected DeviceStatus = iota
	StatusAvailable
	StatusPreviewing
	StatusBusy
)

// String returns a short human-readable status name.
func (s DeviceStatus) String() string {
	switch s {
	case StatusDisconnected:

		return "Disconnected"
	case StatusAvailable:

		return "Available"
	case StatusPreviewing:

		return "Previewing"
	case StatusBusy:

		return "Busy"
	default:

		return "Unknown"
	}
}
