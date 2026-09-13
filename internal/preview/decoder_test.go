package preview

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// layout422 builds a packed-4:2:2 layout with the given driver stride.
func layout422(w, h, stride int, cc domain.FOURCC) domain.FrameLayout {
	return domain.FrameLayout{
		BufferType:   domain.BufferTypeVideoCapture,
		FourCC:       cc,
		Width:        w,
		Height:       h,
		BytesPerLine: stride,
		SizeImage:    stride * h,
	}
}

// TestUYVYPhase plants the D013 phase discipline: UYVY is U Y0 V Y1.
// Padding bytes beyond the active pixels must be ignored.
func TestUYVYPhase(t *testing.T) {
	const w, h = 4, 2

	stride := w*2 + 8 // driver padding at the end of each row

	// Row pattern per pixel pair: U Y0 V Y1.
	// Pair 0: U=10 Y=100 V=20 ; Pair 1: U=30 Y=150 V=40.
	rows := [][]byte{
		{10, 100, 20, 150, 30, 110, 40, 160, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE},
		{11, 101, 21, 151, 31, 111, 41, 161, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE, 0xEE},
	}

	data := make([]byte, 0, stride*h)

	for _, r := range rows {
		data = append(data, r...)
	}

	img, err := For("UYVY").Decode(data, layout422(w, h, stride, "UYVY"))
	if err != nil {
		t.Fatal(err)
	}

	ycc, ok := img.(*image.YCbCr)
	if !ok {
		t.Fatalf("decoded %T, want *image.YCbCr", img)
	}

	wantY := [][]uint8{{100, 150, 110, 160}, {101, 151, 111, 161}}
	wantCb := [][]uint8{{10, 30}, {11, 31}}
	wantCr := [][]uint8{{20, 40}, {21, 41}}

	for row := range h {
		for x := range w {
			if got := ycc.Y[row*w+x]; got != wantY[row][x] {
				t.Fatalf("Y[%d][%d] = %d, want %d", row, x, got, wantY[row][x])
			}
		}

		for x := range w / 2 {
			if got := ycc.Cb[row*(w/2)+x]; got != wantCb[row][x] {
				t.Fatalf("Cb[%d][%d] = %d, want %d", row, x, got, wantCb[row][x])
			}

			if got := ycc.Cr[row*(w/2)+x]; got != wantCr[row][x] {
				t.Fatalf("Cr[%d][%d] = %d, want %d", row, x, got, wantCr[row][x])
			}
		}
	}
}

// TestYUYVPhase checks the mirrored phase: YUYV is Y0 U Y1 V.
func TestYUYVPhase(t *testing.T) {
	const w, h = 2, 1

	stride := w * 2

	// Y0 U Y1 V.
	data := []byte{200, 15, 210, 25}

	img, err := For("YUYV").Decode(data, layout422(w, h, stride, "YUYV"))
	if err != nil {
		t.Fatal(err)
	}

	ycc := img.(*image.YCbCr)

	if ycc.Y[0] != 200 || ycc.Y[1] != 210 {
		t.Fatalf("Y = %d,%d", ycc.Y[0], ycc.Y[1])
	}

	if ycc.Cb[0] != 15 || ycc.Cr[0] != 25 {
		t.Fatalf("chroma = %d/%d", ycc.Cb[0], ycc.Cr[0])
	}
}

func TestPacked422RejectsShortStrideAndTruncatedRows(t *testing.T) {
	if _, err := For("UYVY").Decode(make([]byte, 8), layout422(8, 2, 8, "UYVY")); err == nil {
		t.Fatal("stride smaller than the packed row must be rejected")
	}

	// Two rows advertised, buffer holds only one.
	_, err := For("UYVY").Decode(make([]byte, 4), layout422(2, 2, 4, "UYVY"))
	if err == nil {
		t.Fatal("truncated frame must be rejected")
	}
}

// TestNV12Golden plants a 4×4 frame: Y values 0x10+row*16+x, chroma plane
// rows carrying U=0x40+col, V=0x80+col (NV12: U first).
func TestNV12Golden(t *testing.T) {
	const w, h = 4, 4

	stride := w + 4 // padded rows

	data := make([]byte, stride*(h+h/2))

	for row := range h {
		for x := range w {
			data[row*stride+x] = byte(0x10 + row*16 + x)
		}
	}

	chroma := data[stride*h:]

	for row := range h / 2 {
		for col := range w / 2 {
			chroma[row*stride+2*col] = byte(0x40 + col)
			chroma[row*stride+2*col+1] = byte(0x80 + col)
		}
	}

	img, err := For("NV12").Decode(data, domain.FrameLayout{
		FourCC: "NV12", Width: w, Height: h, BytesPerLine: stride,
	})
	if err != nil {
		t.Fatal(err)
	}

	ycc := img.(*image.YCbCr)

	if ycc.SubsampleRatio != image.YCbCrSubsampleRatio420 {
		t.Fatalf("subsample = %v", ycc.SubsampleRatio)
	}

	if ycc.Y[0] != 0x10 || ycc.Y[3] != 0x13 || ycc.Y[3*w+3] != 0x43 {
		t.Fatalf("Y plane wrong: %v", ycc.Y)
	}

	if ycc.Cb[0] != 0x40 || ycc.Cb[1] != 0x41 {
		t.Fatalf("Cb = %v", ycc.Cb[:2])
	}

	if ycc.Cr[0] != 0x80 || ycc.Cr[1] != 0x81 {
		t.Fatalf("Cr = %v", ycc.Cr[:2])
	}
}

// TestNV21SwapsChroma verifies the V,U interleaving.
func TestNV21SwapsChroma(t *testing.T) {
	const w, h = 2, 2

	data := []byte{
		1, 2,
		3, 4,
		0xAA, 0xBB, // first chroma pair: NV21 means V=0xAA, U=0xBB
	}

	img, err := For("NV21").Decode(data, domain.FrameLayout{FourCC: "NV21", Width: w, Height: h})
	if err != nil {
		t.Fatal(err)
	}

	ycc := img.(*image.YCbCr)

	if ycc.Cb[0] != 0xBB || ycc.Cr[0] != 0xAA {
		t.Fatalf("NV21 chroma wrong: Cb=%d Cr=%d", ycc.Cb[0], ycc.Cr[0])
	}
}

func TestI420Golden(t *testing.T) {
	const w, h = 4, 4

	stride := w

	data := make([]byte, stride*h+w*h/2)

	for i := range w * h {
		data[i] = byte(0x20 + i)
	}

	cb := data[w*h:]
	cr := cb[w*h/4:]

	for i := range w * h / 4 {
		cb[i] = byte(0x50 + i)
		cr[i] = byte(0x90 + i)
	}

	img, err := For("I420").Decode(data, domain.FrameLayout{FourCC: "I420", Width: w, Height: h})
	if err != nil {
		t.Fatal(err)
	}

	ycc := img.(*image.YCbCr)

	if ycc.Y[5] != 0x25 {
		t.Fatalf("Y[5] = %d", ycc.Y[5])
	}

	if ycc.Cb[0] != 0x50 || ycc.Cr[0] != 0x90 {
		t.Fatalf("I420 chroma wrong: Cb=%d Cr=%d", ycc.Cb[0], ycc.Cr[0])
	}
}

func TestMJPEGDecodes(t *testing.T) {
	// Encode a solid-color 8×6 JPEG and decode it through the registry.
	src := image.NewYCbCr(image.Rect(0, 0, 8, 6), image.YCbCrSubsampleRatio420)

	for i := range src.Y {
		src.Y[i] = 0x80
	}

	for i := range src.Cb {
		src.Cb[i] = 0x40
	}

	for i := range src.Cr {
		src.Cr[i] = 0xB0
	}

	var buf bytes.Buffer

	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}

	layout := domain.FrameLayout{
		FourCC: "MJPG", Width: 8, Height: 6,
		BytesUsed: buf.Len(),
	}

	img, err := For("MJPG").Decode(append(buf.Bytes(), 0xFF, 0xFF), layout)
	if err != nil {
		t.Fatal(err)
	}

	bounds := img.Bounds()

	if bounds.Dx() != 8 || bounds.Dy() != 6 {
		t.Fatalf("decoded %v", bounds)
	}

	// Near-solid decode: sample the center pixel with tolerance for JPEG
	// quantization.
	r, g, b, _ := img.At(4, 3).RGBA()

	conv := color.YCbCr{Y: 0x80, Cb: 0x40, Cr: 0xB0}
	wr, wg, wb, _ := conv.RGBA()

	if absDiff(r, wr) > 3000 || absDiff(g, wg) > 3000 || absDiff(b, wb) > 3000 {
		t.Fatalf("decoded color (%d,%d,%d) too far from expected (%d,%d,%d)", r, g, b, wr, wg, wb)
	}
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}

	return b - a
}

func TestRegistryCoverage(t *testing.T) {
	for _, cc := range []domain.FOURCC{"MJPEG", "YUYV", "YUY2", "UYVY", "NV12", "NV21", "I420"} {
		if !Supported(cc) {
			t.Fatalf("%s must be supported (mandatory MVP coverage)", cc)
		}
	}

	for _, cc := range []domain.FOURCC{"XXXX", "GREY", "H264"} {
		if Supported(cc) {
			t.Fatalf("%s must not claim support", cc)
		}
	}
}

func BenchmarkUYVY1080p(b *testing.B) {
	const w, h = 1920, 1080

	layout := layout422(w, h, w*2, "UYVY")
	data := make([]byte, layout.SizeImage)

	for i := range data {
		data[i] = byte(i)
	}

	d := For("UYVY")

	b.ResetTimer()

	for range b.N {
		if _, err := d.Decode(data, layout); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNV12_1080p(b *testing.B) {
	const w, h = 1920, 1080

	layout := domain.FrameLayout{FourCC: "NV12", Width: w, Height: h, BytesPerLine: w, SizeImage: w * h * 3 / 2}
	data := make([]byte, layout.SizeImage)

	for i := range data {
		data[i] = byte(i)
	}

	d := For("NV12")

	b.ResetTimer()

	for range b.N {
		if _, err := d.Decode(data, layout); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMJPEG1080p(b *testing.B) {
	const w, h = 1920, 1080

	src := image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)

	for i := range src.Y {
		src.Y[i] = uint8(i % 251)
	}

	for i := range src.Cb {
		src.Cb[i] = uint8(i % 241)
	}

	for i := range src.Cr {
		src.Cr[i] = uint8(i % 233)
	}

	var buf bytes.Buffer

	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 85}); err != nil {
		b.Fatal(err)
	}

	layout := domain.FrameLayout{FourCC: "MJPG", Width: w, Height: h, BytesUsed: buf.Len()}
	data := buf.Bytes()

	d := For("MJPG")

	b.ResetTimer()

	for range b.N {
		if _, err := d.Decode(data, layout); err != nil {
			b.Fatal(err)
		}
	}
}
