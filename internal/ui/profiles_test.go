package ui

import (
	"fmt"
	"testing"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestApplyResultScrollSizesToShortContent(t *testing.T) {
	test.NewApp()

	content := container.NewVBox(
		widget.NewLabel("Profile applied with issues (partially applied)."),
		widget.NewLabel("Brightness: skipped — control inactive at current exposure mode."),
	)

	got := applyResultScroll(content, applyResultMaxHeight).MinSize()
	want := content.MinSize()

	if got.Width != want.Width || got.Height != want.Height {
		t.Fatalf("short report viewport %v, want full content size %v", got, want)
	}
}

func TestApplyResultScrollCapsTallContent(t *testing.T) {
	test.NewApp()

	content := container.NewVBox()

	for i := range 40 {
		content.Add(widget.NewLabel(fmt.Sprintf("step %02d: skipped — device busy", i)))
	}

	got := applyResultScroll(content, applyResultMaxHeight).MinSize()

	if got.Height != applyResultMaxHeight {
		t.Fatalf("tall report viewport height %v, want cap %v", got.Height, applyResultMaxHeight)
	}

	if got.Width != content.MinSize().Width {
		t.Fatalf("tall report viewport width %v, want content width %v", got.Width, content.MinSize().Width)
	}
}
