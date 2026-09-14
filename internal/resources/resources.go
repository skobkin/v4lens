// Package resources embeds V4Lens' vector icon set. The SVGs are pure
// data; rendering them in the active theme color is the UI's concern
// (theme.NewThemedResource and friends recolor fills on the fly).
package resources

import (
	"embed"

	"fyne.io/fyne/v2"
)

//go:embed icons/*.svg
var icons embed.FS

// load returns an embedded icon as a Fyne resource; keeping the .svg
// extension engages Fyne's SVG renderer (and themed recoloring).
func load(name string) fyne.Resource {
	b, err := icons.ReadFile("icons/" + name)
	if err != nil {
		panic("resources: embedded icon missing: " + err.Error())
	}

	return fyne.NewStaticResource(name, b)
}

// CameraIcon is the application mark (window title bars, tray).
var CameraIcon = load("camera.svg")

// CrosshairIcon marks PTZ surfaces.
var CrosshairIcon = load("crosshair.svg")

// StarIcon marks the default profile.
var StarIcon = load("star.svg")

// PowerIcon marks the quit action.
var PowerIcon = load("power.svg")

// SunIcon heads the Exposure group.
var SunIcon = load("sun.svg")

// SlidersIcon heads the Image tuning group.
var SlidersIcon = load("sliders.svg")

// ThermometerIcon heads the White Balance group.
var ThermometerIcon = load("thermometer.svg")

// LensIcon heads the Focus / Lens group.
var LensIcon = load("lens.svg")

// ChipIcon marks generic control-class groups.
var ChipIcon = load("chip.svg")
