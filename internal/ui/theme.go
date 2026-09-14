package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// v4lensTheme is the application theme: V4Lens colors for both the dark
// and the light variant. The variant itself comes from the desktop (the
// freedesktop settings portal supplies and live-updates it), so switching
// the system color scheme re-skins the app without a restart.
type v4lensTheme struct {
	fyne.Theme
}

// newTheme builds the application theme on top of the Fyne defaults:
// only the color names listed in Color() are overridden.
func newTheme() fyne.Theme {
	return v4lensTheme{Theme: theme.DefaultTheme()}
}

// palette is one variant's color set.
type palette struct {
	background, button, card, input, inputBorder color.NRGBA
	foreground, secondary, placeholder           color.NRGBA
	accent, separator, hover, pressed            color.NRGBA
	success, warning, failure, hyperlink         color.NRGBA
	scrollBar                                    color.NRGBA
	overlay                                      color.NRGBA
}

// nrgba expands a 0xRRGGBB value into an opaque color.
func nrgba(rgb uint32) color.NRGBA {
	return color.NRGBA{
		R: uint8((rgb >> 16) & 0xff), //nolint:gosec // byte extraction from the packed 0xRRGGBB literal; truncation is the point
		G: uint8((rgb >> 8) & 0xff),  //nolint:gosec // ditto
		B: uint8(rgb & 0xff),         //nolint:gosec // ditto
		A: 0xff,
	}
}

// translucent expands a 0xRRGGBB value into a color with the given alpha.
func translucent(rgb uint32, a uint8) color.NRGBA {
	c := nrgba(rgb)
	c.A = a

	return c
}

// darkPalette mirrors the approved concept (assets/v4lens-ui-concept.png).
var darkPalette = palette{
	background:  nrgba(0x161b22),
	button:      nrgba(0x21262d),
	card:        nrgba(0x1c2129),
	input:       nrgba(0x2a3038),
	inputBorder: nrgba(0x3d444d),
	foreground:  nrgba(0xe6e9ef),
	secondary:   nrgba(0x8b949e),
	placeholder: nrgba(0x6e7681),
	accent:      nrgba(0x3b82f6),
	separator:   nrgba(0x2f363d),
	hover:       nrgba(0x262c36),
	pressed:     nrgba(0x1f242c),
	success:     nrgba(0x3fb950),
	warning:     nrgba(0xd29922),
	failure:     nrgba(0xf85149),
	hyperlink:   nrgba(0x58a6ff),
	scrollBar:   nrgba(0x3d444d),
	overlay:     translucent(0x0d1117, 0xc0),
}

// lightPalette is the light counterpart of the concept (the concept image
// is dark-only; the light variant is derived to keep the same hierarchy).
var lightPalette = palette{
	background:  nrgba(0xf6f8fa),
	button:      nrgba(0xffffff),
	card:        nrgba(0xffffff),
	input:       nrgba(0xeaeef2),
	inputBorder: nrgba(0xd0d7de),
	foreground:  nrgba(0x1f2328),
	secondary:   nrgba(0x656d76),
	placeholder: nrgba(0x8c959f),
	accent:      nrgba(0x3b82f6),
	separator:   nrgba(0xd0d7de),
	hover:       nrgba(0xe9edf2),
	pressed:     nrgba(0xdfe4ea),
	success:     nrgba(0x1a7f37),
	warning:     nrgba(0x9a6700),
	failure:     nrgba(0xcf222e),
	hyperlink:   nrgba(0x0969da),
	scrollBar:   nrgba(0xc6cdd5),
	overlay:     translucent(0xf6f8fa, 0xc0),
}

// paletteFor resolves the variant: anything that is not explicitly light
// (including the portal's "unknown" placeholder) renders dark — the look
// the product was designed around.
func paletteFor(v fyne.ThemeVariant) palette {
	if v == theme.VariantLight {
		return lightPalette
	}

	return darkPalette
}

// Color overrides the anchor color names; everything else keeps the
// embedded default theme (shadows deliberately included — they drive the
// tab-bar divider and stock elevation looks).
func (t v4lensTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	p := paletteFor(variant)

	switch name {
	case theme.ColorNameBackground:
		return p.background
	case theme.ColorNameButton:
		return p.button
	case theme.ColorNameInputBackground:
		return p.input
	case theme.ColorNameInputBorder:
		return p.inputBorder
	case theme.ColorNameMenuBackground:
		return p.card
	case theme.ColorNameOverlayBackground:
		return p.overlay
	case theme.ColorNameHeaderBackground:
		return p.background
	case theme.ColorNameForeground:
		return p.foreground
	case theme.ColorNameDisabled:
		return p.secondary
	case theme.ColorNamePlaceHolder:
		return p.placeholder
	case theme.ColorNamePrimary:
		return p.accent
	case theme.ColorNameSeparator:
		return p.separator
	case theme.ColorNameHover:
		return p.hover
	case theme.ColorNamePressed:
		return p.pressed
	case theme.ColorNameSelection, theme.ColorNameFocus:
		return p.accent
	case theme.ColorNameScrollBar:
		return p.scrollBar
	case theme.ColorNameSuccess:
		return p.success
	case theme.ColorNameWarning:
		return p.warning
	case theme.ColorNameError:
		return p.failure
	case theme.ColorNameHyperlink:
		return p.hyperlink
	case theme.ColorNameForegroundOnPrimary,
		theme.ColorNameForegroundOnSuccess,
		theme.ColorNameForegroundOnWarning,
		theme.ColorNameForegroundOnError:

		return color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	default:

		return t.Theme.Color(name, variant)
	}
}
