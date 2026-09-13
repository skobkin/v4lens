//go:build probe && libyuv

package probe

import (
	"fmt"
	"image"
	"unsafe"
)

/*
#cgo LDFLAGS: -lyuv
#include <stdlib.h>
#include <libyuv/convert.h>
*/
import "C"

// libyuvAvailable reports that the optional CGO libyuv adapter is compiled in.
// MJPEG is NOT covered by this adapter: the system libyuv exports no usable
// C entry point for JPEG decoding (MJpegToI420 is absent from the header and
// the shared object), so MJPEG always decodes via image/jpeg.
const libyuvAvailable = true

// decodeLibyuv converts raw YUV frames using libyuv's SIMD kernels, honoring
// the driver-reported stride.
func decodeLibyuv(data []byte, l Layout) (image.Image, error) {
	w, h := l.Width, l.Height

	if w <= 0 || h <= 0 || w%2 != 0 || h%2 != 0 {
		return nil, fmt.Errorf("libyuv: bad dimensions %dx%d", w, h)
	}

	bpl := l.rowLen()
	src := (*C.uint8_t)(nil)

	if len(data) > 0 {
		src = (*C.uint8_t)(unsafeBytePtr(data))
	}

	newI420 := func() *image.YCbCr {
		return image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)
	}

	newI422 := func() *image.YCbCr {
		return image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio422)
	}

	switch l.FourCC {
	case "YUYV", "YUY2":
		img := newI422()

		if rc := C.YUY2ToI420(src, C.int(bpl), yPtr(img), C.int(img.YStride), uPtr(img), C.int(img.CStride), vPtr(img), C.int(img.CStride), C.int(w), C.int(h)); rc != 0 {
			return nil, fmt.Errorf("libyuv: YUY2ToI420 rc=%d", rc)
		}

		return img, nil
	case "UYVY":
		img := newI422()

		if rc := C.UYVYToI420(src, C.int(bpl), yPtr(img), C.int(img.YStride), uPtr(img), C.int(img.CStride), vPtr(img), C.int(img.CStride), C.int(w), C.int(h)); rc != 0 {
			return nil, fmt.Errorf("libyuv: UYVYToI420 rc=%d", rc)
		}

		return img, nil
	case "NV12":
		img := newI420()
		uvStart := bpl * h

		if len(data) < uvStart+1 {
			return nil, fmt.Errorf("libyuv: short NV12 frame: %d bytes", len(data))
		}

		uv := (*C.uint8_t)(unsafeBytePtr(data[uvStart:]))

		if rc := C.NV12ToI420(src, C.int(bpl), uv, C.int(bpl), yPtr(img), C.int(img.YStride), uPtr(img), C.int(img.CStride), vPtr(img), C.int(img.CStride), C.int(w), C.int(h)); rc != 0 {
			return nil, fmt.Errorf("libyuv: NV12ToI420 rc=%d", rc)
		}

		return img, nil
	default:

		return nil, fmt.Errorf("libyuv: unsupported fourcc %q", l.FourCC)
	}
}

// unsafeBytePtr converts a byte slice header to a raw C pointer.
func unsafeBytePtr(b []byte) unsafe.Pointer { //nolint:gosec // benchmark-only adapter over managed slices
	return unsafe.Pointer(&b[0]) //nolint:gosec // low-level CGO bridge required for the libyuv comparison
}

func yPtr(img *image.YCbCr) *C.uint8_t {
	return (*C.uint8_t)(unsafeBytePtr(img.Y))
}

func uPtr(img *image.YCbCr) *C.uint8_t {
	return (*C.uint8_t)(unsafeBytePtr(img.Cb))
}

func vPtr(img *image.YCbCr) *C.uint8_t {
	return (*C.uint8_t)(unsafeBytePtr(img.Cr))
}
