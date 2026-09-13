package viewmodel

import "git.skobk.in/skobkin/v4lens/internal/domain"

// Tab identifies the main-window tab a control belongs to.
type Tab int8

const (
	TabImage Tab = iota
	TabPTZ
	TabAdvanced
)

// String returns the tab's display name.
func (t Tab) String() string {
	switch t {
	case TabImage:

		return "Image"
	case TabPTZ:

		return "PTZ"
	default:

		return "Advanced"
	}
}

// Semantic groups inside the Image tab, in display order.
const (
	GroupExposure     = "Exposure"
	GroupImage        = "Image"
	GroupWhiteBalance = "White Balance"
	GroupFocus        = "Focus / Lens"
	GroupPTZ          = "PTZ"
)

// semanticEntry is one curated control mapping. The table classifies
// well-known standard V4L2 IDs; it never defines the supported universe —
// everything unlisted lands in Advanced grouped by control class.
type semanticEntry struct {
	tab   Tab
	group string
	order int
}

// curated lists the standard V4L2 control IDs with a stable semantic home
// (linux/videodev2.h uapi IDs).
var curated = map[uint32]semanticEntry{
	// User class: image tuning.
	0x00980900: {tab: TabImage, group: GroupImage, order: 10}, // Brightness
	0x00980901: {tab: TabImage, group: GroupImage, order: 20}, // Contrast
	0x00980902: {tab: TabImage, group: GroupImage, order: 30}, // Saturation
	0x00980903: {tab: TabImage, group: GroupImage, order: 40}, // Hue
	0x00980911: {tab: TabImage, group: GroupImage, order: 50}, // Sharpness
	0x00980912: {tab: TabImage, group: GroupImage, order: 60}, // Backlight compensation
	0x00980918: {tab: TabImage, group: GroupImage, order: 70}, // Gamma

	// User class: white balance.
	0x0098090c: {tab: TabImage, group: GroupWhiteBalance, order: 10}, // Auto white balance
	0x0098090d: {tab: TabImage, group: GroupWhiteBalance, order: 20}, // Do white balance
	0x0098090e: {tab: TabImage, group: GroupWhiteBalance, order: 30}, // White balance temperature

	// Camera class: exposure + focus.
	0x009a0901: {tab: TabImage, group: GroupExposure, order: 10}, // Auto exposure (mode menu)
	0x009a0902: {tab: TabImage, group: GroupExposure, order: 20}, // Exposure time, absolute
	0x009a0903: {tab: TabImage, group: GroupExposure, order: 30}, // Exposure auto priority
	0x009a090a: {tab: TabImage, group: GroupFocus, order: 10},    // Focus, absolute
	0x009a090b: {tab: TabImage, group: GroupFocus, order: 20},    // Focus, relative
	0x009a090c: {tab: TabImage, group: GroupFocus, order: 30},    // Focus, automatic continuous

	// Camera class: pan/tilt/zoom.
	0x009a0904: {tab: TabPTZ, group: GroupPTZ, order: 10}, // Pan, relative
	0x009a0905: {tab: TabPTZ, group: GroupPTZ, order: 20}, // Tilt, relative
	0x009a0906: {tab: TabPTZ, group: GroupPTZ, order: 30}, // Pan, reset
	0x009a0907: {tab: TabPTZ, group: GroupPTZ, order: 40}, // Tilt, reset
	0x009a0908: {tab: TabPTZ, group: GroupPTZ, order: 50}, // Pan, absolute
	0x009a0909: {tab: TabPTZ, group: GroupPTZ, order: 60}, // Tilt, absolute
	0x009a090d: {tab: TabPTZ, group: GroupPTZ, order: 70}, // Zoom, absolute
	0x009a090e: {tab: TabPTZ, group: GroupPTZ, order: 80}, // Zoom, relative
	0x009a090f: {tab: TabPTZ, group: GroupPTZ, order: 90}, // Zoom, continuous
}

// Semantics is the resolved placement of one control.
type Semantics struct {
	Tab   Tab
	Group string
	Order int
}

// Classify maps a descriptor to its tab/group placement. Unlisted controls
// go to Advanced (group filled in by the caller from the control class so
// the pure table stays free of runtime state).
func Classify(d domain.ControlDescriptor) Semantics {
	if e, ok := curated[d.ID]; ok {
		return Semantics{Tab: e.tab, Group: e.group, Order: e.order}
	}

	return Semantics{Tab: TabAdvanced, Group: advancedGroupOf(d)}
}

// advancedGroupOf names the Advanced group for a control: its reported
// V4L2 control class when known, else the driver class metadata.
func advancedGroupOf(d domain.ControlDescriptor) string {
	if d.ClassName != "" {
		return d.ClassName
	}

	return "Other"
}

// GroupOrder is the Image-tab group display order.
var GroupOrder = map[string]int{
	GroupExposure:     10,
	GroupImage:        20,
	GroupWhiteBalance: 30,
	GroupFocus:        40,
}
