//go:build probe

package probe

import (
	"image"
	"testing"
)

// requireYCbCr decodes and asserts the *image.YCbCr result type.
func requireYCbCr(t *testing.T, data []byte, l Layout) *image.YCbCr {
	t.Helper()

	img, err := DecodePureGo(data, l)
	if err != nil {
		t.Fatalf("decode %s %dx%d: %v", l.FourCC, l.Width, l.Height, err)
	}

	ycbcr, ok := img.(*image.YCbCr)
	if !ok {
		t.Fatalf("expected *image.YCbCr, got %T", img)
	}

	return ycbcr
}

// buildPacked422 synthesizes a padded packed-4:2:2 frame with planted values:
// Y = pixel index, U = 0x80, V = 0x40. order uyvy selects U Y0 V Y1 vs Y0 U Y1 V.
func buildPacked422(w, h, bpl int, uyvy bool) []byte {
	buf := make([]byte, bpl*h)

	for row := range h {
		for x := range w / 2 {
			y0, y1 := byte(row*w+2*x), byte(row*w+2*x+1)

			if uyvy { // U Y0 V Y1
				buf[row*bpl+4*x] = 0x80
				buf[row*bpl+4*x+1] = y0
				buf[row*bpl+4*x+2] = 0x40
				buf[row*bpl+4*x+3] = y1
			} else { // Y0 U Y1 V
				buf[row*bpl+4*x] = y0
				buf[row*bpl+4*x+1] = 0x80
				buf[row*bpl+4*x+2] = y1
				buf[row*bpl+4*x+3] = 0x40
			}
		}
	}

	return buf
}

// TestDecodeSyntheticPhase verifies byte-order (phase), stride handling and
// chroma placement against planted pixels, for every pure-Go kernel. This is
// the test that would have caught the UYVY phase bug the probe found.
func TestDecodeSyntheticPhase(t *testing.T) {
	const w, h, pad = 8, 4, 16 // padded stride exercises per-row indexing

	t.Run("UYVY", func(t *testing.T) {
		buf := buildPacked422(w, h, w*2+pad, true)
		img := requireYCbCr(t, buf, Layout{FourCC: "UYVY", Width: w, Height: h, BytesPerLine: w*2 + pad, SizeImage: len(buf)})

		for i := range w {
			if img.Y[i] != byte(i) {
				t.Fatalf("Y phase: Y[%d]=%d want %d", i, img.Y[i], i)
			}
		}

		if img.Cb[0] != 0x80 || img.Cr[0] != 0x40 {
			t.Fatalf("chroma placement: Cb=0x%02x Cr=0x%02x want 0x80/0x40", img.Cb[0], img.Cr[0])
		}

		if img.SubsampleRatio != image.YCbCrSubsampleRatio422 {
			t.Fatalf("subsample ratio: %v want 422", img.SubsampleRatio)
		}
	})

	t.Run("YUYV", func(t *testing.T) {
		buf := buildPacked422(w, h, w*2+pad, false)
		img := requireYCbCr(t, buf, Layout{FourCC: "YUYV", Width: w, Height: h, BytesPerLine: w*2 + pad, SizeImage: len(buf)})

		for i := range w {
			if img.Y[i] != byte(i) {
				t.Fatalf("Y phase: Y[%d]=%d want %d", i, img.Y[i], i)
			}
		}

		if img.Cb[0] != 0x80 || img.Cr[0] != 0x40 {
			t.Fatalf("chroma placement: Cb=0x%02x Cr=0x%02x want 0x80/0x40", img.Cb[0], img.Cr[0])
		}
	})

	// Cross-check both packed orders against libyuv on synthetic values.
	if libyuvAvailable {
		for _, order := range []struct {
			fourcc string
			uyvy   bool
		}{
			{"UYVY", true},
			{"YUYV", false},
		} {
			buf := buildPacked422(w, h, w*2, order.uyvy)
			l := Layout{FourCC: order.fourcc, Width: w, Height: h, BytesPerLine: w * 2, SizeImage: len(buf)}

			img := requireYCbCr(t, buf, l)

			lib, err := decodeLibyuv(buf, l)
			if err != nil {
				t.Fatalf("libyuv %s: %v", order.fourcc, err)
			}

			lim := lib.(*image.YCbCr)

			for i := range img.Y {
				d := int(img.Y[i]) - int(lim.Y[i])
				if d < -2 || d > 2 {
					t.Fatalf("%s: pure-Go Y[%d]=%d vs libyuv %d exceeds rounding", order.fourcc, i, img.Y[i], lim.Y[i])
				}
			}
		}
	}
}
