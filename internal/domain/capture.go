package domain

// FrameInterval is an exact rational frame period (numerator/denominator,
// e.g. 1001/60000 for 59.94 fps). It is never rounded to a float; 0/0 is
// never a valid interval.
type FrameInterval struct {
	Num uint32
	Den uint32
}

// Valid reports whether the interval is a usable rational.
func (f FrameInterval) Valid() bool {
	return f.Num != 0 && f.Den != 0
}

// CaptureConfig is one capture-configuration transaction unit: input,
// pixel format, resolution and the optional exact frame interval. Interval
// is nil only for devices without V4L2_CAP_TIMEPERFRAME support.
type CaptureConfig struct {
	Input         uint32
	FourCC        FOURCC
	Width, Height uint32
	Interval      *FrameInterval
}

// Equal compares all fields including the optional interval.
func (c CaptureConfig) Equal(o CaptureConfig) bool {
	if c.Input != o.Input || c.FourCC != o.FourCC ||
		c.Width != o.Width || c.Height != o.Height {
		return false
	}

	switch {
	case c.Interval == nil && o.Interval == nil:

		return true
	case c.Interval == nil || o.Interval == nil:

		return false
	default:

		return *c.Interval == *o.Interval
	}
}

// FrameSizeType preserves the capability form exactly as ENUM_FRAMESIZES
// reported it. Devices with stepwise/continuous sizes are fully usable and
// never dropped.
type FrameSizeType int8

const (
	FrameSizeDiscrete FrameSizeType = iota
	FrameSizeStepwise
	FrameSizeContinuous
)

// FrameSize is one frame-size capability entry: a concrete W/H for
// discrete devices, or bounds+step for stepwise/continuous ones. No
// interval lists are precomputed here — intervals are lazy.
type FrameSize struct {
	Type FrameSizeType

	// Discrete form.
	Width, Height uint32

	// Stepwise/continuous form.
	MinW, MinH   uint32
	MaxW, MaxH   uint32
	StepW, StepH uint32
}

// FormatDesc describes one pixel format and its frame-size capabilities
// for the currently active input.
type FormatDesc struct {
	FourCC      FOURCC
	Description string
	Sizes       []FrameSize
}

// DiscreteSizes returns the concrete discrete sizes, if any.
func (f FormatDesc) DiscreteSizes() []FrameSize {
	var out []FrameSize

	for _, s := range f.Sizes {
		if s.Type == FrameSizeDiscrete {
			out = append(out, s)
		}
	}

	return out
}

// IntervalType preserves the ENUM_FRAMEINTERVALS capability form.
type IntervalType int8

const (
	IntervalDiscrete IntervalType = iota
	IntervalStepwise
	IntervalContinuous
)

// IntervalCap is one frame-interval capability for a concrete
// FOURCC+width+height query. Discrete entries use Min only; stepwise and
// continuous carry the rational Min/Max/Step range.
type IntervalCap struct {
	Type     IntervalType
	Min      FrameInterval
	Max      FrameInterval
	Step     FrameInterval
	Discrete []FrameInterval
}

// BufferType distinguishes the capture buffer type. The MVP streams
// BufferTypeVideoCapture only; MPLANE is detected and reported honestly.
type BufferType int8

const (
	BufferTypeUnknown BufferType = iota
	BufferTypeVideoCapture
	BufferTypeVideoCaptureMPlane
	BufferTypeOther
)

// PlaneLayout carries per-plane geometry (populated for MPLANE devices and
// kept for MPLANE-readiness of the architecture).
type PlaneLayout struct {
	BytesPerLine int
	SizeImage    int
	BytesUsed    int
}

// FrameLayout is the EFFECTIVE frame memory layout, produced from the
// actual G_FMT/S_FMT result — never recomputed from FOURCC+W+H. Decoders
// must honor BytesPerLine (driver padding) and never assume width*bpp.
type FrameLayout struct {
	BufferType    BufferType
	FourCC        FOURCC
	Width, Height int
	BytesPerLine  int
	SizeImage     int
	BytesUsed     int
	Planes        []PlaneLayout
}

// RowBytes is the driver stride: the bytes one output row occupies.
func (l FrameLayout) RowBytes() int {
	if l.BytesPerLine > 0 {
		return l.BytesPerLine
	}

	return l.Width * 2 // conservative packed-422 default; decoders validate
}

// Frame is one captured frame in V4Lens-owned memory. Data never aliases
// driver MMAP buffers and stays immutable for the lifetime of the Frame.
type Frame struct {
	Data     []byte
	Layout   FrameLayout
	Sequence uint32
}
