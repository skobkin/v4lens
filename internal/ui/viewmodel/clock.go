// Package viewmodel contains the Fyne-free UI logic: the slider-write
// coalescer with an injectable clock, the curated control-semantics table,
// and the staged format-change model. Keeping these free of the GUI toolkit
// makes them unit-testable without a display.
package viewmodel

import "time"

// Clock abstracts time for the coalescer so tests never sleep.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// RealClock is the production clock.
type RealClock struct{}

// Now returns the wall clock.
func (RealClock) Now() time.Time {
	return time.Now()
}

// After returns the wall-clock timer channel.
func (RealClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}
