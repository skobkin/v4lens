package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

// fixedWidthLayout forces its single child to an exact width, taking the
// available height. Fyne sizes Border/HBox slots to the child's MinSize,
// so wrapping a widget here reserves a usable, uniform width — entries
// wide enough for their values (no inner scrollbars, aligned edges) and
// selects wide enough for their labels.
type fixedWidthLayout struct {
	width float32
}

func (f fixedWidthLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objects {
		o.Resize(fyne.NewSize(f.width, size.Height))
		o.Move(fyne.NewPos(0, 0))
	}
}

func (f fixedWidthLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	h := float32(0)

	for _, o := range objects {
		if mh := o.MinSize().Height; mh > h {
			h = mh
		}
	}

	return fyne.NewSize(f.width, h)
}

// withFixedWidth wraps one widget so it always occupies the given width.
func withFixedWidth(width float32, obj fyne.CanvasObject) fyne.CanvasObject {
	return container.New(fixedWidthLayout{width: width}, obj)
}

// controlLabelWidth is the shared control-name column width: comfortable
// for the longest curated names; longer driver names wrap to a second
// line. Every row of a tab shares it, so the editor columns align.
const controlLabelWidth = 200

// spacedVBoxLayout stacks objects full-width with a fixed vertical gap —
// group cards need more air between them than a plain VBox's theme
// padding (the padding is fixed at theme construction time).
type spacedVBoxLayout struct {
	gap float32
}

// Layout packs the objects into a single column with the fixed gap.
func (v spacedVBoxLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)

	for _, child := range objects {
		if !child.Visible() {
			continue
		}

		child.Move(fyne.NewPos(0, y))
		child.Resize(fyne.NewSize(size.Width, child.MinSize().Height))
		y += child.MinSize().Height + v.gap
	}
}

// MinSize sums the children and their gaps.
func (v spacedVBoxLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	w, h := float32(0), float32(0)
	first := true

	for _, child := range objects {
		if !child.Visible() {
			continue
		}

		childMin := child.MinSize()
		w = fyne.Max(w, childMin.Width)

		if !first {
			h += v.gap
		}

		h += childMin.Height
		first = false
	}

	return fyne.NewSize(w, h)
}

// newSpacedVBox stacks objects with a fixed vertical gap.
func newSpacedVBox(gap float32, objects ...fyne.CanvasObject) *fyne.Container {
	return container.New(spacedVBoxLayout{gap: gap}, objects...)
}
