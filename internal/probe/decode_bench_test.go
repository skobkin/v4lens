//go:build probe

package probe

import (
	"context"
	"fmt"
	"image"
	"os"
	"testing"
	"time"

	"github.com/pion/mediadevices/pkg/frame"
	"github.com/vladimirvivien/go4vl/device"
)

// benchFrame is one captured real frame plus its effective layout.
type benchFrame struct {
	data   []byte
	layout Layout
}

// captureBenchFrame snapshots the current format, applies the requested
// format+size through a short-lived fd (ownership released on close), captures
// one frame through a dedicated streaming handle (copy before Release) and
// hands back a restore func for the original format.
func captureBenchFrame(b testing.TB, devPath, fourcc string, w, h uint32) (benchFrame, func(), error) {
	b.Helper()

	if _, err := os.Stat(devPath); err != nil {
		return benchFrame{}, nil, fmt.Errorf("device %s not available", devPath)
	}

	ctrlFD, err := os.OpenFile(devPath, os.O_RDWR, 0)
	if err != nil {
		return benchFrame{}, nil, fmt.Errorf("open: %w", err)
	}

	defer func() { _ = ctrlFD.Close() }()

	snapFmt, err := gFmt(ctrlFD.Fd())
	if err != nil {
		return benchFrame{}, nil, fmt.Errorf("snapshot G_FMT: %w", err)
	}

	restore := func() {
		b.Helper()

		if _, err := sFmtOnShortLivedFd(devPath, snapFmt); err != nil {
			b.Errorf("RESTORE FAILED: S_FMT %s %dx%d: %v", snapFmt.FourCC, snapFmt.Width, snapFmt.Height, err)
		}
	}

	if _, err := sFmtOnShortLivedFd(devPath, PixFmt{Width: w, Height: h, FourCC: fourcc}); err != nil {
		return benchFrame{}, nil, fmt.Errorf("S_FMT %s %dx%d: %w", fourcc, w, h, err)
	}

	streamDev, err := device.Open(devPath)
	if err != nil {
		return benchFrame{}, nil, fmt.Errorf("stream open: %w", err)
	}

	defer func() { _ = streamDev.Close() }()

	pf, err := streamDev.GetPixFormat()
	if err != nil {
		return benchFrame{}, nil, fmt.Errorf("GetPixFormat: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := streamDev.Start(ctx); err != nil {
		return benchFrame{}, nil, fmt.Errorf("start: %w", err)
	}

	select {
	case fr, ok := <-streamDev.GetFrames():
		if !ok || len(fr.Data) == 0 {
			_ = streamDev.Stop()

			return benchFrame{}, nil, fmt.Errorf("no frame captured")
		}

		captured := benchFrame{
			data: append([]byte(nil), fr.Data...),
			layout: Layout{
				FourCC:       fourccString(uint32(pf.PixelFormat)),
				Width:        int(pf.Width),
				Height:       int(pf.Height),
				BytesPerLine: int(pf.BytesPerLine),
				SizeImage:    int(pf.SizeImage),
				BytesUsed:    len(fr.Data),
			},
		}

		fr.Release()
		_ = streamDev.Stop()

		return captured, restore, nil
	case err := <-streamDev.GetError():
		_ = streamDev.Stop()

		return benchFrame{}, nil, fmt.Errorf("stream error: %w", err)
	case <-ctx.Done():
		_ = streamDev.Stop()

		return benchFrame{}, nil, fmt.Errorf("timeout waiting for frame")
	}
}

// captureForBench captures the requested frame or skips the benchmark.
func captureForBench(b testing.TB, fourcc string, w, h uint32) benchFrame {
	b.Helper()

	fr, restore, err := captureBenchFrame(b, devicePath(), fourcc, w, h)
	if err != nil {
		b.Skipf("capture %s %dx%d failed: %v", fourcc, w, h, err)
	}

	b.Cleanup(restore)
	b.Logf("captured %s: %dx%d bpl=%d sizeimage=%d bytesused=%d",
		fr.layout.FourCC, fr.layout.Width, fr.layout.Height, fr.layout.BytesPerLine, fr.layout.SizeImage, fr.layout.BytesUsed)

	return fr
}

func BenchmarkPureGoYUYV(b *testing.B) {
	fr := captureForBench(b, "YUYV", 1920, 1080)
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if _, err := DecodePureGo(fr.data, fr.layout); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLibyuvYUYV(b *testing.B) {
	fr := captureForBench(b, "YUYV", 1920, 1080)

	if !libyuvAvailable {
		b.Skip("built without libyuv tag")
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if _, err := decodeLibyuv(fr.data, fr.layout); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPionYUYV(b *testing.B) {
	fr := captureForBench(b, "YUYV", 1920, 1080)

	dec, err := frame.NewDecoder(frame.FormatYUYV)
	if err != nil {
		b.Fatal(err)
	}

	if fr.layout.BytesPerLine != fr.layout.Width*2 {
		b.Logf("NOTE: pion decoder assumes tightly packed rows; driver stride is %d (padded) — output would be distorted", fr.layout.BytesPerLine)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		img, cleanup, err := dec.Decode(fr.data, fr.layout.Width, fr.layout.Height)
		if err != nil {
			b.Fatal(err)
		}

		_ = img
		cleanup()
	}
}

func BenchmarkPureGoMJPEG(b *testing.B) {
	fr := captureForBench(b, "MJPG", 1920, 1080)
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if _, err := DecodePureGo(fr.data, fr.layout); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPionMJPEG(b *testing.B) {
	fr := captureForBench(b, "MJPG", 1920, 1080)

	dec, err := frame.NewDecoder(frame.FormatMJPEG)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		img, cleanup, err := dec.Decode(fr.data, fr.layout.Width, fr.layout.Height)
		if err != nil {
			b.Fatal(err)
		}

		_ = img
		cleanup()
	}
}

// BenchmarkPureGoNV12 is a secondary raw-format datapoint (NV12 cannot be
// captured from a UVC webcam, so it runs on synthetic padded data).
func BenchmarkPureGoNV12(b *testing.B) {
	w, h := 1920, 1080
	bpl := w + 64 // simulated driver padding

	buf := make([]byte, bpl*h+bpl*(h/2))
	for i := range buf {
		buf[i] = byte(i)
	}

	l := Layout{FourCC: "NV12", Width: w, Height: h, BytesPerLine: bpl, SizeImage: len(buf)}
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if _, err := DecodePureGo(buf, l); err != nil {
			b.Fatal(err)
		}
	}
}

// TestDecodeRoundTripSanity validates the pure-Go converter on a real captured
// YUYV frame and, when available, compares against libyuv output.
func TestDecodeRoundTripSanity(t *testing.T) {
	if _, err := os.Stat(devicePath()); err != nil {
		t.Skipf("probe device %s not available", devicePath())
	}

	fr, restore, err := captureBenchFrame(t, devicePath(), "YUYV", 640, 480)
	if err != nil {
		t.Skipf("capture failed: %v", err)
	}

	t.Cleanup(restore)

	img, err := DecodePureGo(fr.data, fr.layout)
	if err != nil {
		t.Fatalf("pure-Go decode: %v", err)
	}

	ycbcr, ok := img.(*image.YCbCr)
	if !ok {
		t.Fatalf("expected *image.YCbCr, got %T", img)
	}

	t.Logf("decoded %dx%d ratio=%v Y[0]=%d Cb[0]=%d Cr[0]=%d (bpl=%d)",
		fr.layout.Width, fr.layout.Height, ycbcr.SubsampleRatio, ycbcr.Y[0], ycbcr.Cb[0], ycbcr.Cr[0], fr.layout.BytesPerLine)

	if libyuvAvailable {
		img2, err := decodeLibyuv(fr.data, fr.layout)
		if err != nil {
			t.Fatalf("libyuv decode: %v", err)
		}

		y2 := img2.(*image.YCbCr)
		var diff int

		for i := 0; i < len(ycbcr.Y); i += 997 {
			d := int(ycbcr.Y[i]) - int(y2.Y[i])
			if d < 0 {
				diff -= d
			} else {
				diff += d
			}
		}

		t.Logf("libyuv vs pure-Go sampled |Y diff| = %d (SIMD vs Go rounding makes small diffs expected)", diff)
	}
}
