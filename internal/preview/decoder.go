// Package preview implements the streaming preview lane: pure-Go decoders
// (per D013) that honor the driver-reported frame layout, and a bounded
// latest-frame pipeline feeding the UI at a render ceiling of ~30 fps.
package preview

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// Decoder converts one raw frame into an image, consuming the effective
// FrameLayout (driver stride/padding — never an assumption from
// FOURCC+width+height). Decoded images are published to the UI and must be
// treated as immutable afterwards.
type Decoder interface {
	Decode(data []byte, layout domain.FrameLayout) (image.Image, error)
	Supported(cc domain.FOURCC) bool
}

// ReusableDecoder is implemented by decoders that can refill a previous
// image in place. The pipeline's bounded publication ring (3 slots) only
// refills a slot three publications after it was published, so the UI can
// safely hold a reference in the meantime.
type ReusableDecoder interface {
	Decoder
	// DecodeInto decodes into prev when prev is a reusable image with
	// matching geometry, allocating fresh otherwise.
	DecodeInto(data []byte, layout domain.FrameLayout, prev image.Image) (image.Image, error)
}

// MVP coverage (ARCHITECTURE §preview): MJPEG, YUYV/YUY2, UYVY, NV12,
// NV21, I420. Every other FOURCC renders "Preview unavailable" and never
// blocks controls.
var registry = map[domain.FOURCC]Decoder{
	"YUYV":  packed422Decoder{yAt: 0, uAt: 1, y1At: 2, vAt: 3},
	"YUY2":  packed422Decoder{yAt: 0, uAt: 1, y1At: 2, vAt: 3},
	"UYVY":  packed422Decoder{yAt: 1, uAt: 0, y1At: 3, vAt: 2},
	"NV12":  semiPlanar420Decoder{uFirst: true},
	"NV21":  semiPlanar420Decoder{uFirst: false},
	"I420":  i420Decoder{},
	"MJPG":  mjpegDecoder{},
	"JPEG":  mjpegDecoder{},
	"MJPEG": mjpegDecoder{},
}

// strideOf returns the driver-reported row stride, or def when the driver
// reported none (compressed formats always pass a purpose-built default).
func strideOf(layout domain.FrameLayout, def int) int {
	if layout.BytesPerLine > 0 {
		return layout.BytesPerLine
	}

	return def
}

// For returns the decoder for a FOURCC, or nil when unsupported.
func For(cc domain.FOURCC) Decoder {
	return registry[cc]
}

// Supported reports whether a decoder exists for the FOURCC.
func Supported(cc domain.FOURCC) bool {
	return registry[cc] != nil
}

// frameError renders decoder input problems honestly (bad/short buffers,
// layouts that cannot hold the advertised geometry).
type frameError struct{ msg string }

func (e frameError) Error() string { return e.msg }

// rowSlice bounds-checks one source row against the buffer.
func rowSlice(data []byte, row, rowBytes int) ([]byte, error) {
	off := row * rowBytes

	if off+rowBytes > len(data) {
		return nil, frameError{
			fmt.Sprintf("frame truncated: need %d bytes for row %d, have %d", off+rowBytes, row, len(data)),
		}
	}

	return data[off : off+rowBytes], nil
}

// packed422Decoder converts packed 4:2:2 formats (YUYV, UYVY) to
// *image.YCbCr with 4:2:2 subsampling. The byte phase distinguishes the
// family: YUYV is Y0 U Y1 V, UYVY is U Y0 V Y1 (D013: a phase flip, not a
// chroma swap).
type packed422Decoder struct {
	yAt, uAt, y1At, vAt int
}

func (d packed422Decoder) Supported(cc domain.FOURCC) bool {
	return registry[cc] == d
}

func (d packed422Decoder) Decode(data []byte, layout domain.FrameLayout) (image.Image, error) {
	return d.DecodeInto(data, layout, nil)
}

// DecodeInto refills prev when it fits (same geometry + 4:2:2 ratio),
// allocating a fresh image otherwise. A failed decode leaves prev valid —
// the pipeline keeps displaying the last good frame.
func (d packed422Decoder) DecodeInto(data []byte, layout domain.FrameLayout, prev image.Image) (image.Image, error) {
	w, h := layout.Width, layout.Height

	if w <= 0 || h <= 0 || w%2 != 0 {
		return nil, frameError{fmt.Sprintf("unsupported packed-422 geometry %dx%d", w, h)}
	}

	// Packed 4:2:2 occupies 2 bytes per pixel regardless of stride padding.
	rowBytes := strideOf(layout, w*2)
	if rowBytes < w*2 {
		return nil, frameError{
			fmt.Sprintf("row stride %d too small for %dx%d packed 4:2:2", rowBytes, w, h),
		}
	}

	img := reusableYCbCr(prev, w, h, image.YCbCrSubsampleRatio422)
	if img == nil {
		img = image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio422)
	}

	for row := range h {
		src, err := rowSlice(data, row, rowBytes)
		if err != nil {
			return nil, err
		}

		yRow := img.Y[row*w : (row+1)*w]
		cbRow := img.Cb[row*(w/2) : (row+1)*(w/2)]
		crRow := img.Cr[row*(w/2) : (row+1)*(w/2)]

		d.decodeRow(src, yRow, cbRow, crRow)
	}

	return img, nil
}

// decodeRow unpacks one row: chroma samples repeat for the pixel pair.
// src covers the full row (padding beyond w*2 bytes is ignored).
func (d packed422Decoder) decodeRow(src []byte, yRow, cbRow, crRow []uint8) {
	for x := 0; x < len(yRow); x += 2 {
		s := x * 2

		yRow[x] = src[s+d.yAt]
		yRow[x+1] = src[s+d.y1At]
		cbRow[x/2] = src[s+d.uAt]
		crRow[x/2] = src[s+d.vAt]
	}
}

// reusableYCbCr returns prev when it is a *image.YCbCr with exactly the
// requested geometry and subsampling (so row indexing stays in bounds),
// nil otherwise.
func reusableYCbCr(prev image.Image, w, h int, ratio image.YCbCrSubsampleRatio) *image.YCbCr {
	p, ok := prev.(*image.YCbCr)
	if !ok || p.Bounds().Dx() != w || p.Bounds().Dy() != h || p.SubsampleRatio != ratio {
		return nil
	}

	return p
}

// semiPlanar420Decoder converts NV12/NV21 (Y plane + interleaved UV plane)
// to *image.YCbCr with 4:2:0 subsampling. Both planes share the driver
// row stride; the chroma plane is U,V interleaved (NV12) or V,U (NV21).
type semiPlanar420Decoder struct {
	uFirst bool
}

func (d semiPlanar420Decoder) Supported(cc domain.FOURCC) bool {
	return registry[cc] == d
}

func (d semiPlanar420Decoder) Decode(data []byte, layout domain.FrameLayout) (image.Image, error) {
	return d.DecodeInto(data, layout, nil)
}

// DecodeInto refills prev when it fits (same geometry + 4:2:0 ratio).
func (d semiPlanar420Decoder) DecodeInto(data []byte, layout domain.FrameLayout, prev image.Image) (image.Image, error) {
	w, h := layout.Width, layout.Height

	if w <= 0 || h <= 0 || w%2 != 0 || h%2 != 0 {
		return nil, frameError{fmt.Sprintf("unsupported semi-planar geometry %dx%d", w, h)}
	}

	stride := strideOf(layout, w)
	if stride < w {
		return nil, frameError{fmt.Sprintf("row stride %d too small for %dx%d NV12/NV21", stride, w, h)}
	}

	chromaRows := h / 2

	// Y plane (h rows) followed by the interleaved chroma plane
	// (h/2 rows of U,V pairs, w/2 pairs across).
	need := stride*h + stride*chromaRows

	if len(data) < need {
		return nil, frameError{
			fmt.Sprintf("frame truncated: need %d bytes for %dx%d NV12/NV21, have %d", need, w, h, len(data)),
		}
	}

	img := reusableYCbCr(prev, w, h, image.YCbCrSubsampleRatio420)
	if img == nil {
		img = image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)
	}

	for row := range h {
		src, err := rowSlice(data, row, stride)
		if err != nil {
			return nil, err
		}

		copy(img.Y[row*w:(row+1)*w], src[:w])
	}

	chroma := data[stride*h:]

	for row := range chromaRows {
		src := chroma[row*stride : row*stride+w]

		cbRow := img.Cb[row*(w/2) : (row+1)*(w/2)]
		crRow := img.Cr[row*(w/2) : (row+1)*(w/2)]

		for x := range cbRow {
			u, v := src[2*x], src[2*x+1]

			if !d.uFirst {
				u, v = v, u
			}

			cbRow[x] = u
			crRow[x] = v
		}
	}

	return img, nil
}

// i420Decoder converts fully planar I420/YU12 (Y plane, then Cb plane,
// then Cr plane) to *image.YCbCr with 4:2:0 subsampling. Single-planar
// I420 packs the chroma planes at width/2 stride right after the Y plane.
type i420Decoder struct{}

func (i420Decoder) Supported(cc domain.FOURCC) bool {
	return registry[cc] != nil && registry[cc] == i420Decoder{}
}

func (i420Decoder) Decode(data []byte, layout domain.FrameLayout) (image.Image, error) {
	return i420Decoder{}.DecodeInto(data, layout, nil)
}

// DecodeInto refills prev when it fits (same geometry + 4:2:0 ratio).
func (i420Decoder) DecodeInto(data []byte, layout domain.FrameLayout, prev image.Image) (image.Image, error) {
	w, h := layout.Width, layout.Height

	if w <= 0 || h <= 0 || w%2 != 0 || h%2 != 0 {
		return nil, frameError{fmt.Sprintf("unsupported planar geometry %dx%d", w, h)}
	}

	stride := strideOf(layout, w)
	if stride < w {
		return nil, frameError{fmt.Sprintf("row stride %d too small for %dx%d I420", stride, w, h)}
	}

	chromaStride := w / 2
	chromaRows := h / 2

	need := stride*h + 2*chromaStride*chromaRows

	if len(data) < need {
		return nil, frameError{
			fmt.Sprintf("frame truncated: need %d bytes for %dx%d I420, have %d", need, w, h, len(data)),
		}
	}

	img := reusableYCbCr(prev, w, h, image.YCbCrSubsampleRatio420)
	if img == nil {
		img = image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)
	}

	for row := range h {
		src, err := rowSlice(data, row, stride)
		if err != nil {
			return nil, err
		}

		copy(img.Y[row*w:(row+1)*w], src[:w])
	}

	cbPlane := data[stride*h:]
	crPlane := cbPlane[chromaStride*chromaRows:]

	for row := range chromaRows {
		copy(img.Cb[row*chromaStride:(row+1)*chromaStride], cbPlane[row*chromaStride:][:chromaStride])
		copy(img.Cr[row*chromaStride:(row+1)*chromaStride], crPlane[row*chromaStride:][:chromaStride])
	}

	return img, nil
}

// mjpegDecoder decodes compressed MJPEG frames via the standard library
// (D013: stdlib image/jpeg at ~21 ms/frame for 1080p — comfortably inside
// the ~33 ms render budget). Compressed formats carry no stride
// (bytesperline=0, D013); BytesUsed bounds the JPEG stream when set.
type mjpegDecoder struct{}

func (mjpegDecoder) Supported(cc domain.FOURCC) bool {
	return registry[cc] == mjpegDecoder{}
}

func (mjpegDecoder) Decode(data []byte, layout domain.FrameLayout) (image.Image, error) {
	buf := data

	if layout.BytesUsed > 0 && layout.BytesUsed <= len(data) {
		buf = data[:layout.BytesUsed]
	}

	img, err := jpeg.Decode(bytes.NewReader(buf))
	if err != nil {
		return nil, frameError{fmt.Sprintf("mjpeg decode: %v", err)}
	}

	return img, nil
}
