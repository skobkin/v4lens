// Package v4l is the ONLY package allowed to import go4vl and the home of
// V4Lens's local V4L2 ioctl wrappers. go4vl is used for MMAP streaming and
// capability enumeration where the Phase 0 probe validated it; extended
// control access and format transactions use faithful local ioctls
// (DECISION_LOG D013: go4vl misses user-class controls and truncates
// int64 metadata).
package v4l

/*
#include <stddef.h>
#include <linux/videodev2.h>

// v4l2_reqs exposes kernel-computed ioctl request numbers, struct sizes and
// decisive field offsets so the Go side never hand-computes _IOC values or
// guesses struct layout. The package test asserts the Go mirrors against it.
struct v4l2_reqs {
	unsigned g_fmt;
	unsigned s_fmt;
	unsigned try_fmt;
	unsigned g_parm;
	unsigned s_parm;
	unsigned query_ext_ctrl;
	unsigned g_ext_ctrls;
	unsigned s_ext_ctrls;
	unsigned g_ctrl;
	unsigned s_ctrl;
	unsigned g_input;
	unsigned s_input;
	unsigned query_cap;
	unsigned query_menu;
	unsigned enum_fmt;
	unsigned enum_framesizes;
	unsigned enum_frameintervals;
	unsigned enum_input;
	unsigned sz_query_ext_ctrl;
	unsigned sz_ext_control;
	unsigned sz_ext_controls;
	unsigned sz_format;
	unsigned sz_streamparm;
	unsigned sz_capability;
	unsigned sz_fmtdesc;
	unsigned sz_frmsizeenum;
	unsigned sz_frmivalenum;
	unsigned sz_input;
	unsigned sz_querymenu;
	unsigned off_qec_elem_size;
	unsigned off_qec_elems;
	unsigned off_ext_ctrl_value;
	unsigned off_ext_ctrls_controls;
	unsigned off_fmtdesc_pixelformat;
	unsigned off_frmsize_stepwise;
	unsigned off_frmival_stepwise;
	unsigned off_input_std;
	unsigned off_querymenu_name;
};

const struct v4l2_reqs v4l2_reqs = {
	VIDIOC_G_FMT,
	VIDIOC_S_FMT,
	VIDIOC_TRY_FMT,
	VIDIOC_G_PARM,
	VIDIOC_S_PARM,
	VIDIOC_QUERY_EXT_CTRL,
	VIDIOC_G_EXT_CTRLS,
	VIDIOC_S_EXT_CTRLS,
	VIDIOC_G_CTRL,
	VIDIOC_S_CTRL,
	VIDIOC_G_INPUT,
	VIDIOC_S_INPUT,
	VIDIOC_QUERYCAP,
	VIDIOC_QUERYMENU,
	VIDIOC_ENUM_FMT,
	VIDIOC_ENUM_FRAMESIZES,
	VIDIOC_ENUM_FRAMEINTERVALS,
	VIDIOC_ENUMINPUT,
	sizeof(struct v4l2_query_ext_ctrl),
	sizeof(struct v4l2_ext_control),
	sizeof(struct v4l2_ext_controls),
	sizeof(struct v4l2_format),
	sizeof(struct v4l2_streamparm),
	sizeof(struct v4l2_capability),
	sizeof(struct v4l2_fmtdesc),
	sizeof(struct v4l2_frmsizeenum),
	sizeof(struct v4l2_frmivalenum),
	sizeof(struct v4l2_input),
	sizeof(struct v4l2_querymenu),
	offsetof(struct v4l2_query_ext_ctrl, elem_size),
	offsetof(struct v4l2_query_ext_ctrl, elems),
	offsetof(struct v4l2_ext_control, value),
	offsetof(struct v4l2_ext_controls, controls),
	offsetof(struct v4l2_fmtdesc, pixelformat),
	offsetof(struct v4l2_frmsizeenum, stepwise),
	offsetof(struct v4l2_frmivalenum, stepwise),
	offsetof(struct v4l2_input, std),
	offsetof(struct v4l2_querymenu, name),
};
*/
import "C"

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Reqs carries ioctl request numbers, struct sizes and key field offsets
// as computed from the in-tree kernel headers.
type Reqs struct {
	GFmt, SFmt, TryFmt          uint32
	GParm, SParm                uint32
	QueryExtCtrl, GExtCtrls     uint32
	SExtCtrls, GCtrl, SCtrl     uint32
	GInput, SInput              uint32
	QueryCap, QueryMenu         uint32
	EnumFmt                     uint32
	EnumFrameSizes, EnumFrameIv uint32
	EnumInput                   uint32

	SzQueryExtCtrl              uintptr
	SzExtControl, SzExtControls uintptr
	SzFormat, SzStreamparm      uintptr
	SzCapability                uintptr
	SzFmtDesc                   uintptr
	SzFrameSizeEnum             uintptr
	SzFrameIvalEnum             uintptr
	SzInput                     uintptr
	SzQueryMenu                 uintptr

	OffQecElemSize      uintptr
	OffQecElems         uintptr
	OffExtCtrlValue     uintptr
	OffExtCtrlsControls uintptr
	OffFmtDescFourCC    uintptr
	OffFrameSizeStep    uintptr
	OffFrameIvalStep    uintptr
	OffInputStd         uintptr
	OffQueryMenuName    uintptr
}

// ReqsOf returns the header-computed request numbers, sizes and offsets.
func ReqsOf() Reqs {
	r := C.v4l2_reqs

	return Reqs{
		GFmt: uint32(r.g_fmt), SFmt: uint32(r.s_fmt), TryFmt: uint32(r.try_fmt),
		GParm: uint32(r.g_parm), SParm: uint32(r.s_parm),
		QueryExtCtrl: uint32(r.query_ext_ctrl),
		GExtCtrls:    uint32(r.g_ext_ctrls), SExtCtrls: uint32(r.s_ext_ctrls),
		GCtrl: uint32(r.g_ctrl), SCtrl: uint32(r.s_ctrl),
		GInput: uint32(r.g_input), SInput: uint32(r.s_input),
		QueryCap: uint32(r.query_cap), QueryMenu: uint32(r.query_menu),
		EnumFmt:         uint32(r.enum_fmt),
		EnumFrameSizes:  uint32(r.enum_framesizes),
		EnumFrameIv:     uint32(r.enum_frameintervals),
		EnumInput:       uint32(r.enum_input),
		SzQueryExtCtrl:  uintptr(r.sz_query_ext_ctrl),
		SzExtControl:    uintptr(r.sz_ext_control),
		SzExtControls:   uintptr(r.sz_ext_controls),
		SzFormat:        uintptr(r.sz_format),
		SzStreamparm:    uintptr(r.sz_streamparm),
		SzCapability:    uintptr(r.sz_capability),
		SzFmtDesc:       uintptr(r.sz_fmtdesc),
		SzFrameSizeEnum: uintptr(r.sz_frmsizeenum),
		SzFrameIvalEnum: uintptr(r.sz_frmivalenum),
		SzInput:         uintptr(r.sz_input),
		SzQueryMenu:     uintptr(r.sz_querymenu),

		OffQecElemSize:      uintptr(r.off_qec_elem_size),
		OffQecElems:         uintptr(r.off_qec_elems),
		OffExtCtrlValue:     uintptr(r.off_ext_ctrl_value),
		OffExtCtrlsControls: uintptr(r.off_ext_ctrls_controls),
		OffFmtDescFourCC:    uintptr(r.off_fmtdesc_pixelformat),
		OffFrameSizeStep:    uintptr(r.off_frmsize_stepwise),
		OffFrameIvalStep:    uintptr(r.off_frmival_stepwise),
		OffInputStd:         uintptr(r.off_input_std),
		OffQueryMenuName:    uintptr(r.off_querymenu_name),
	}
}

// ---- Go mirrors of the Linux uapi structs (asserted against the headers
// ---- in the package tests; see the Phase 0 findings on elem_size). ----

type v4l2QueryExtCtrl struct {
	ID       uint32
	Type     uint32
	Name     [32]byte
	Minimum  int64
	Maximum  int64
	Step     int64
	Default  int64
	Flags    uint32
	ElemSize uint32
	Elems    uint32
	NrOfDims uint32
	Dims     [4]uint32
	Reserved [32]uint32
}

// v4l2ExtControl mirrors struct v4l2_ext_control: the value union sits at
// offset 12 with no trailing padding.
type v4l2ExtControl struct {
	ID   uint32
	Size uint32
	_    uint32 // kernel-reserved word between size and the union
	VLo  uint32 // union low half: value (s32) / value64 low bits
	VHi  int32  // union high half: value64 high bits
}

func (c *v4l2ExtControl) packValue(v int64) {
	c.VLo = uint32(uint64(v))
	c.VHi = int32(v >> 32)
}

func (c *v4l2ExtControl) value(is64 bool) int64 {
	if is64 {
		return int64(uint64(c.VLo) | uint64(uint32(c.VHi))<<32)
	}

	return int64(int32(c.VLo))
}

type v4l2ExtControls struct {
	Which    uint32 // 0 == V4L2_CTRL_WHICH_CUR_VAL
	Count    uint32
	ErrorIdx uint32
	Reserved [2]uint32
	Controls unsafe.Pointer
}

type v4l2Control struct { // legacy struct v4l2_control
	ID    uint32
	Value int32
}

type v4l2Fract struct {
	Numerator   uint32
	Denominator uint32
}

type v4l2PixFormat struct {
	Width        uint32
	Height       uint32
	PixelFormat  uint32
	Field        uint32
	BytesPerLine uint32
	SizeImage    uint32
	Colorspace   uint32
	Priv         uint32
	Flags        uint32
	YcbcrEnc     uint32
	Quantization uint32
	XferFunc     uint32
}

type v4l2Format struct { // pix member; union 8-aligned via v4l2_window
	Type uint32
	_    [4]byte
	Pix  v4l2PixFormat
	_    [200 - unsafe.Sizeof(v4l2PixFormat{})]byte
}

type v4l2CaptureParm struct {
	Capability   uint32
	CaptureMode  uint32
	TimePerFrame v4l2Fract
	ExtendedMode uint32
	ReadBuffers  uint32
	Reserved     [4]uint32
}

type v4l2Streamparm struct { // capture member; union 4-aligned here
	Type    uint32
	Capture v4l2CaptureParm
	_       [200 - unsafe.Sizeof(v4l2CaptureParm{})]byte
}

type v4l2Capability struct {
	Driver     [16]byte
	Card       [32]byte
	BusInfo    [32]byte
	Version    uint32
	Caps       uint32
	DeviceCaps uint32
	Reserved   [3]uint32
}

type v4l2FmtDesc struct {
	Index       uint32
	Type        uint32
	Flags       uint32
	Description [32]byte
	PixelFormat uint32
	MbusCode    uint32
	Reserved    [3]uint32
}

// v4l2FrmsizeDiscrete/Stepwise are embedded in the frmsizeenum union.
type v4l2FrmsizeDiscrete struct {
	Width  uint32
	Height uint32
}

type v4l2FrmsizeStepwise struct {
	MinWidth   uint32
	MaxWidth   uint32
	StepWidth  uint32
	MinHeight  uint32
	MaxHeight  uint32
	StepHeight uint32
}

type v4l2FrameSizeEnum struct {
	Index       uint32
	PixelFormat uint32
	Type        uint32
	Union       [24]byte // discrete or stepwise, 4-aligned
	Reserved    [2]uint32
}

func (e *v4l2FrameSizeEnum) discrete() v4l2FrmsizeDiscrete {
	return *(*v4l2FrmsizeDiscrete)(unsafe.Pointer(&e.Union[0])) //nolint:gosec // union view of a local uapi struct
}

func (e *v4l2FrameSizeEnum) stepwise() v4l2FrmsizeStepwise {
	return *(*v4l2FrmsizeStepwise)(unsafe.Pointer(&e.Union[0])) //nolint:gosec // union view of a local uapi struct
}

type v4l2FrameIvalEnum struct {
	Index       uint32
	PixelFormat uint32
	Width       uint32
	Height      uint32
	Type        uint32
	Union       [24]byte // discrete fract or stepwise range
	Reserved    [2]uint32
}

func (e *v4l2FrameIvalEnum) discrete() v4l2Fract {
	return *(*v4l2Fract)(unsafe.Pointer(&e.Union[0])) //nolint:gosec // union view of a local uapi struct
}

type v4l2FrameIvalStepwise struct {
	Min  v4l2Fract
	Max  v4l2Fract
	Step v4l2Fract
}

func (e *v4l2FrameIvalEnum) stepwise() v4l2FrameIvalStepwise {
	return *(*v4l2FrameIvalStepwise)(unsafe.Pointer(&e.Union[0])) //nolint:gosec // union view of a local uapi struct
}

type v4l2Input struct {
	Index    uint32
	Name     [32]byte
	Type     uint32
	Audioset uint32
	Tuner    uint32
	Std      uint64
	Status   uint32
	Caps     uint32
	Reserved [3]uint32
}

type v4l2QueryMenu struct {
	ID    uint32
	Index uint32
	Name  [32]byte // union with int64 value; name covers the larger arm
	_     uint32   // union tail for the value arm
}

func (m *v4l2QueryMenu) intValue() int64 {
	return *(*int64)(unsafe.Pointer(&m.Name[0])) //nolint:gosec // union view of a local uapi struct
}

// ---- uapi constants V4Lens relies on ----

const (
	bufTypeVideoCapture = 1

	ctrlTypeInteger     = 1
	ctrlTypeBoolean     = 2
	ctrlTypeMenu        = 3
	ctrlTypeButton      = 4
	ctrlTypeInteger64   = 5
	ctrlTypeCtrlClass   = 6
	ctrlTypeString      = 7
	ctrlTypeBitmask     = 8
	ctrlTypeIntegerMenu = 9
	ctrlTypeU8          = 0x0100
	ctrlTypeU16         = 0x0101
	ctrlTypeU32         = 0x0102

	ctrlFlagDisabled   = 0x0001
	ctrlFlagGrabbed    = 0x0002
	ctrlFlagReadOnly   = 0x0004
	ctrlFlagUpdate     = 0x0008
	ctrlFlagInactive   = 0x0010
	ctrlFlagSlider     = 0x0020
	ctrlFlagWriteOnly  = 0x0040
	ctrlFlagVolatile   = 0x0080
	ctrlFlagHasPayload = 0x0100
	ctrlFlagExecute    = 0x0200
	ctrlFlagModLayout  = 0x0400
	ctrlFlagDynArray   = 0x0800
	ctrlFlagNextCtrl   = 0x80000000
	ctrlFlagNextCompnd = 0x40000000

	capVideoCapture      = 0x00000001
	capVideoCaptureMCore = 0x00001000 // V4L2_CAP_VIDEO_CAPTURE_MPLANE
	capMetaCapture       = 0x00800000
	capDeviceCaps        = 0x04000000

	parmCapTimePerFrame = 0x1000

	fmtSizeTypeDiscrete   = 1
	fmtSizeTypeStepwise   = 2
	fmtSizeTypeContinuous = 3
	fmtIvalTypeDiscrete   = 1
	fmtIvalTypeStepwise   = 2
	fmtIvalTypeContinuous = 3
	inputTypeTuner        = 1
	inputTypeCamera       = 2

	fmtDescFlagCompressed = 0x0001
	fmtDescFlagEmulated   = 0x0002
)

// ioctl performs one raw V4L2 ioctl and converts the errno to an error.
func ioctl(fd uintptr, req uint32, arg unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, fd, uintptr(req), uintptr(arg)) //nolint:gosec // low-level V4L2 ioctl; arguments are kernel uapi structs owned here

	if errno != 0 {
		return fmt.Errorf("ioctl 0x%08x: %w", req, errno)
	}

	return nil
}
