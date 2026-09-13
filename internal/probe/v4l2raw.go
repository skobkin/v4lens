//go:build probe

package probe

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

/*
#include <stddef.h>
#include <linux/videodev2.h>

// v4l2_reqs exposes kernel-computed ioctl request numbers and struct sizes so
// the Go side never has to hand-compute _IOC values or guess struct layout.
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
	unsigned sz_query_ext_ctrl;
	unsigned sz_ext_control;
	unsigned sz_ext_controls;
	unsigned sz_format;
	unsigned sz_streamparm;
	unsigned sz_control;
	unsigned off_qec_elem_size;
	unsigned off_qec_elems;
	unsigned off_qec_dims;
	unsigned off_ext_ctrl_value;
	unsigned off_ext_ctrls_controls;
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
	sizeof(struct v4l2_query_ext_ctrl),
	sizeof(struct v4l2_ext_control),
	sizeof(struct v4l2_ext_controls),
	sizeof(struct v4l2_format),
	sizeof(struct v4l2_streamparm),
	sizeof(struct v4l2_control),
	offsetof(struct v4l2_query_ext_ctrl, elem_size),
	offsetof(struct v4l2_query_ext_ctrl, elems),
	offsetof(struct v4l2_query_ext_ctrl, dims),
	offsetof(struct v4l2_ext_control, value),
	offsetof(struct v4l2_ext_controls, controls),
};
*/
import "C"

// Kernel struct layout mirrors of the linux/videodev2.h uapi. Field order and
// padding are verified at probe runtime against the sizes reported by the
// in-tree headers (see KernelReqs and the probe self-check).

type v4l2QueryExtCtrl struct { // struct v4l2_query_ext_ctrl
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

type v4l2ExtControl struct { // struct v4l2_ext_control (union at offset 12, no trailing padding)
	ID   uint32
	Size uint32
	_    uint32 // kernel reserves one word between size and the union
	VLo  uint32 // union low half: value (s32) / value64 low bits
	VHi  int32  // union high half: value64 high bits
}

// packValue splits a 64-bit control payload into the union halves.
func (c *v4l2ExtControl) packValue(v int64) {
	c.VLo = uint32(uint64(v))
	c.VHi = int32(v >> 32)
}

// value reassembles the union halves as the effective int64 payload.
func (c *v4l2ExtControl) value(is64 bool) int64 {
	if is64 {
		return int64(uint64(c.VLo) | uint64(uint32(c.VHi))<<32)
	}

	return int64(int32(c.VLo))
}

type v4l2ExtControls struct { // struct v4l2_ext_controls
	Which    uint32 // 0 == V4L2_CTRL_WHICH_CUR_VAL
	Count    uint32
	ErrorIdx uint32
	Reserved [2]uint32
	Controls unsafe.Pointer
}

type v4l2Control struct { // struct v4l2_control (legacy)
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

type v4l2Format struct { // struct v4l2_format (pix member; union is 8-aligned via v4l2_window)
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

type v4l2Streamparm struct { // struct v4l2_streamparm (capture member; union is 4-aligned here)
	Type    uint32
	Capture v4l2CaptureParm
	_       [200 - unsafe.Sizeof(v4l2CaptureParm{})]byte
}

// KernelReqs carries ioctl request numbers, struct sizes and key field
// offsets as computed by the in-tree kernel headers, for layout assertions
// and ioctl calls.
type KernelReqs struct {
	GFmt, SFmt, TryFmt          uint32
	GParm, SParm                uint32
	QueryExtCtrl, GExtCtrls     uint32
	SExtCtrls, GCtrl, SCtrl     uint32
	GInput, SInput              uint32
	SzQueryExtCtrl              uintptr
	SzExtControl, SzExtControls uintptr
	SzFormat, SzStreamparm      uintptr
	SzControl                   uintptr
	OffQecElemSize              uintptr
	OffQecElems                 uintptr
	OffQecDims                  uintptr
	OffExtCtrlValue             uintptr
	OffExtCtrlsControls         uintptr
}

// kernelReqs reads the values compiled from the real headers.
func kernelReqs() KernelReqs {
	r := C.v4l2_reqs

	return KernelReqs{
		GFmt:         uint32(r.g_fmt),
		SFmt:         uint32(r.s_fmt),
		TryFmt:       uint32(r.try_fmt),
		GParm:        uint32(r.g_parm),
		SParm:        uint32(r.s_parm),
		QueryExtCtrl: uint32(r.query_ext_ctrl),
		GExtCtrls:    uint32(r.g_ext_ctrls),
		SExtCtrls:    uint32(r.s_ext_ctrls),
		GCtrl:        uint32(r.g_ctrl),
		SCtrl:        uint32(r.s_ctrl),
		GInput:       uint32(r.g_input),
		SInput:       uint32(r.s_input),

		SzQueryExtCtrl:      uintptr(r.sz_query_ext_ctrl),
		SzExtControl:        uintptr(r.sz_ext_control),
		SzExtControls:       uintptr(r.sz_ext_controls),
		SzFormat:            uintptr(r.sz_format),
		SzStreamparm:        uintptr(r.sz_streamparm),
		SzControl:           uintptr(r.sz_control),
		OffQecElemSize:      uintptr(r.off_qec_elem_size),
		OffQecElems:         uintptr(r.off_qec_elems),
		OffQecDims:          uintptr(r.off_qec_dims),
		OffExtCtrlValue:     uintptr(r.off_ext_ctrl_value),
		OffExtCtrlsControls: uintptr(r.off_ext_ctrls_controls),
	}
}

// v4l2 control type values (include/uapi/linux/videodev2.h).
const (
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
)

// ctrlTypeName maps a V4L2 control type to a short human-readable name.
func ctrlTypeName(t uint32) string {
	switch t {
	case ctrlTypeInteger:

		return "INTEGER"
	case ctrlTypeBoolean:

		return "BOOLEAN"
	case ctrlTypeMenu:

		return "MENU"
	case ctrlTypeButton:

		return "BUTTON"
	case ctrlTypeInteger64:

		return "INTEGER64"
	case ctrlTypeCtrlClass:

		return "CTRL_CLASS"
	case ctrlTypeString:

		return "STRING"
	case ctrlTypeBitmask:

		return "BITMASK"
	case ctrlTypeIntegerMenu:

		return "INTEGER_MENU"
	case ctrlTypeU8:

		return "U8"
	case ctrlTypeU16:

		return "U16"
	case ctrlTypeU32:

		return "U32"
	default:

		return fmt.Sprintf("0x%04x", t)
	}
}

// V4L2 control flag bits (include/uapi/linux/videodev2.h).
const (
	ctrlFlagDisabled   = 0x0001
	ctrlFlagGrabbed    = 0x0002
	ctrlFlagReadOnly   = 0x0004
	ctrlFlagUpdate     = 0x0008
	ctrlFlagInactive   = 0x0010
	ctrlFlagSlider     = 0x0020
	ctrlFlagWriteOnly  = 0x0040
	ctrlFlagVolatile   = 0x0080
	ctrlFlagHasPayload = 0x0100
	ctrlFlagExecute    = 0x0200 // EXECUTE_ON_WRITE
	ctrlFlagModLayout  = 0x0400 // MODIFY_LAYOUT
	ctrlFlagDynArray   = 0x0800 // DYNAMIC_ARRAY
	ctrlFlagMinMax     = 0x1000 // HAS_WHICH_MIN_MAX (newer kernels; unknown bits must be tolerated)
	ctrlFlagNextCtrl   = 0x80000000
	ctrlFlagNextCompnd = 0x40000000
)

// bufTypeVideoCapture is V4L2_BUF_TYPE_VIDEO_CAPTURE.
const bufTypeVideoCapture = 1

// ioctl performs a raw V4L2 ioctl and converts the errno to an error.
func ioctl(fd uintptr, req uint32, arg unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, fd, uintptr(req), uintptr(arg)) //nolint:gosec // low-level V4L2 ioctl; fd/req/arg are validated by the callers

	if errno != 0 {
		return fmt.Errorf("ioctl 0x%08x: %w", req, errno)
	}

	return nil
}

// queryExtCtrl runs VIDIOC_QUERY_EXT_CTRL for one control id (which may carry
// NEXT_CTRL / NEXT_COMPOUND request flags).
func queryExtCtrl(fd uintptr, id uint32) (v4l2QueryExtCtrl, error) {
	q := v4l2QueryExtCtrl{ID: id}

	if err := ioctl(fd, kernelReqs().QueryExtCtrl, unsafe.Pointer(&q)); err != nil { //nolint:gosec // pointer to local uapi struct
		return v4l2QueryExtCtrl{}, err
	}

	return q, nil
}

// enumerateAllControls walks the full control tree using
// NEXT_CTRL | NEXT_COMPOUND on every iteration, covering simple and compound
// controls across all classes (user-class included).
func enumerateAllControls(fd uintptr) ([]v4l2QueryExtCtrl, error) {
	var result []v4l2QueryExtCtrl

	req := uint32(ctrlFlagNextCtrl | ctrlFlagNextCompnd)
	for {
		q, err := queryExtCtrl(fd, req)
		if err != nil {
			if len(result) > 0 {
				break
			}

			return nil, fmt.Errorf("first QUERY_EXT_CTRL failed: %w", err)
		}

		result = append(result, q)
		req = q.ID | ctrlFlagNextCtrl | ctrlFlagNextCompnd
	}

	return result, nil
}

// extControlValue reads one control via VIDIOC_G_EXT_CTRLS (single entry).
func extControlValue(fd uintptr, id uint32, is64 bool) (int64, error) {
	ctl := v4l2ExtControl{ID: id}
	ctrls := v4l2ExtControls{Count: 1, Controls: unsafe.Pointer(&ctl)} //nolint:gosec // pointer to local uapi struct

	if err := ioctl(fd, kernelReqs().GExtCtrls, unsafe.Pointer(&ctrls)); err != nil { //nolint:gosec // pointer to local uapi struct
		return 0, err
	}

	return ctl.value(is64), nil
}

// setExtControlValue writes one control via VIDIOC_S_EXT_CTRLS without any
// pre-query, so the kernel performs the clamping (matches v4l2-ctl behavior).
func setExtControlValue(fd uintptr, id uint32, is64 bool, value int64) error {
	ctl := v4l2ExtControl{ID: id}
	ctl.packValue(value)
	ctrls := v4l2ExtControls{Count: 1, Controls: unsafe.Pointer(&ctl)} //nolint:gosec // pointer to local uapi struct

	return ioctl(fd, kernelReqs().SExtCtrls, unsafe.Pointer(&ctrls)) //nolint:gosec // pointer to local uapi struct
}

// PixFmt is the effective pix-format view the probe works with.
type PixFmt struct {
	Width        uint32
	Height       uint32
	FourCC       string
	BytesPerLine uint32
	SizeImage    uint32
	Field        uint32
	Priv         uint32
}

// gFmt reads the currently negotiated format via VIDIOC_G_FMT.
func gFmt(fd uintptr) (PixFmt, error) {
	var f v4l2Format
	f.Type = bufTypeVideoCapture

	if err := ioctl(fd, kernelReqs().GFmt, unsafe.Pointer(&f)); err != nil { //nolint:gosec // pointer to local uapi struct
		return PixFmt{}, err
	}

	return PixFmt{
		Width:        f.Pix.Width,
		Height:       f.Pix.Height,
		FourCC:       fourccString(f.Pix.PixelFormat),
		BytesPerLine: f.Pix.BytesPerLine,
		SizeImage:    f.Pix.SizeImage,
		Field:        f.Pix.Field,
		Priv:         f.Pix.Priv,
	}, nil
}

// sFmt / tryFmt write or validate a format. tryFmt must never change state.
func sFmt(fd uintptr, pf PixFmt) (PixFmt, error) {
	return fmtIoctl(fd, kernelReqs().SFmt, pf)
}

func tryFmt(fd uintptr, pf PixFmt) (PixFmt, error) {
	return fmtIoctl(fd, kernelReqs().TryFmt, pf)
}

func fmtIoctl(fd uintptr, req uint32, pf PixFmt) (PixFmt, error) {
	var f v4l2Format
	f.Type = bufTypeVideoCapture
	f.Pix.Width = pf.Width
	f.Pix.Height = pf.Height
	f.Pix.PixelFormat = fourcc(pf.FourCC)
	f.Pix.BytesPerLine = pf.BytesPerLine
	f.Pix.SizeImage = pf.SizeImage
	f.Pix.Field = pf.Field

	if err := ioctl(fd, req, unsafe.Pointer(&f)); err != nil { //nolint:gosec // pointer to local uapi struct
		return PixFmt{}, err
	}

	return PixFmt{
		Width:        f.Pix.Width,
		Height:       f.Pix.Height,
		FourCC:       fourccString(f.Pix.PixelFormat),
		BytesPerLine: f.Pix.BytesPerLine,
		SizeImage:    f.Pix.SizeImage,
		Field:        f.Pix.Field,
		Priv:         f.Pix.Priv,
	}, nil
}

// StreamParm is the probe's view of the effective capture parameters.
type StreamParm struct {
	TimePerFrame    v4l2Fract
	CapTimePerFrame bool // capture.capability & V4L2_CAP_TIMEPERFRAME
	ReadBuffers     uint32
}

// gParm reads VIDIOC_G_PARM for the capture type.
func gParm(fd uintptr) (StreamParm, error) {
	var p v4l2Streamparm
	p.Type = bufTypeVideoCapture

	if err := ioctl(fd, kernelReqs().GParm, unsafe.Pointer(&p)); err != nil { //nolint:gosec // pointer to local uapi struct
		return StreamParm{}, err
	}

	return StreamParm{
		TimePerFrame:    p.Capture.TimePerFrame,
		CapTimePerFrame: p.Capture.Capability&0x1000 != 0, // V4L2_CAP_TIMEPERFRAME
		ReadBuffers:     p.Capture.ReadBuffers,
	}, nil
}

// sParm writes the frame interval (timeperframe) only.
func sParm(fd uintptr, tpf v4l2Fract) error {
	var p v4l2Streamparm
	p.Type = bufTypeVideoCapture
	p.Capture.TimePerFrame = tpf

	return ioctl(fd, kernelReqs().SParm, unsafe.Pointer(&p)) //nolint:gosec // pointer to local uapi struct
}

// gInput / sInput read and write the active input index.
func gInput(fd uintptr) (uint32, error) {
	var idx int32

	if err := ioctl(fd, kernelReqs().GInput, unsafe.Pointer(&idx)); err != nil { //nolint:gosec // pointer to local uapi struct
		return 0, err
	}

	return uint32(idx), nil
}

func sInput(fd uintptr, idx uint32) error {
	v := int32(idx)

	return ioctl(fd, kernelReqs().SInput, unsafe.Pointer(&v)) //nolint:gosec // pointer to local uapi struct
}

// fourcc encodes a 4-character FOURCC string to its little-endian u32 form.
func fourcc(s string) uint32 {
	if len(s) < 4 {
		return 0
	}

	return uint32(s[0]) | uint32(s[1])<<8 | uint32(s[2])<<16 | uint32(s[3])<<24
}

// fourccString decodes a u32 FOURCC to a printable string.
func fourccString(v uint32) string {
	return string([]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)})
}
