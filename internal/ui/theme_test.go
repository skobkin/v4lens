package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Both variants must resolve every overridden anchor to an opaque color,
// and the variants must stay distinguishable (a broken palette table that
// returns the same struct for both would fail the difference checks).
func TestThemeProvidesBothVariants(t *testing.T) {
	th := newTheme()

	anchors := []fyne.ThemeColorName{
		theme.ColorNameBackground,
		theme.ColorNameForeground,
		theme.ColorNameInputBackground,
		theme.ColorNameSeparator,
		theme.ColorNameSuccess,
		theme.ColorNameWarning,
		theme.ColorNameError,
	}

	for _, name := range anchors {
		dark := asNRGBA(t, th.Color(name, theme.VariantDark))
		light := asNRGBA(t, th.Color(name, theme.VariantLight))

		if dark.A != 0xff || light.A != 0xff {
			t.Fatalf("%s: palette colors must be opaque (dark %v, light %v)", name, dark, light)
		}

		if dark == light {
			t.Fatalf("%s: dark and light palettes must differ", name)
		}
	}

	// The accent is shared by design; the backgrounds are what actually
	// distinguish the variants.
	if asNRGBA(t, th.Color(theme.ColorNamePrimary, theme.VariantDark)) !=
		asNRGBA(t, th.Color(theme.ColorNamePrimary, theme.VariantLight)) {
		t.Fatal("accent must be identical across variants")
	}

	// Unknown color names fall through to the embedded default theme.
	if th.Color(theme.ColorNameShadow, theme.VariantDark) == nil {
		t.Fatal("fallthrough colors must still resolve")
	}
}

// asNRGBA requires the palette to hand out NRGBA values directly (both the
// overridden anchors and the default-theme fallthrough do).
func asNRGBA(t *testing.T, c color.Color) color.NRGBA {
	t.Helper()

	n, ok := c.(color.NRGBA)
	if !ok {
		t.Fatalf("expected color.NRGBA, got %T", c)
	}

	return n
}
