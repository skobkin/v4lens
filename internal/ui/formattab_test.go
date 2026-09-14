package ui

import (
	"testing"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// The Input picker shows the plain input name; the numeric index is
// internal bookkeeping, not something the user picks by.
func TestInputLabelShowsNameOnly(t *testing.T) {
	if got := inputLabel(domain.InputInfo{Index: 0, Name: "Camera 1"}); got != "Camera 1" {
		t.Fatalf("inputLabel = %q, want the plain name", got)
	}

	// Nameless inputs still need a readable, pickable label.
	if got := inputLabel(domain.InputInfo{Index: 2}); got != "Input 2" {
		t.Fatalf("nameless inputLabel = %q", got)
	}
}
