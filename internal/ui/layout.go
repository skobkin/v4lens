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
