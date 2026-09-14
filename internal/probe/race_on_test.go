//go:build integration && race

package probe

// raceEnabled reports that the test binary runs under the race detector.
// Instrumentation multiplies CPU-bound work (decoding) by an order of
// magnitude, so wall-time gates are asserted only in native runs.
const raceEnabled = true
