package v4l

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ---- extended controls (local, faithful int64) ----

// queryExtCtrl runs VIDIOC_QUERY_EXT_CTRL for one control id, which may
// carry NEXT_CTRL/NEXT_COMPOUND request flags.
func queryExtCtrl(fd uintptr, id uint32) (v4l2QueryExtCtrl, error) {
	q := v4l2QueryExtCtrl{ID: id}

	if err := ioctl(fd, ReqsOf().QueryExtCtrl, unsafe.Pointer(&q)); err != nil { //nolint:gosec // pointer to local uapi struct
		return v4l2QueryExtCtrl{}, err
	}

	return q, nil
}

// enumerateAllControls walks the whole control tree, ORing
// NEXT_CTRL|NEXT_COMPOUND on every iteration so simple and compound
// controls across all classes are covered (D013).
func enumerateAllControls(fd uintptr) ([]v4l2QueryExtCtrl, error) {
	var result []v4l2QueryExtCtrl

	req := uint32(ctrlFlagNextCtrl | ctrlFlagNextCompnd)

	for {
		q, err := queryExtCtrl(fd, req)
		if err != nil {
			if len(result) > 0 {
				break
			}

			return nil, err
		}

		result = append(result, q)
		req = q.ID | ctrlFlagNextCtrl | ctrlFlagNextCompnd
	}

	return result, nil
}

// queryMenu runs VIDIOC_QUERYMENU for one menu index. Returns
// unix.EINVAL-shaped error when the index is absent.
func queryMenu(fd uintptr, id uint32, index int64) (v4l2QueryMenu, error) {
	m := v4l2QueryMenu{ID: id, Index: uint32(index)} //nolint:gosec // uapi menu indices are u32; bounded by the 4096-entry walk guard

	if err := ioctl(fd, ReqsOf().QueryMenu, unsafe.Pointer(&m)); err != nil { //nolint:gosec // pointer to local uapi struct
		return v4l2QueryMenu{}, err
	}

	return m, nil
}

// extControlValue reads one control via G_EXT_CTRLS (single entry).
func extControlValue(fd uintptr, id uint32, is64 bool) (int64, error) {
	ctl := v4l2ExtControl{ID: id}
	ctrls := v4l2ExtControls{Count: 1, Controls: unsafe.Pointer(&ctl)} //nolint:gosec // pointer to local uapi struct

	if err := ioctl(fd, ReqsOf().GExtCtrls, unsafe.Pointer(&ctrls)); err != nil { //nolint:gosec // pointer to local uapi struct
		return 0, err
	}

	return ctl.value(is64), nil
}

// extControlFlags reads the current live flags via QUERY_EXT_CTRL.
func extControlFlags(fd uintptr, id uint32) (uint32, error) {
	q, err := queryExtCtrl(fd, id)
	if err != nil {
		return 0, err
	}

	return q.Flags, nil
}

// setExtControlValue writes one control via S_EXT_CTRLS without a
// pre-query: the kernel clamps, and the caller reads back the effective
// value when the control is readable (matches v4l2-ctl behavior, D013).
// The value union packs both s32 and s64 arms identically, so the control
// type needs no separate handling here.
func setExtControlValue(fd uintptr, id uint32, value int64, str []byte) error {
	ctl := v4l2ExtControl{ID: id}
	ctl.packValue(value)

	if str != nil {
		ctl.Size = uint32(len(str)) //nolint:gosec // payload size from the caller-owned string
	}

	ctrls := v4l2ExtControls{Count: 1, Controls: unsafe.Pointer(&ctl)} //nolint:gosec // pointer to local uapi struct

	if str != nil {
		// String controls carry their payload through ctrl.size and a
		// char pointer in the union; the uapi uses a separate __user
		// pointer arm, addressed via the same 8-byte union slot.
		*(*unsafe.Pointer)(unsafe.Pointer(&ctl.VLo)) = unsafe.Pointer(&str[0]) //nolint:gosec // pointer to caller-owned payload
	}

	return ioctl(fd, ReqsOf().SExtCtrls, unsafe.Pointer(&ctrls)) //nolint:gosec // pointer to local uapi struct
}

// ---- format / parm / input ----

// PixFormat is the effective pix-format view (D013: S_FMT negotiates —
// always read the result back, requested != effective).
type PixFormat struct {
	Width        uint32
	Height       uint32
	FourCC       string
	BytesPerLine uint32
	SizeImage    uint32
	Field        uint32
	Priv         uint32
}

func pixToKernel(pf PixFormat, f *v4l2Format) {
	f.Type = bufTypeVideoCapture
	f.Pix.Width = pf.Width
	f.Pix.Height = pf.Height
	f.Pix.PixelFormat = FourCC(pf.FourCC)
	f.Pix.BytesPerLine = pf.BytesPerLine
	f.Pix.SizeImage = pf.SizeImage
	f.Pix.Field = pf.Field
}

func pixFromKernel(f *v4l2Format) PixFormat {
	return PixFormat{
		Width:        f.Pix.Width,
		Height:       f.Pix.Height,
		FourCC:       FourCCString(f.Pix.PixelFormat),
		BytesPerLine: f.Pix.BytesPerLine,
		SizeImage:    f.Pix.SizeImage,
		Field:        f.Pix.Field,
		Priv:         f.Pix.Priv,
	}
}

// GetFormat reads the negotiated format via G_FMT.
func GetFormat(fd uintptr) (PixFormat, error) {
	var f v4l2Format
	f.Type = bufTypeVideoCapture

	if err := ioctl(fd, ReqsOf().GFmt, unsafe.Pointer(&f)); err != nil { //nolint:gosec // pointer to local uapi struct
		return PixFormat{}, err
	}

	return pixFromKernel(&f), nil
}

// SetFormat issues S_FMT and returns the driver-adjusted result.
func SetFormat(fd uintptr, pf PixFormat) (PixFormat, error) {
	return formatIoctl(fd, ReqsOf().SFmt, pf)
}

// TryFormat issues TRY_FMT: pure validation, no state change.
func TryFormat(fd uintptr, pf PixFormat) (PixFormat, error) {
	return formatIoctl(fd, ReqsOf().TryFmt, pf)
}

func formatIoctl(fd uintptr, req uint32, pf PixFormat) (PixFormat, error) {
	var f v4l2Format
	pixToKernel(pf, &f)

	if err := ioctl(fd, req, unsafe.Pointer(&f)); err != nil { //nolint:gosec // pointer to local uapi struct
		return PixFormat{}, err
	}

	return pixFromKernel(&f), nil
}

// StreamParm is the effective capture-parameter view.
type StreamParm struct {
	TimePerFrame    [2]uint32 // numerator, denominator
	CapTimePerFrame bool      // capture.capability & V4L2_CAP_TIMEPERFRAME
	ReadBuffers     uint32
}

// GetParm reads G_PARM for the capture type.
func GetParm(fd uintptr) (StreamParm, error) {
	var p v4l2Streamparm
	p.Type = bufTypeVideoCapture

	if err := ioctl(fd, ReqsOf().GParm, unsafe.Pointer(&p)); err != nil { //nolint:gosec // pointer to local uapi struct
		return StreamParm{}, err
	}

	return StreamParm{
		TimePerFrame:    [2]uint32{p.Capture.TimePerFrame.Numerator, p.Capture.TimePerFrame.Denominator},
		CapTimePerFrame: p.Capture.Capability&parmCapTimePerFrame != 0,
		ReadBuffers:     p.Capture.ReadBuffers,
	}, nil
}

// SetParm writes the frame interval (timeperframe) only. Callers gate this
// on CapTimePerFrame; 0/0 is never sent.
func SetParm(fd uintptr, num, den uint32) error {
	var p v4l2Streamparm
	p.Type = bufTypeVideoCapture
	p.Capture.TimePerFrame = v4l2Fract{Numerator: num, Denominator: den}

	return ioctl(fd, ReqsOf().SParm, unsafe.Pointer(&p)) //nolint:gosec // pointer to local uapi struct
}

// GetInput reads the active input index.
func GetInput(fd uintptr) (uint32, error) {
	var idx int32

	if err := ioctl(fd, ReqsOf().GInput, unsafe.Pointer(&idx)); err != nil { //nolint:gosec // pointer to local uapi struct
		return 0, err
	}

	return uint32(idx), nil //nolint:gosec // G_INPUT yields a signed uapi index
}

// SetInput selects the input index. Never used for discovery (D013).
func SetInput(fd uintptr, idx uint32) error {
	v := int32(idx) //nolint:gosec // S_INPUT takes a signed uapi index

	return ioctl(fd, ReqsOf().SInput, unsafe.Pointer(&v)) //nolint:gosec // pointer to local uapi struct
}

// ---- capabilities ----

// Capabilities mirrors the QUERYCAP bits V4Lens classifies on.
type Capabilities struct {
	Driver     string
	Card       string
	BusInfo    string
	Caps       uint32
	DeviceCaps uint32
}

// QueryCap runs VIDIOC_QUERYCAP.
func QueryCap(fd uintptr) (Capabilities, error) {
	var c v4l2Capability

	if err := ioctl(fd, ReqsOf().QueryCap, unsafe.Pointer(&c)); err != nil { //nolint:gosec // pointer to local uapi struct
		return Capabilities{}, err
	}

	return Capabilities{
		Driver:     cString(c.Driver[:]),
		Card:       cString(c.Card[:]),
		BusInfo:    cString(c.BusInfo[:]),
		Caps:       c.Caps,
		DeviceCaps: c.DeviceCaps,
	}, nil
}

// CaptureCapsOf classifies device_caps into the domain capture caps.
func CaptureCapsOf(devCaps uint32) (single, mplane, meta bool) {
	return devCaps&capVideoCapture != 0,
		devCaps&capVideoCaptureMCore != 0,
		devCaps&capMetaCapture != 0
}

// ---- enumerations ----

// FmtDesc is one ENUM_FMT entry.
type FmtDesc struct {
	Index       uint32
	FourCC      string
	Description string
	Compressed  bool
	Emulated    bool
}

// EnumFormats lists the pixel formats of the capture buffer type. Formats
// describe the currently active input; inputs are never switched for
// discovery (D013).
func EnumFormats(fd uintptr) ([]FmtDesc, error) {
	var out []FmtDesc

	for idx := uint32(0); idx < 128; idx++ {
		var d v4l2FmtDesc
		d.Index = idx
		d.Type = bufTypeVideoCapture

		if err := ioctl(fd, ReqsOf().EnumFmt, unsafe.Pointer(&d)); err != nil { //nolint:gosec // pointer to local uapi struct
			if len(out) > 0 {
				break
			}

			return nil, err
		}

		out = append(out, FmtDesc{
			Index:       d.Index,
			FourCC:      FourCCString(d.PixelFormat),
			Description: cString(d.Description[:]),
			Compressed:  d.Flags&fmtDescFlagCompressed != 0,
			Emulated:    d.Flags&fmtDescFlagEmulated != 0,
		})
	}

	return out, nil
}

// FrameSize is one ENUM_FRAMESIZES entry in its native form.
type FrameSize struct {
	IsDiscrete        bool
	Width, Height     uint32 // discrete
	MinW, MaxW, StepW uint32 // stepwise/continuous
	MinH, MaxH, StepH uint32
}

// EnumFrameSizes lists frame sizes for one pixel format.
func EnumFrameSizes(fd uintptr, fourcc string) ([]FrameSize, error) {
	var out []FrameSize

	cc := FourCC(fourcc)

	for idx := uint32(0); idx < 256; idx++ {
		var e v4l2FrameSizeEnum
		e.Index = idx
		e.PixelFormat = cc

		if err := ioctl(fd, ReqsOf().EnumFrameSizes, unsafe.Pointer(&e)); err != nil { //nolint:gosec // pointer to local uapi struct
			if len(out) > 0 {
				break
			}

			return nil, err
		}

		switch e.Type {
		case fmtSizeTypeDiscrete:
			d := e.discrete()
			out = append(out, FrameSize{IsDiscrete: true, Width: d.Width, Height: d.Height})
		case fmtSizeTypeStepwise, fmtSizeTypeContinuous:
			s := e.stepwise()
			out = append(out, FrameSize{
				IsDiscrete: false,
				MinW:       s.MinWidth, MaxW: s.MaxWidth, StepW: s.StepWidth,
				MinH: s.MinHeight, MaxH: s.MaxHeight, StepH: s.StepHeight,
			})
		default:

			return nil, fmt.Errorf("unknown frame size type %d", e.Type)
		}
	}

	return out, nil
}

// FrameInterval is one ENUM_FRAMEINTERVALS entry in its native form.
type FrameInterval struct {
	IsDiscrete       bool
	Num, Den         uint32 // discrete
	MinNum, MinDen   uint32 // stepwise/continuous
	MaxNum, MaxDen   uint32
	StepNum, StepDen uint32
}

// EnumFrameIntervals lists frame intervals for one concrete format+size
// (lazy capability query; requires a validated W/H).
func EnumFrameIntervals(fd uintptr, fourcc string, w, h uint32) ([]FrameInterval, error) {
	var out []FrameInterval

	cc := FourCC(fourcc)

	for idx := uint32(0); idx < 256; idx++ {
		var e v4l2FrameIvalEnum
		e.Index = idx
		e.PixelFormat = cc
		e.Width = w
		e.Height = h

		if err := ioctl(fd, ReqsOf().EnumFrameIv, unsafe.Pointer(&e)); err != nil { //nolint:gosec // pointer to local uapi struct
			if len(out) > 0 {
				break
			}

			return nil, err
		}

		switch e.Type {
		case fmtIvalTypeDiscrete:
			d := e.discrete()
			out = append(out, FrameInterval{IsDiscrete: true, Num: d.Numerator, Den: d.Denominator})
		case fmtIvalTypeStepwise, fmtIvalTypeContinuous:
			s := e.stepwise()
			out = append(out, FrameInterval{
				IsDiscrete: false,
				MinNum:     s.Min.Numerator, MinDen: s.Min.Denominator,
				MaxNum: s.Max.Numerator, MaxDen: s.Max.Denominator,
				StepNum: s.Step.Numerator, StepDen: s.Step.Denominator,
			})

			return out, nil // stepwise/continuous is a single range entry
		default:

			return nil, fmt.Errorf("unknown frame interval type %d", e.Type)
		}
	}

	return out, nil
}

// Input is one ENUMINPUT entry.
type Input struct {
	Index  uint32
	Name   string
	Camera bool
}

// EnumInputs lists the device inputs (independent enumeration; no S_INPUT).
func EnumInputs(fd uintptr) ([]Input, error) {
	var out []Input

	for idx := uint32(0); idx < 32; idx++ {
		var i v4l2Input
		i.Index = idx

		if err := ioctl(fd, ReqsOf().EnumInput, unsafe.Pointer(&i)); err != nil { //nolint:gosec // pointer to local uapi struct
			if len(out) > 0 {
				break
			}

			return nil, err
		}

		out = append(out, Input{Index: i.Index, Name: cString(i.Name[:]), Camera: i.Type == inputTypeCamera})
	}

	return out, nil
}

// ---- helpers ----

// FourCC encodes a FOURCC string into its little-endian u32 form.
func FourCC(s string) uint32 {
	if len(s) < 4 {
		return 0
	}

	return uint32(s[0]) | uint32(s[1])<<8 | uint32(s[2])<<16 | uint32(s[3])<<24
}

// FourCCString decodes a u32 FOURCC into a printable string.
func FourCCString(v uint32) string {
	return string([]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}) //nolint:gosec // intentional FOURCC byte packing
}

func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}

	return string(b)
}

// IsBusy reports whether err is the EBUSY the kernel returns for capture
// configuration conflicts (ARCHITECTURE §9: busy comes only from operation
// results).
func IsBusy(err error) bool {
	return errors.Is(err, unix.EBUSY)
}
