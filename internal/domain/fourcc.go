// Package domain defines V4Lens's normalized device, control and capture
// types. The package has no I/O, no Fyne and no V4L2 library imports: it is
// the shared vocabulary between the backend, profiles, preview and UI.
package domain

// FOURCC is a pixel format identifier as reported by V4L2 (e.g. "MJPG",
// "UYVY"). It is kept as a string so it is printable and comparable.
type FOURCC string

// String returns the FOURCC text.
func (f FOURCC) String() string {
	return string(f)
}
