package resources

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
)

// Every exported icon must resolve from the embedded set to non-empty SVG
// content — a broken //go:embed pattern or a renamed file must fail here,
// not panic at application startup.
func TestEmbeddedIconsResolve(t *testing.T) {
	icons := []fyne.Resource{
		CameraIcon,
		CrosshairIcon,
		StarIcon,
		PowerIcon,
		SunIcon,
		SlidersIcon,
		ThermometerIcon,
		LensIcon,
		ChipIcon,
	}

	if len(icons) == 0 {
		t.Fatal("icon set is empty")
	}

	for _, r := range icons {
		if !strings.HasSuffix(r.Name(), ".svg") {
			t.Fatalf("icon name %q must keep the .svg extension", r.Name())
		}

		content := string(r.Content())
		if !strings.Contains(content, "<svg") || !strings.Contains(content, "</svg>") {
			t.Fatalf("%s: content does not look like SVG: %.40q", r.Name(), content)
		}
	}
}
