//go:build probe

package probe

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
)

// Layout describes the EFFECTIVE memory layout of a captured frame as
// reported by the driver (G_FMT / S_FMT result and the buffer's bytesused).
// Decoders must consume this layout instead of assuming tightly packed rows:
// bytesperline may exceed width*bpp and planar formats may carry padding.
type Layout struct {
	FourCC       string
	Width        int
	Height       int
	BytesPerLine int
	SizeImage    int
	BytesUsed    int
}

// rowLen returns the bytes a full row occupies in the source buffer.
func (l Layout) rowLen() int {
	return l.BytesPerLine
}

// usedLen returns how many bytes of data are valid for this frame.
func (l Layout) usedLen() int {
	if l.BytesUsed > 0 && l.BytesUsed <= l.SizeImage {
		return l.BytesUsed
	}

	if l.SizeImage > 0 {
		return l.SizeImage
	}

	return int(^uint(0) >> 1) // max int; bounds come from per-row checks
}

// DecodePureGo converts raw frame bytes to an *image.YCbCr (or decoded JPEG
// image) using only the standard library, honoring the driver-reported stride.
func DecodePureGo(data []byte, l Layout) (image.Image, error) {
	switch l.FourCC {
	case "MJPG", "JPEG":

		return jpeg.Decode(bytes.NewReader(data))
	case "YUYV", "YUY2":

		return packed422ToYCbCr(data, l, false)
	case "UYVY":

		return packed422ToYCbCr(data, l, true)
	case "NV12":

		return nv12ToYCbCr(data, l, false)
	case "NV21":

		return nv12ToYCbCr(data, l, true)
	case "I420":

		return i420ToYCbCr(data, l)
	default:

		return nil, fmt.Errorf("purego: unsupported fourcc %q", l.FourCC)
	}
}

// packed422ToYCbCr converts packed 4:2:2 honoring the driver stride; every row
// starts at row*BytesPerLine. Byte order: YUYV = Y0 U Y1 V, UYVY = U Y0 V Y1
// (the Y/chroma PHASE flips, not just the chroma assignment).
func packed422ToYCbCr(data []byte, l Layout, uyvy bool) (image.Image, error) {
	w, h := l.Width, l.Height

	if w <= 0 || h <= 0 || w%2 != 0 {
		return nil, fmt.Errorf("packed422: bad dimensions %dx%d", w, h)
	}

	bpl := l.rowLen()

	if bpl < w*2 {
		return nil, fmt.Errorf("packed422: bytesperline %d smaller than width*2 (%d)", bpl, w*2)
	}

	if len(data) < bpl*(h-1)+w*2 {
		return nil, fmt.Errorf("packed422: short frame: %d bytes for %dx%d stride %d", len(data), w, h, bpl)
	}

	img := image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio422)

	for row := range h {
		src := data[row*bpl:]
		yRow := img.Y[row*img.YStride:]
		cbRow := img.Cb[row*img.CStride:]
		crRow := img.Cr[row*img.CStride:]

		for x := range w / 2 {
			var y0, y1, u, v byte

			if uyvy {
				u, y0, v, y1 = src[4*x], src[4*x+1], src[4*x+2], src[4*x+3]
			} else {
				y0, u, y1, v = src[4*x], src[4*x+1], src[4*x+2], src[4*x+3]
			}

			yRow[2*x] = y0
			yRow[2*x+1] = y1
			cbRow[x] = u
			crRow[x] = v
		}
	}

	return img, nil
}

// nv12ToYCbCr converts semi-planar 4:2:0 (NV12: UV, NV21: VU) honoring strides.
func nv12ToYCbCr(data []byte, l Layout, swapChroma bool) (image.Image, error) {
	w, h := l.Width, l.Height

	if w <= 0 || h <= 0 || w%2 != 0 || h%2 != 0 {
		return nil, fmt.Errorf("nv12: bad dimensions %dx%d", w, h)
	}

	bpl := l.rowLen()

	if bpl < w {
		return nil, fmt.Errorf("nv12: bytesperline %d smaller than width (%d)", bpl, w)
	}

	uvRows := h / 2

	if len(data) < bpl*(h+uvRows-1)+w {
		return nil, fmt.Errorf("nv12: short frame: %d bytes for %dx%d stride %d", len(data), w, h, bpl)
	}

	img := image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)

	for row := range h {
		copy(img.Y[row*img.YStride:row*img.YStride+w], data[row*bpl:row*bpl+w])
	}

	uvStart := bpl * h

	for row := range uvRows {
		src := data[uvStart+row*bpl:]
		cbRow := img.Cb[row*img.CStride:]
		crRow := img.Cr[row*img.CStride:]

		for x := range w / 2 {
			c0, c1 := src[2*x], src[2*x+1]

			if swapChroma {
				c0, c1 = c1, c0
			}

			cbRow[x] = c0
			crRow[x] = c1
		}
	}

	return img, nil
}

// i420ToYCbCr converts planar 4:2:0 honoring the Y stride; the chroma stride
// is taken as BytesPerLine/2, the standard I420 arrangement.
func i420ToYCbCr(data []byte, l Layout) (image.Image, error) {
	w, h := l.Width, l.Height

	if w <= 0 || h <= 0 || w%2 != 0 || h%2 != 0 {
		return nil, fmt.Errorf("i420: bad dimensions %dx%d", w, h)
	}

	bpl := l.rowLen()

	if bpl < w {
		return nil, fmt.Errorf("i420: bytesperline %d smaller than width (%d)", bpl, w)
	}

	uStride := bpl / 2
	uRows := h / 2
	uStart := bpl * h
	vStart := uStart + uStride*uRows

	if len(data) < vStart+uStride*(uRows-1)+w/2 {
		return nil, fmt.Errorf("i420: short frame: %d bytes for %dx%d stride %d", len(data), w, h, bpl)
	}

	img := image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)

	for row := range h {
		copy(img.Y[row*img.YStride:row*img.YStride+w], data[row*bpl:row*bpl+w])
	}

	for row := range uRows {
		copy(img.Cb[row*img.CStride:row*img.CStride+w/2], data[uStart+row*uStride:uStart+row*uStride+w/2])
		copy(img.Cr[row*img.CStride:row*img.CStride+w/2], data[vStart+row*uStride:vStart+row*uStride+w/2])
	}

	return img, nil
}
