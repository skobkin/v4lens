package viewmodel

import "fmt"

// NormalizeNumeric validates a raw numeric input against the control's
// range and steps it to the nearest valid value:
//   - values outside [min,max] clamp to the bound;
//   - with step > 0 the value snaps to the nearest on-step position;
//   - a zero or negative step means the driver reports no stepping, so
//     only the range applies.
//
// The result is what gets written to the device; the read-back (when the
// control is readable) stays the final word.
func NormalizeNumeric(v, min, max, step int64) (int64, error) {
	if max < min {
		return 0, fmt.Errorf("invalid range [%d, %d]", min, max)
	}

	if v < min {
		v = min
	}

	if v > max {
		v = max
	}

	if step > 1 {
		nearest := min + ((v-min+step/2)/step)*step

		if nearest > max {
			nearest = min + ((max-min)/step)*step
		}

		v = nearest
	}

	return v, nil
}

// PracticalSlider reports whether the [min,max] range renders usefully as
// a slider: a bounded span that fits a screen-scale widget. Awkward ranges
// (huge spans, negative exotics) get a numeric entry instead.
func PracticalSlider(min, max int64) bool {
	if min >= max {
		return false
	}

	span := max - min

	// Beyond a billion the slider is unusable; plain entry instead.
	return span > 0 && span <= 1_000_000_000
}
