package v4l

import (
	"testing"
	"unsafe"
)

// TestStructLayouts asserts the Go mirrors against sizes AND decisive field
// offsets computed from the in-tree kernel headers. Offsets caught the
// missing elem_size field that sizes alone could not (D013).
func TestStructLayouts(t *testing.T) {
	r := ReqsOf()

	sizes := []struct {
		name string
		goSz uintptr
		cSz  uintptr
	}{
		{"v4l2_query_ext_ctrl", unsafe.Sizeof(v4l2QueryExtCtrl{}), r.SzQueryExtCtrl},
		{"v4l2_ext_control", unsafe.Sizeof(v4l2ExtControl{}), r.SzExtControl},
		{"v4l2_ext_controls", unsafe.Sizeof(v4l2ExtControls{}), r.SzExtControls},
		{"v4l2_format", unsafe.Sizeof(v4l2Format{}), r.SzFormat},
		{"v4l2_streamparm", unsafe.Sizeof(v4l2Streamparm{}), r.SzStreamparm},
		{"v4l2_capability", unsafe.Sizeof(v4l2Capability{}), r.SzCapability},
		{"v4l2_fmtdesc", unsafe.Sizeof(v4l2FmtDesc{}), r.SzFmtDesc},
		{"v4l2_frmsizeenum", unsafe.Sizeof(v4l2FrameSizeEnum{}), r.SzFrameSizeEnum},
		{"v4l2_frmivalenum", unsafe.Sizeof(v4l2FrameIvalEnum{}), r.SzFrameIvalEnum},
		{"v4l2_input", unsafe.Sizeof(v4l2Input{}), r.SzInput},
		{"v4l2_querymenu", unsafe.Sizeof(v4l2QueryMenu{}), r.SzQueryMenu},
	}

	for _, tc := range sizes {
		t.Run(tc.name, func(t *testing.T) {
			if tc.goSz != tc.cSz {
				t.Fatalf("size mismatch: Go=%d kernel=%d", tc.goSz, tc.cSz)
			}
		})
	}

	offsets := []struct {
		name  string
		goOff uintptr
		cOff  uintptr
	}{
		{"query_ext_ctrl.elem_size", unsafe.Offsetof(v4l2QueryExtCtrl{}.ElemSize), r.OffQecElemSize},
		{"query_ext_ctrl.elems", unsafe.Offsetof(v4l2QueryExtCtrl{}.Elems), r.OffQecElems},
		{"ext_control.value", unsafe.Offsetof(v4l2ExtControl{}.VLo), r.OffExtCtrlValue},
		{"ext_controls.controls", unsafe.Offsetof(v4l2ExtControls{}.Controls), r.OffExtCtrlsControls},
		{"fmtdesc.pixelformat", unsafe.Offsetof(v4l2FmtDesc{}.PixelFormat), r.OffFmtDescFourCC},
		{"frmsizeenum.stepwise", unsafe.Offsetof(v4l2FrameSizeEnum{}.Union), r.OffFrameSizeStep},
		{"frmivalenum.stepwise", unsafe.Offsetof(v4l2FrameIvalEnum{}.Union), r.OffFrameIvalStep},
		{"input.std", unsafe.Offsetof(v4l2Input{}.Std), r.OffInputStd},
		{"querymenu.name", unsafe.Offsetof(v4l2QueryMenu{}.Name), r.OffQueryMenuName},
	}

	for _, tc := range offsets {
		t.Run(tc.name, func(t *testing.T) {
			if tc.goOff != tc.cOff {
				t.Fatalf("offset mismatch: Go=%d kernel=%d", tc.goOff, tc.cOff)
			}
		})
	}
}

func TestExtControlValuePacking(t *testing.T) {
	var c v4l2ExtControl

	c.packValue(-5)
	if got := c.value(false); got != -5 {
		t.Fatalf("int32 round trip: %d", got)
	}

	var big v4l2ExtControl

	const v64 = int64(1<<40 + 12345)
	big.packValue(v64)

	if got := big.value(true); got != v64 {
		t.Fatalf("int64 round trip: %d want %d", got, v64)
	}
}

func TestFourCCRoundTrip(t *testing.T) {
	for _, s := range []string{"MJPG", "UYVY", "YUYV", "NV12", "NV21", "I420"} {
		if got := FourCCString(FourCC(s)); got != s {
			t.Fatalf("fourcc %s -> %s", s, got)
		}
	}
}

func TestCaptureCapsOf(t *testing.T) {
	single, mplane, meta := CaptureCapsOf(0x04200001) // PW513 shape

	if !single || mplane || meta {
		t.Fatalf("PW513 caps wrong: single=%v mplane=%v meta=%v", single, mplane, meta)
	}

	single, mplane, meta = CaptureCapsOf(0x04a01001) // mplane + metadata + ext fmt

	if !single || !mplane || !meta {
		t.Fatalf("mplane caps wrong: single=%v mplane=%v meta=%v", single, mplane, meta)
	}
}
