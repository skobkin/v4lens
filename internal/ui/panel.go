package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"git.skobk.in/skobkin/v4lens/internal/domain"
	"git.skobk.in/skobkin/v4lens/internal/ui/viewmodel"
)

// ControlPanel renders one tab's controls: curated semantics place
// well-known controls into ordered groups (Image/PTZ), everything else
// lands in Advanced grouped by control class in driver order. Unknown
// controls never disappear.
type ControlPanel struct {
	hooks Hooks

	box       *fyne.Container
	renderers map[uint32]ControlRenderer
}

// NewControlPanel builds an empty panel.
func NewControlPanel(hooks Hooks) *ControlPanel {
	return &ControlPanel{
		hooks:     hooks,
		box:       container.NewVBox(),
		renderers: map[uint32]ControlRenderer{},
	}
}

// Object returns the panel canvas object.
func (p *ControlPanel) Object() fyne.CanvasObject {
	return p.box
}

// Renderer returns the live renderer for one control (nil when the panel
// does not show it).
func (p *ControlPanel) Renderer(id uint32) ControlRenderer {
	return p.renderers[id]
}

// SetContent rebuilds the panel from descriptors and current values.
// Values may lack entries for write-only controls; those render editable
// without an echo.
func (p *ControlPanel) SetContent(descs []domain.ControlDescriptor, values map[uint32]domain.ControlValue, advanced bool) {
	p.renderers = map[uint32]ControlRenderer{}
	p.box.Objects = nil

	if len(descs) == 0 {
		empty := widget.NewLabel("No controls in this tab.")
		empty.Alignment = fyne.TextAlignCenter

		p.box.Add(empty)

		return
	}

	for _, group := range groupControls(descs, advanced) {
		head := widget.NewLabel(group.title)
		head.TextStyle = fyne.TextStyle{Bold: true}

		p.box.Add(head)

		for _, d := range group.descs {
			var v *domain.ControlValue

			if val, ok := values[d.ID]; ok {
				v = &val
			}

			r := newControlRenderer(d, v, p.hooks)
			p.renderers[d.ID] = r
			p.box.Add(r.Object())

			// Replay current dependency flags into the fresh renderer.
			r.SetDisabled(disabledOf(d), disabledReasonOf(d))
		}
	}
}

// controlGroup is one titled block of controls.
type controlGroup struct {
	title string
	descs []domain.ControlDescriptor
}

// groupControls buckets descriptors into titled groups. Advanced mode
// groups by control class in driver order; curated tabs group by the
// curated table's group order.
func groupControls(descs []domain.ControlDescriptor, advanced bool) []controlGroup {
	type bucket struct {
		title string
		order int
		descs []domain.ControlDescriptor
	}

	var (
		buckets []*bucket
		byTitle = map[string]*bucket{}
	)

	bucketFor := func(title string, order int) *bucket {
		if b, ok := byTitle[title]; ok {
			return b
		}

		b := &bucket{title: title, order: order}
		byTitle[title] = b
		buckets = append(buckets, b)

		return b
	}

	for _, d := range descs {
		if advanced {
			// Advanced groups by driver control class in first-appearance
			// (driver) order — never by curated semantics.
			group := d.ClassName

			if group == "" {
				group = viewmodel.Classify(d).Group
			}

			b := bucketFor(group, len(buckets))
			b.descs = append(b.descs, d)

			continue
		}

		sem := viewmodel.Classify(d)

		// Safety: a control classified out of this tab still shows, in the
		// fallback group, never dropped.
		b := bucketFor(sem.Group, groupOrderOf(sem.Group))
		b.descs = append(b.descs, d)
	}

	// Stable sort buckets by declared order (Advanced keeps driver order).
	for i := 1; i < len(buckets); i++ {
		for j := i; j > 0 && buckets[j].order < buckets[j-1].order; j-- {
			buckets[j], buckets[j-1] = buckets[j-1], buckets[j]
		}
	}

	out := make([]controlGroup, 0, len(buckets))

	for _, b := range buckets {
		out = append(out, controlGroup{title: b.title, descs: b.descs})
	}

	return out
}

func groupOrderOf(group string) int {
	if o, ok := viewmodel.GroupOrder[group]; ok {
		return o
	}

	return 1 << 30
}

// disabledOf derives renderer disablement from the live flags.
func disabledOf(d domain.ControlDescriptor) bool {
	return d.CurFlags.Has(domain.FlagInactive) || d.CurFlags.Has(domain.FlagGrabbed)
}

// disabledReasonOf explains why an editor is disabled (truthful states
// only — no flag, no reason shown).
func disabledReasonOf(d domain.ControlDescriptor) string {
	switch {
	case d.CurFlags.Has(domain.FlagGrabbed):

		return "in use by another application"
	case d.CurFlags.Has(domain.FlagInactive):

		return "inactive — enable its prerequisite control first"
	default:

		return ""
	}
}
