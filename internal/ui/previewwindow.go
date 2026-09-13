package ui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"log/slog"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"git.skobk.in/skobkin/v4lens/internal/domain"
	"git.skobk.in/skobkin/v4lens/internal/preview"
)

// PreviewWindow is the streaming preview for one device: canvas image at
// FillModeContain, a truthful status line, and a translucent overlay while
// our own stream is frozen for a format reconfiguration. The window
// consumes the pipeline's event channel; all widget updates are dispatched
// with fyne.Do.
type PreviewWindow struct {
	fa     fyne.App
	mgr    *preview.Manager
	active func(ctx context.Context, key string) (domain.CaptureConfig, error)
	log    *slog.Logger

	// Dispatch defaults to fyne.Do; tests inject a synchronous runner.
	Dispatch func(fn func())

	win     fyne.Window
	key     string
	pipe    *preview.Pipeline
	done    chan struct{}
	stopped sync.Once

	image   *canvas.Image
	status  *widget.Label
	overlay *fyne.Container
	overTxt *widget.Label

	// fps measurement window (UI goroutine only).
	frames   int
	fpsStart time.Time
	lastFPS  float64
	fpsKnown bool
}

// dispatch runs fn on the UI goroutine.
func (w *PreviewWindow) dispatch(fn func()) {
	if w.Dispatch != nil {
		w.Dispatch(fn)

		return
	}

	fyne.Do(fn)
}

// NewPreviewWindow builds (but does not show) the preview window for key.
func NewPreviewWindow(fa fyne.App, mgr *preview.Manager,
	active func(ctx context.Context, key string) (domain.CaptureConfig, error), log *slog.Logger,
) *PreviewWindow {
	w := &PreviewWindow{
		fa:     fa,
		mgr:    mgr,
		active: active,
		log:    log,
		done:   make(chan struct{}),
	}

	w.image = canvas.NewImageFromImage(nil)
	w.image.FillMode = canvas.ImageFillContain

	w.status = widget.NewLabel("Starting…")
	w.status.TextStyle = fyne.TextStyle{Monospace: true}

	w.overTxt = widget.NewLabel("")
	w.overTxt.TextStyle = fyne.TextStyle{Italic: true}
	w.overTxt.Alignment = fyne.TextAlignCenter

	shade := canvas.NewRectangle(shadeColor())
	w.overlay = container.NewStack(shade, container.NewCenter(w.overTxt))
	w.overlay.Hide()

	w.win = fa.NewWindow("V4Lens — Preview")
	w.win.SetContent(container.NewBorder(nil, w.status, nil, nil, container.NewStack(w.image, w.overlay)))
	w.win.Resize(fyne.NewSize(800, 560))
	w.win.SetCloseIntercept(w.closeRequested)

	return w
}

// shadeColor is the translucent overlay backdrop (kept subtle over video).
func shadeColor() color.NRGBA {
	return color.NRGBA{A: 0x50}
}

// Show reveals the window and starts streaming key's effective capture
// configuration. Device I/O runs off the UI goroutine; events flow back
// through fyne.Do.
func (w *PreviewWindow) Show(key string) {
	if w.key == key && w.pipe != nil {
		w.win.RequestFocus()

		return
	}

	if w.pipe != nil {
		w.restartFor(key)

		return
	}

	w.key = key
	w.win.Show()

	go w.start(key)
}

// restartFor switches the window to another device: stop the old pipeline
// asynchronously, then start the new one.
func (w *PreviewWindow) restartFor(key string) {
	old := w.pipe
	oldKey := w.key

	w.key = key
	w.status.SetText("Starting…")
	w.setImage(nil)

	go func() {
		if old != nil {
			w.mgr.StopKey(oldKey)
		}

		w.start(key)
	}()
}

// start resolves the effective capture config and starts the pipeline.
// Runs on its own goroutine (never the UI thread).
func (w *PreviewWindow) start(key string) {
	cfg, err := w.active(context.Background(), key)
	if err != nil {
		w.dispatchStatus("✗ " + err.Error())

		return
	}

	p := w.mgr.PipelineFor(key)

	if err := p.Start(context.Background(), cfg); err != nil {
		w.dispatchStatus("✗ " + err.Error())

		return
	}

	w.pipe = p

	go w.consume(p)
}

// consume forwards pipeline events to the UI goroutine until the window is
// closed.
func (w *PreviewWindow) consume(p *preview.Pipeline) {
	events := p.Events()

	for {
		select {
		case <-w.done:

			return
		case ev, ok := <-events:
			if !ok {
				return
			}

			w.dispatchEvent(ev)
		}
	}
}

func (w *PreviewWindow) dispatchEvent(ev preview.Event) {
	w.dispatch(func() { w.applyEvent(ev) })
}

// applyEvent updates all widgets from one pipeline event (UI goroutine).
func (w *PreviewWindow) applyEvent(ev preview.Event) {
	switch ev.State {
	case preview.StateStreaming:
		w.setImage(ev.Img)
		w.overlay.Hide()
		w.tickFPS()
		w.status.SetText(previewStatusText(ev, w.lastFPS, w.fpsKnown))

	case preview.StateFrozen:
		if ev.Img != nil {
			w.setImage(ev.Img)
		}

		w.overTxt.SetText("Reconfiguring format…")
		w.overlay.Show()
		w.status.SetText(previewStatusText(ev, w.lastFPS, w.fpsKnown))
		w.resetFPS()

	case preview.StateUnsupported, preview.StateBusy, preview.StateError:
		w.overTxt.SetText(ev.Detail)
		w.overlay.Show()
		w.status.SetText(previewStatusText(ev, w.lastFPS, w.fpsKnown))
		w.resetFPS()

	case preview.StateStopped:
		w.status.SetText(previewStatusText(ev, w.lastFPS, w.fpsKnown))
		w.resetFPS()
	}
}

// tickFPS accumulates rendered frames over a one-second window (UI
// goroutine only); the status line reports the measured rate.
func (w *PreviewWindow) tickFPS() {
	now := time.Now()

	if w.fpsStart.IsZero() {
		w.fpsStart = now
	}

	w.frames++

	if elapsed := now.Sub(w.fpsStart); elapsed >= time.Second {
		w.lastFPS = float64(w.frames) / elapsed.Seconds()
		w.fpsKnown = true

		w.frames = 0
		w.fpsStart = now
	}
}

func (w *PreviewWindow) resetFPS() {
	w.frames = 0
	w.fpsStart = time.Time{}
}

func (w *PreviewWindow) setImage(img image.Image) {
	w.image.Image = img
	w.image.Refresh()
}

func (w *PreviewWindow) dispatchStatus(text string) {
	w.dispatch(func() { w.status.SetText(text) })
}

// closeRequested is the window close intercept: stop the event consumer
// and tear the pipeline down asynchronously (the UI never waits on device
// teardown).
func (w *PreviewWindow) closeRequested() {
	w.shutdown()
	w.win.Close()
}

// shutdown stops the consumer and the pipeline exactly once.
func (w *PreviewWindow) shutdown() {
	w.stopped.Do(func() {
		close(w.done)

		key := w.key

		go func() {
			w.mgr.StopKey(key)
			w.log.Debug("preview stopped", "device", key)
		}()
	})
}

// previewStatusText renders the truthful status line for one event. fps
// values are the MEASURED render rate (no driver promise).
func previewStatusText(ev preview.Event, fps float64, fpsKnown bool) string {
	switch ev.State {
	case preview.StateStreaming:
		s := fmt.Sprintf("● Previewing · %s · %dx%d", ev.Layout.FourCC, ev.Layout.Width, ev.Layout.Height)

		if fpsKnown {
			s += fmt.Sprintf(" · %.0f fps", fps)
		}

		return s
	case preview.StateFrozen:

		return "⏸ Frozen — reconfiguring format…"
	case preview.StateBusy:

		return "⏳ Device busy — " + ev.Detail
	case preview.StateUnsupported, preview.StateError:

		return "✗ " + ev.Detail
	default:

		return "Preview stopped"
	}
}
