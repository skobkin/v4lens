//go:build probe

package probe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/vladimirvivien/go4vl/device"
	"github.com/vladimirvivien/go4vl/v4l2"
	"golang.org/x/sys/unix"
)

const defaultProbeDevice = "/dev/video0"

// TestStructLayouts asserts that the Go mirrors of the V4L2 uapi structs match
// the sizes computed from the in-tree kernel headers. Any mismatch means the
// raw ioctl wrappers would fail with ENOTTY or corrupt kernel memory.
func TestStructLayouts(t *testing.T) {
	reqs := kernelReqs()

	cases := []struct {
		name string
		goSz uintptr
		cSz  uintptr
	}{
		{"v4l2_query_ext_ctrl", unsafe.Sizeof(v4l2QueryExtCtrl{}), reqs.SzQueryExtCtrl},
		{"v4l2_ext_control", unsafe.Sizeof(v4l2ExtControl{}), reqs.SzExtControl},
		{"v4l2_ext_controls", unsafe.Sizeof(v4l2ExtControls{}), reqs.SzExtControls},
		{"v4l2_control", unsafe.Sizeof(v4l2Control{}), reqs.SzControl},
		{"v4l2_format", unsafe.Sizeof(v4l2Format{}), reqs.SzFormat},
		{"v4l2_streamparm", unsafe.Sizeof(v4l2Streamparm{}), reqs.SzStreamparm},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.goSz != tc.cSz {
				t.Fatalf("layout mismatch: Go sizeof=%d kernel sizeof=%d", tc.goSz, tc.cSz)
			}
		})
	}

	// Field offsets: sizes alone cannot catch a missing or reordered middle
	// field (the elem_size bug exactly), so assert the decisive offsets too.
	offsets := []struct {
		name   string
		goOff  uintptr
		kernOk uintptr
	}{
		{"query_ext_ctrl.elem_size", unsafe.Offsetof(v4l2QueryExtCtrl{}.ElemSize), reqs.OffQecElemSize},
		{"query_ext_ctrl.elems", unsafe.Offsetof(v4l2QueryExtCtrl{}.Elems), reqs.OffQecElems},
		{"query_ext_ctrl.dims", unsafe.Offsetof(v4l2QueryExtCtrl{}.Dims), reqs.OffQecDims},
		{"ext_control.value", unsafe.Offsetof(v4l2ExtControl{}.VLo), reqs.OffExtCtrlValue},
		{"ext_controls.controls", unsafe.Offsetof(v4l2ExtControls{}.Controls), reqs.OffExtCtrlsControls},
	}

	for _, tc := range offsets {
		t.Run(tc.name, func(t *testing.T) {
			if tc.goOff != tc.kernOk {
				t.Fatalf("offset mismatch: Go offset=%d kernel offset=%d", tc.goOff, tc.kernOk)
			}
		})
	}
}

// TestDeviceProbe runs the full state-preserving real-device probe and prints
// the findings used for the Phase 0 decision log. It skips automatically when
// the device does not exist.
func TestDeviceProbe(t *testing.T) {
	devPath := devicePath()

	if _, err := os.Stat(devPath); err != nil {
		t.Skipf("probe device %s not available", devPath)
	}

	// Raw control fd: opened once, used only for panel/control ioctls.
	rawFD, err := os.OpenFile(devPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open raw fd: %v", err)
	}

	defer func() { _ = rawFD.Close() }()
	fd := rawFD.Fd()

	goDev, err := device.Open(devPath)
	if err != nil {
		t.Fatalf("go4vl open: %v", err)
	}

	defer func() { _ = goDev.Close() }()

	capability := goDev.Capability()
	t.Logf("driver=%q card=%q bus=%q", strings.TrimSpace(capability.Driver), strings.TrimSpace(capability.Card), strings.TrimSpace(capability.BusInfo))
	t.Logf("capabilities=0x%08x device_caps=0x%08x", capability.Capabilities, capability.DeviceCapabilities)

	caps := capability.DeviceCapabilities
	t.Logf("single-planar capture: %v", caps&0x00000001 != 0) // V4L2_CAP_VIDEO_CAPTURE
	t.Logf("multi-planar capture:  %v", caps&0x00001000 != 0) // V4L2_CAP_VIDEO_CAPTURE_MPLANE
	t.Logf("metadata capture:      %v", caps&0x00800000 != 0) // V4L2_CAP_META_CAPTURE

	// --- snapshot everything the probe may touch -------------------------------
	snapInput, err := gInput(fd)
	if err != nil {
		t.Logf("G_INPUT: %v", err)
	}

	snapFmt, err := gFmt(fd)
	if err != nil {
		t.Fatalf("G_FMT: %v", err)
	}

	snapParm, err := gParm(fd)
	if err != nil {
		t.Logf("G_PARM: %v", err)
	}

	t.Logf("snapshot: input=%d fmt=%s %dx%d bpl=%d sizeimage=%d priv=%d tpf=%d/%d timeperframe_cap=%v",
		snapInput, snapFmt.FourCC, snapFmt.Width, snapFmt.Height, snapFmt.BytesPerLine,
		snapFmt.SizeImage, snapFmt.Priv, snapParm.TimePerFrame.Numerator, snapParm.TimePerFrame.Denominator,
		snapParm.CapTimePerFrame)

	defer func() {
		t.Helper()

		if snapParm.CapTimePerFrame {
			if err := sParm(fd, snapParm.TimePerFrame); err != nil {
				t.Errorf("RESTORE FAILED: S_PARM %d/%d: %v", snapParm.TimePerFrame.Numerator, snapParm.TimePerFrame.Denominator, err)
			}
		}

		if err := sInput(fd, snapInput); err != nil {
			t.Errorf("RESTORE FAILED: S_INPUT %d: %v", snapInput, err)
		}

		// Format restore runs on a short-lived fd which is closed immediately,
		// releasing stream ownership (the documented V4L2 ownership model).
		if _, err := sFmtOnShortLivedFd(devPath, snapFmt); err != nil {
			t.Errorf("RESTORE FAILED: S_FMT %s %dx%d: %v", snapFmt.FourCC, snapFmt.Width, snapFmt.Height, err)
		}

		got, err := gFmt(fd)
		if err != nil {
			t.Errorf("RESTORE VERIFY FAILED: G_FMT: %v", err)

			return
		}

		if got != snapFmt {
			t.Errorf("RESTORE VERIFY FAILED: got %+v want %+v", got, snapFmt)
		}
	}()

	// --- full control enumeration (NEXT_CTRL | NEXT_COMPOUND) ------------------
	ctrls, err := enumerateAllControls(fd)
	if err != nil {
		t.Fatalf("control enumeration: %v", err)
	}

	t.Logf("enumerated %d controls via NEXT_CTRL|NEXT_COMPOUND:", len(ctrls))

	for _, c := range ctrls {
		t.Logf("  id=0x%08x type=%-13s flags=0x%06x elems=%d elem_size=%d min=%d max=%d step=%d def=%d name=%q",
			c.ID, ctrlTypeName(c.Type), c.Flags, c.Elems, c.ElemSize,
			c.Minimum, c.Maximum, c.Step, c.Default, cString(c.Name[:]))
	}

	// go4vl comparison: what does its QueryAllExtControls see?
	go4Ctrls, err := v4l2.QueryAllExtControls(fd)
	if err != nil {
		t.Logf("go4vl QueryAllExtControls failed: %v", err)
	} else {
		seen := make([]uint32, 0, len(ctrls))

		for _, c := range ctrls {
			if c.Type != ctrlTypeCtrlClass {
				seen = append(seen, c.ID)
			}
		}

		go4Seen := make([]uint32, 0, len(go4Ctrls))

		for _, c := range go4Ctrls {
			go4Seen = append(go4Seen, uint32(c.ID))
		}

		missing := make([]uint32, 0)

		for _, id := range seen {
			if !slices.Contains(go4Seen, id) {
				missing = append(missing, id)
			}
		}

		t.Logf("go4vl QueryAllExtControls: %d controls; missed by go4vl: %d", len(go4Ctrls), len(missing))

		for _, id := range missing {
			t.Logf("  missed id=0x%08x", id)
		}
	}

	// --- write/read-back on brightness (state-preserving) ----------------------
	if err := probeControlWrite(t, fd, cidBrightness); err != nil {
		t.Logf("brightness write probe: %v", err)
	}

	// --- capability trees -------------------------------------------------------
	descs, err := goDev.GetFormatDescriptions()
	if err != nil {
		t.Fatalf("ENUM_FMT: %v", err)
	}

	for _, d := range descs {
		cc := uint32(d.PixelFormat)
		sizes, err := v4l2.GetFormatFrameSizes(fd, d.PixelFormat)
		if err != nil {
			t.Logf("format %s (%q): frame sizes: %v", fourccString(cc), strings.TrimSpace(d.Description), err)

			continue
		}

		for _, s := range sizes {
			if s.Type == v4l2.FrameSizeTypeDiscrete {
				t.Logf("format %s: size %dx%d intervals: %s",
					fourccString(cc), s.Size.MinWidth, s.Size.MinHeight,
					intervalList(fd, d.PixelFormat, s.Size.MinWidth, s.Size.MinHeight))

				continue
			}

			t.Logf("format %s: %s min=%dx%d max=%dx%d step=%dx%d",
				fourccString(cc), frmsizeTypeName(s.Type),
				s.Size.MinWidth, s.Size.MinHeight, s.Size.MaxWidth, s.Size.MaxHeight,
				s.Size.StepWidth, s.Size.StepHeight)
		}
	}

	// --- streaming: capture ~200 frames, record effective layout ----------------
	probeStream(t, devPath)

	// --- fd-ownership probes ----------------------------------------------------
	probeFDOwnership(t, devPath, fd)

	// --- external-owner conflict: S_FMT/TRY_FMT while a stream is live ----------
	probeExternalStreamConflict(t, devPath)

	// --- effective layout after everything -------------------------------------
	finalFmt, err := gFmt(fd)
	if err != nil {
		t.Fatalf("final G_FMT: %v", err)
	}

	t.Logf("effective format after probes: %s %dx%d bpl=%d sizeimage=%d",
		finalFmt.FourCC, finalFmt.Width, finalFmt.Height, finalFmt.BytesPerLine, finalFmt.SizeImage)
}

const cidBrightness = 0x00980900

// probeControlWrite snapshots, mutates, reads back and restores one control.
func probeControlWrite(t *testing.T, fd uintptr, id uint32) error {
	t.Helper()

	q, err := queryExtCtrl(fd, id)
	if err != nil {
		return fmt.Errorf("control 0x%08x not present: %w", id, err)
	}

	if q.Flags&ctrlFlagWriteOnly != 0 || q.Flags&ctrlFlagReadOnly != 0 {
		return fmt.Errorf("control 0x%08x flags 0x%x not suitable for write/read-back probe", id, q.Flags)
	}

	is64 := q.Type == ctrlTypeInteger64

	before, err := extControlValue(fd, id, is64)
	if err != nil {
		return fmt.Errorf("read before write: %w", err)
	}

	target := before + q.Step
	if target > q.Maximum {
		target = before - q.Step
	}

	if err := setExtControlValue(fd, id, is64, target); err != nil {
		return fmt.Errorf("write %d: %w", target, err)
	}

	after, err := extControlValue(fd, id, is64)
	if err != nil {
		return fmt.Errorf("read back after write: %w", err)
	}

	t.Logf("control 0x%08x: %d -> %d -> readback %d (min=%d max=%d)", id, before, target, after, q.Minimum, q.Maximum)

	// restore
	if err := setExtControlValue(fd, id, is64, before); err != nil {
		t.Errorf("RESTORE FAILED: control 0x%08x -> %d: %v", id, before, err)

		return nil
	}

	restored, err := extControlValue(fd, id, is64)
	if err != nil {
		t.Errorf("RESTORE VERIFY FAILED: control 0x%08x read: %v", id, err)

		return nil
	}

	if restored != before {
		t.Errorf("RESTORE VERIFY FAILED: control 0x%08x is %d, want %d", id, restored, before)
	}

	return nil
}

// probeStream opens the streaming handle (format applied at open by the
// driver negotiation), consumes frames with copy-then-Release discipline and
// records the effective layout.
func probeStream(t *testing.T, devPath string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	streamDev, err := device.Open(devPath)
	if err != nil {
		t.Fatalf("stream open: %v", err)
	}

	defer func() { _ = streamDev.Close() }()

	pf, err := streamDev.GetPixFormat()
	if err != nil {
		t.Fatalf("stream GetPixFormat: %v", err)
	}

	t.Logf("stream handle negotiated: %s %dx%d bpl=%d sizeimage=%d",
		fourccString(uint32(pf.PixelFormat)), pf.Width, pf.Height, pf.BytesPerLine, pf.SizeImage)

	if err := streamDev.Start(ctx); err != nil {
		t.Fatalf("stream start: %v", err)
	}

	frames := streamDev.GetFrames()
	want := 200
	got := 0
	var gaps int
	lastSeq := ^uint32(0)

	for got < want {
		select {
		case frame, ok := <-frames:
			if !ok {
				t.Fatalf("frame channel closed after %d frames", got)
			}

			if lastSeq != ^uint32(0) && frame.Sequence != lastSeq+1 {
				gaps += int(frame.Sequence - lastSeq - 1)
			}

			lastSeq = frame.Sequence
			frame.Release()
			got++
		case err := <-streamDev.GetError():
			t.Fatalf("stream error after %d frames: %v", got, err)
		case <-ctx.Done():
			t.Logf("stopped at %d frames (timeout)", got)
			_ = streamDev.Stop()

			return
		}
	}

	_ = streamDev.Stop()
	t.Logf("captured %d frames, sequence gaps: %d", got, gaps)
}

// probeExternalStreamConflict attempts capture reconfiguration from a second
// fd WHILE a stream is actively producing buffers on the first fd — the
// external-owner scenario V4Lens must never disrupt. Findings are
// informational; the architecture classifies whatever the driver returns.
func probeExternalStreamConflict(t *testing.T, devPath string) {
	t.Helper()

	streamDev, err := device.Open(devPath)
	if err != nil {
		t.Fatalf("conflict stream open: %v", err)
	}

	defer func() { _ = streamDev.Close() }()

	if err := streamDev.Start(context.Background()); err != nil {
		t.Fatalf("conflict stream start: %v", err)
	}

	defer func() { _ = streamDev.Stop() }()

	// Drain a few frames so the vb2 queue is actively cycling.
	frames := streamDev.GetFrames()

	for range 5 {
		select {
		case fr, ok := <-frames:
			if !ok {
				t.Fatalf("conflict stream closed early")
			}

			fr.Release()
		case <-time.After(2 * time.Second):
			t.Fatalf("conflict stream produced no frames")
		}
	}

	ctrlFD, err := os.OpenFile(devPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("conflict probe open: %v", err)
	}

	defer func() { _ = ctrlFD.Close() }()

	current, err := gFmt(ctrlFD.Fd())
	if err != nil {
		t.Fatalf("conflict G_FMT: %v", err)
	}

	// S_FMT from a foreign fd while the stream runs: the driver must reject
	// this (EBUSY) or the external stream gets disrupted — both are recorded.
	attempted := current
	attempted.Width = 640
	attempted.Height = 480

	res, fmtErr := sFmt(ctrlFD.Fd(), attempted)
	t.Logf("conflict: S_FMT %dx%d from second fd WHILE STREAMING: err=%v busy=%v result=%s %dx%d",
		attempted.Width, attempted.Height, fmtErr, isBusy(fmtErr), res.FourCC, res.Width, res.Height)

	eff, err := gFmt(ctrlFD.Fd())
	if err != nil {
		t.Fatalf("conflict effective G_FMT: %v", err)
	}

	t.Logf("conflict: effective format while streaming: %s %dx%d (stream frames still arriving)", eff.FourCC, eff.Width, eff.Height)

	// TRY_FMT must stay a harmless validation even with the stream live.
	tryRes, tryErr := tryFmt(ctrlFD.Fd(), attempted)
	t.Logf("conflict: TRY_FMT %dx%d while streaming: err=%v busy=%v result=%s %dx%d",
		attempted.Width, attempted.Height, tryErr, isBusy(tryErr), tryRes.FourCC, tryRes.Width, tryRes.Height)
}

// probeFDOwnership records how this driver behaves when capture configuration
// is attempted from fds that do not own a stream. Findings are informational;
// the architecture does not depend on this driver's tolerance.
func probeFDOwnership(t *testing.T, devPath string, ctrlFD uintptr) {
	t.Helper()

	current, err := gFmt(ctrlFD)
	if err != nil {
		t.Fatalf("G_FMT before ownership probes: %v", err)
	}

	// 1. short-lived fd performing S_FMT then closing (our detached-apply shape)
	shortFmt, shortErr := sFmtOnShortLivedFd(devPath, current)
	t.Logf("ownership: S_FMT on short-lived fd (same format): err=%v busy=%v result=%s %dx%d bpl=%d",
		shortErr, isBusy(shortErr), shortFmt.FourCC, shortFmt.Width, shortFmt.Height, shortFmt.BytesPerLine)

	// 2. TRY_FMT from a second fd (validation only, must not mutate)
	tryFD, err := os.OpenFile(devPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("ownership probe open: %v", err)
	}

	tryRes, tryErr := tryFmt(tryFD.Fd(), current)
	t.Logf("ownership: TRY_FMT from second fd: err=%v busy=%v result=%s %dx%d bpl=%d",
		tryErr, isBusy(tryErr), tryRes.FourCC, tryRes.Width, tryRes.Height, tryRes.BytesPerLine)
	_ = tryFD.Close()

	// 3. S_FMT with a DIFFERENT format from a plain second fd, then verify the
	// effective format on the control fd and restore the original afterwards.
	different := current
	if different.Height != 480 {
		different.Height = 480
	} else {
		different.Height = 720
	}

	secondFD, err := os.OpenFile(devPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("ownership probe open: %v", err)
	}

	mutRes, mutErr := sFmt(secondFD.Fd(), different)
	t.Logf("ownership: S_FMT (different %dx%d) from plain second fd: err=%v busy=%v result=%s %dx%d bpl=%d",
		different.Width, different.Height, mutErr, isBusy(mutErr),
		mutRes.FourCC, mutRes.Width, mutRes.Height, mutRes.BytesPerLine)
	_ = secondFD.Close()

	after, err := gFmt(ctrlFD)
	if err != nil {
		t.Fatalf("G_FMT after ownership probes: %v", err)
	}

	t.Logf("ownership: effective format on control fd after external S_FMT: %s %dx%d bpl=%d",
		after.FourCC, after.Width, after.Height, after.BytesPerLine)
}

// sFmtOnShortLivedFd opens the node, applies fmt, reads back the effective
// format and closes the fd, releasing ownership again.
func sFmtOnShortLivedFd(devPath string, fmture PixFmt) (PixFmt, error) {
	fd, err := os.OpenFile(devPath, os.O_RDWR, 0)
	if err != nil {
		return PixFmt{}, fmt.Errorf("open: %w", err)
	}

	defer func() { _ = fd.Close() }()

	got, err := sFmt(fd.Fd(), fmture)
	if err != nil {
		return PixFmt{}, err
	}

	return got, nil
}

// intervalList enumerates frame intervals for one concrete format+size.
func intervalList(fd uintptr, cc v4l2.FourCCType, w, h uint32) string {
	var parts []string

	for idx := uint32(0); idx < 64; idx++ {
		iv, err := v4l2.GetFormatFrameInterval(fd, idx, cc, w, h)
		if err != nil {
			break
		}

		if iv.Type == v4l2.FrameIntervalTypeDiscrete {
			parts = append(parts, fmt.Sprintf("%d/%d", iv.Interval.Min.Numerator, iv.Interval.Min.Denominator))

			continue
		}

		return fmt.Sprintf("%s min=%d/%d max=%d/%d step=%d/%d", frmsizeTypeName(iv.Type),
			iv.Interval.Min.Numerator, iv.Interval.Min.Denominator,
			iv.Interval.Max.Numerator, iv.Interval.Max.Denominator,
			iv.Interval.Step.Numerator, iv.Interval.Step.Denominator)
	}

	if len(parts) == 0 {
		return "(none)"
	}

	return strings.Join(parts, ",")
}

func frmsizeTypeName(t uint32) string {
	switch t {
	case v4l2.FrameSizeTypeDiscrete:

		return "DISCRETE"
	case v4l2.FrameSizeTypeStepwise:

		return "STEPWISE"
	case v4l2.FrameSizeTypeContinuous:

		return "CONTINUOUS"
	default:

		return fmt.Sprintf("type=%d", t)
	}
}

// isBusy classifies an operation result as a busy/conflict condition.
func isBusy(err error) bool {
	return errors.Is(err, unix.EBUSY)
}

func devicePath() string {
	if p := os.Getenv("V4LENS_PROBE_DEVICE"); p != "" {
		return p
	}

	return defaultProbeDevice
}

func cString(b []byte) string {
	if i := slices.Index(b, 0); i >= 0 {
		return string(b[:i])
	}

	return string(b)
}
