//go:build integration

// Real-device GUI verification (Phase 7): drives the real Fyne UI against
// the real camera. TestMain keeps fa.Run on the process main thread (the
// GL driver requires it); the scripted sequence hops onto the UI loop via
// the driver's RunOnMain. Run with:
//
//	go test -tags integration -run TestRealGUI -v -timeout 180s ./internal/ui/
//
// While it runs, the preview window and the frozen overlay can be
// observed (or screenshotted) on the live desktop.
package ui

import (
	"context"
	"errors"
	"fmt"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"

	"git.skobk.in/skobkin/v4lens/internal/app"
	"git.skobk.in/skobkin/v4lens/internal/config"
	"git.skobk.in/skobkin/v4lens/internal/device"
	"git.skobk.in/skobkin/v4lens/internal/domain"
	"git.skobk.in/skobkin/v4lens/internal/platform"
	"git.skobk.in/skobkin/v4lens/internal/preview"
	"git.skobk.in/skobkin/v4lens/internal/profile"
	"git.skobk.in/skobkin/v4lens/internal/v4l"
)

// stampedEvent pairs a pipeline event with its arrival time so waits can
// ignore stale pre-apply events.
type stampedEvent struct {
	ev preview.Event
	at time.Time
}

var (
	guiExitCode   = 1
	guiPass       func()
	guiLogger     *slog.Logger
	guiPhase      func(format string, args ...any)
	guiWaitState  func(want preview.State, d time.Duration)
	guiStates     chan stampedEvent
	guiDeviceKey  string
	guiDevNode    string
	guiRestoreCfg domain.CaptureConfig
	guiRestoreMu  = make(chan error, 1)
	guiWaitSince  time.Time
	guiFa         fyne.App
	guiMgr        *preview.Manager
	guiPipe       *preview.Pipeline
	guiMW         *MainWindow
	guiProfiles   *profile.Store
)

func TestMain(m *testing.M) {
	if !guiAvailable() {
		os.Exit(m.Run())
	}

	os.Exit(guiMain())
}

// guiAvailable reports whether the real-device GUI run was requested.
func guiAvailable() bool {
	return os.Getenv("V4LENS_GUI") == "1"
}

func guiMain() int {
	guiLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	dev := os.Getenv("V4LENS_DEVICE")
	if dev == "" {
		dev = "/dev/video0"
	}

	guiDevNode = dev

	ids, err := device.Discover(device.DefaultRoots(v4l.ClassifyNode), guiLogger)
	if err != nil {
		guiLogger.Error("discover", "err", err)

		return 1
	}

	for i := range ids {
		for _, ep := range ids[i].Endpoints {
			if ep.Node == dev && ep.Role == domain.RoleCapture {
				guiDeviceKey = ids[i].Key

				break
			}
		}
	}

	if guiDeviceKey == "" {
		guiLogger.Error("no capture identity owns the device", "dev", dev)

		return 1
	}

	dir, _ := os.MkdirTemp("", "v4lens-gui-verify")
	cfgStore := config.NewStore(filepath.Join(dir, "config.yaml"))
	_ = cfgStore.Save(config.Default())
	guiProfiles = profile.NewStore(filepath.Join(dir, "profiles"), guiLogger)

	// Snapshot the active format for the final restore.
	snap, err := v4l.NewHandle(dev)
	if err != nil {
		guiLogger.Error("open", "err", err)

		return 1
	}

	guiRestoreCfg, err = snap.ActiveCaptureConfig(context.Background())
	_ = snap.Close()

	if err != nil {
		guiLogger.Error("snapshot", "err", err)

		return 1
	}

	guiLogger.Info("original config", "fourcc", guiRestoreCfg.FourCC,
		"size", fmt.Sprintf("%dx%d", guiRestoreCfg.Width, guiRestoreCfg.Height))

	var application *app.App

	guiMgr = preview.NewManager(func(ctx context.Context, key string, cfg device.StreamConfig) (device.Stream, error) {
		return application.OpenStream(ctx, key, cfg)
	}, guiLogger)

	application, err = app.New(app.Options{
		Config:      cfgStore,
		Profiles:    guiProfiles,
		Log:         guiLogger,
		StreamOwner: guiMgr,
		Roots:       device.DefaultRoots(v4l.ClassifyNode),
		OpenBackend: func(identity domain.DeviceIdentity) (device.Handle, error) {
			ep := identity.CaptureEndpoint()
			if ep == nil {
				return nil, errors.New("no capture endpoint")
			}

			return v4l.NewHandle(ep.Node)
		},
	})
	if err != nil {
		guiLogger.Error("app", "err", err)

		return 1
	}

	guiStates = make(chan stampedEvent, 64)
	guiPhase = func(format string, args ...any) {
		guiLogger.Info(fmt.Sprintf("PHASE: "+format, args...))
	}

	guiWaitState = func(want preview.State, d time.Duration) {
		deadline := time.After(d)

		for {
			select {
			case se := <-guiStates:
				if se.ev.State == want && !se.at.Before(guiWaitSince) {
					guiWaitSince = time.Now()

					return
				}
			case <-deadline:
				guiLogger.Error("timeout waiting for state", "want", want.String())

				guiFa.Quit()
			}
		}
	}

	guiFa = fyneapp.New()
	// Empty lookup: platform falls back to the real $HOME-based XDG paths.
	guiMW = NewMainWindow(guiFa, application, platform.ForTest(func(string) string { return "" }, os.Getuid()), guiLogger)
	guiMW.SetPreviewManager(guiMgr)
	guiMW.Show()
	guiMW.Start()

	time.AfterFunc(300*time.Millisecond, application.Start)

	// The scripted sequence; window operations hop onto the UI loop.
	time.AfterFunc(2*time.Second, guiSequence)

	guiFa.Run()

	return guiExitCode
}

// guiSequence drives preview open -> live -> apply (frozen) -> resume,
// using the exact code path the Preview button uses.
func guiSequence() {
	guiPhase("opening preview via the Preview-button code path")
	guiMW.previewDevice(guiDeviceKey)

	// previewDevice dispatches via fyne.Do; wait for the pipeline to appear.
	deadline := time.Now().Add(10 * time.Second)

	for guiPipe = guiMgr.Lookup(guiDeviceKey); guiPipe == nil; guiPipe = guiMgr.Lookup(guiDeviceKey) {
		if time.Now().After(deadline) {
			guiLogger.Error("pipeline never appeared")

			guiFa.Quit()

			return
		}

		time.Sleep(100 * time.Millisecond)
	}

	go func() {
		captured := map[preview.State]bool{}

		for {
			select {
			case ev := <-guiPipe.Events():
				guiLogger.Info("pipeline", "state", ev.State.String(),
					"layout", fmt.Sprintf("%s %dx%d", ev.Layout.FourCC, ev.Layout.Width, ev.Layout.Height))

				// Deterministic on-screen captures of each decisive
				// moment (canvas capture runs on the UI loop). The
				// PREVIEW window is captured — the main window may be
				// occluded and read back black.
				if !captured[ev.State] && (ev.State == preview.StateStreaming || ev.State == preview.StateFrozen) {
					captured[ev.State] = true

					name := "/tmp/v4lens-live.png"
					if ev.State == preview.StateFrozen {
						name = "/tmp/v4lens-frozen.png"
					}

					fyne.Do(func() {
						if guiMW.previewWin == nil || guiMW.previewWin.win == nil {
							return
						}

						img := guiMW.previewWin.win.Canvas().Capture()
						f, ferr := os.Create(name)
						if ferr == nil {
							_ = png.Encode(f, img)
							_ = f.Close()
						}

						guiLogger.Info("captured window", "state", ev.State.String(), "file", name)
					})
				}

				select {
				case guiStates <- stampedEvent{ev: ev, at: time.Now()}:
				default:
				}
			case <-time.After(120 * time.Second):
				return
			}
		}
	}()

	go func() {
		guiWaitState(preview.StateStreaming, 20*time.Second)
		guiPhase("LIVE PREVIEW — screenshot now")
		time.Sleep(8 * time.Second)

		// Capture-only profile that provably differs from the active config.
		target := domain.CaptureConfig{
			FourCC: "MJPG", Width: 1920, Height: 1080,
			Interval: &domain.FrameInterval{Num: 1, Den: 30},
		}

		if guiRestoreCfg.FourCC == target.FourCC && guiRestoreCfg.Width == target.Width {
			target.FourCC = "UYVY"
		}

		if err := guiProfiles.Save(guiDeviceKey, &profile.Profile{
			Name:    "verify-live",
			Capture: &target,
		}); err != nil {
			guiLogger.Error("save profile", "err", err)
		}

		guiPhase("APPLYING FORMAT CHANGE — frozen overlay expected")
		guiWaitSince = time.Now()

		go func() {
			// The apply itself runs on the session mutation goroutine.
			res, err := guiMW.app.ApplyProfile(guiDeviceKey, "verify-live")
			guiLogger.Info("apply returned", "err", err)
			if err != nil {
				return
			}

			guiLogger.Info("apply finished", "status", res.Status.String())
		}()

		guiWaitState(preview.StateStreaming, 25*time.Second)
		guiPhase("PREVIEW RESUMED — screenshot the resumed window")
		time.Sleep(6 * time.Second)

		// Tear down and restore the original configuration.
		guiMW.app.Stop()
		guiMgr.StopAll()
		time.Sleep(500 * time.Millisecond)

		_, err := v4l.ConfigureDetached(guiDevNode, guiRestoreCfg)
		guiRestoreMu <- err

		guiExitCode = 0
		guiFa.Quit()
	}()
}

// TestRealGUIPreviewFrozenOverlay is the driver entry point; the actual
// verification happens in guiMain/guiSequence (the pass criteria are the
// observed pipeline states plus the on-screen overlay).
func TestRealGUIPreviewFrozenOverlay(t *testing.T) {
	if os.Getenv("V4LENS_GUI") != "1" {
		t.Skip("set V4LENS_GUI=1 to run the real-device GUI verification")
	}

	// guiMain already ran via TestMain before any test executes; nothing
	// to do here but report. In GUI mode the process exits from TestMain,
	// so this body only executes for the skip path.
	if guiExitCode != 0 {
		t.Fatal("GUI verification sequence failed (see log)")

		return
	}

	t.Log("GUI preview verification completed")
}
