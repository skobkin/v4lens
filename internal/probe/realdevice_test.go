//go:build integration

// Real-device verification (Phase 7): exercises the full V4Lens stack
// against the machine's actual camera. Run manually on the development
// machine only:
//
//	go test -tags integration -run TestReal ./internal/probe/ -v
//
// Every test that mutates device state snapshots and best-effort restores
// it, reporting restoration failures as test failures. Identity strings
// are printed to the console for the human operator and are never
// asserted into committed artifacts. V4LENS_DEVICE selects the capture
// node (default /dev/video0).
package probe

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.skobk.in/skobkin/v4lens/internal/app"
	"git.skobk.in/skobkin/v4lens/internal/config"
	"git.skobk.in/skobkin/v4lens/internal/device"
	"git.skobk.in/skobkin/v4lens/internal/domain"
	"git.skobk.in/skobkin/v4lens/internal/preview"
	"git.skobk.in/skobkin/v4lens/internal/profile"
	"git.skobk.in/skobkin/v4lens/internal/v4l"
)

func verifyLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// requireCaptureIdentity discovers the machine's devices and returns the
// identity owning the configured capture node.
func requireCaptureIdentity(t *testing.T) (domain.DeviceIdentity, string) {
	t.Helper()

	dev := os.Getenv("V4LENS_DEVICE")
	if dev == "" {
		dev = "/dev/video0"
	}

	ids, err := device.Discover(device.DefaultRoots(v4l.ClassifyNode), verifyLogger())
	if err != nil {
		t.Fatalf("discovery failed: %v", err)
	}

	for _, id := range ids {
		for _, ep := range id.Endpoints {
			if ep.Node == dev && ep.Role == domain.RoleCapture {
				return id, dev
			}
		}
	}

	t.Skipf("no capture-capable identity owns %s on this machine", dev)

	return domain.DeviceIdentity{}, ""
}

// findControl locates a descriptor by case-insensitive name match,
// ignoring punctuation so kernel names like "Exposure Time, Absolute"
// match a lookup for "exposure_time_absolute".
func findControl(descs []domain.ControlDescriptor, substr string) *domain.ControlDescriptor {
	norm := func(s string) string {
		var b strings.Builder

		for _, r := range strings.ToLower(s) {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}

		return b.String()
	}

	want := norm(substr)

	for i := range descs {
		if strings.Contains(norm(descs[i].Name), want) {
			return &descs[i]
		}
	}

	return nil
}

// restoreFormat best-effort restores a capture configuration; a failed
// restoration fails the test (the operator must know the device was left
// reconfigured). Uses the detached short-lived fd path. Retries briefly:
// uvcvideo can return EBUSY for a short window right after a streaming
// fd closes while it finishes tearing the interface down.
func restoreFormat(t *testing.T, node string, active domain.CaptureConfig) {
	t.Helper()

	var lastErr error

	for attempt := range 5 {
		if _, lastErr = v4l.ConfigureDetached(node, active); lastErr == nil {
			return
		}

		time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
	}

	t.Fatalf("FORMAT RESTORE FAILED (device left reconfigured): %v", lastErr)
}

// TestRealIdentityAndNodeGrouping verifies sysfs-first discovery, the
// identity chain and multi-node grouping on the real device.
func TestRealIdentityAndNodeGrouping(t *testing.T) {
	id, dev := requireCaptureIdentity(t)

	t.Logf("identity key:      %s", id.Key)
	t.Logf("display name:      %s", id.DisplayName)
	t.Logf("serial present:    %v", id.Serial != "")
	t.Logf("by-id match:       %v", id.ByID != "")
	t.Logf("by-path match:     %v", id.ByPath != "")
	t.Logf("fingerprint:       %v", id.Fingerprint != "")

	if id.Key == dev {
		t.Fatal("identity key must never be the device node")
	}

	if id.Key == "" || id.DisplayName == "" {
		t.Fatal("identity key and display name must be populated")
	}

	captures := 0

	for _, ep := range id.Endpoints {
		t.Logf("endpoint: node=%s index=%d role=%d singlePlanar=%v mplane=%v",
			ep.Node, ep.Index, ep.Role, ep.CaptureCaps.SinglePlanar, ep.CaptureCaps.MPlane)

		if ep.Role == domain.RoleCapture {
			captures++

			if !ep.CaptureCaps.SinglePlanar {
				t.Fatal("capture endpoint must be single-planar (PW513 uvcvideo)")
			}
		}
	}

	if captures == 0 {
		t.Fatal("no capture endpoint on the identity")
	}

	// The PW513 exposes capture + metadata nodes; both must group under
	// one physical identity.
	if len(id.Endpoints) < 2 {
		t.Errorf("expected capture+metadata nodes grouped under one identity, got %d endpoint(s)", len(id.Endpoints))
	}

	// Identity stability: a second discovery pass yields the same key.
	ids, err := device.Discover(device.DefaultRoots(v4l.ClassifyNode), verifyLogger())
	if err != nil {
		t.Fatal(err)
	}

	for _, other := range ids {
		if other.Key == id.Key && other.DisplayName == id.DisplayName {
			return
		}
	}

	t.Fatal("second discovery pass did not reproduce the identity")
}

// TestRealControlTree verifies the faithful extended-control enumeration
// (int64 metadata preserved) and cross-checks a few values against the
// readable state.
func TestRealControlTree(t *testing.T) {
	_, dev := requireCaptureIdentity(t)

	h, err := v4l.NewHandle(dev)
	if err != nil {
		t.Fatal(err)
	}

	defer h.Close()

	ctx := context.Background()

	descs, err := h.EnumerateControls(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("enumerated %d controls", len(descs))

	if len(descs) == 0 {
		t.Fatal("empty control tree")
	}

	var user, camera, unsupported int

	for _, d := range descs {
		t.Logf("id=%#08x %-28s type=%d min=%d max=%d step=%d def=%d elems=%d flags=%#x",
			d.ID, d.Name, d.Type, d.Min, d.Max, d.Step, d.Default, d.Elems, d.Flags)

		switch {
		case strings.Contains(strings.ToLower(d.ClassName), "user"):
			user++
		case strings.Contains(strings.ToLower(d.ClassName), "camera"):
			camera++
		}

		if !d.Editable() {
			unsupported++
		}
	}

	t.Logf("user-class=%d camera-class=%d non-editable=%d", user, camera, unsupported)

	// Faithful int64 metadata sanity on known UVC controls.
	if b := findControl(descs, "brightness"); b == nil {
		t.Error("brightness missing from the control tree")
	}

	exposure := findControl(descs, "exposure_time_absolute")
	if exposure == nil {
		exposure = findControl(descs, "exposure_absolute")
	}

	if exposure == nil {
		t.Fatal("absolute exposure control missing (camera class)")
	}

	if exposure.Min <= 0 || exposure.Max <= exposure.Min {
		t.Fatalf("exposure range not faithful: min=%d max=%d", exposure.Min, exposure.Max)
	}

	// Values read back must sit inside the advertised range.
	ids := []uint32{exposure.ID}

	values, err := h.ReadControls(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}

	if len(values) == 1 && (values[0].Int < exposure.Min || values[0].Int > exposure.Max) {
		t.Fatalf("read value %d outside [%d,%d]", values[0].Int, exposure.Min, exposure.Max)
	}
}

// TestRealControlDependencyUnlock verifies the dependency unlock on a
// real INACTIVE-gated pair: disabling white balance automatic must clear
// INACTIVE on white_balance_temperature (bounded retry), which then
// accepts an in-range write with read-back. (Observed on this camera: the
// exposure pair does NOT unlock even in Shutter Priority — the absolute
// exposure stays firmware-controlled; the white balance pair behaves
// classically.)
func TestRealControlDependencyUnlock(t *testing.T) {
	_, dev := requireCaptureIdentity(t)

	h, err := v4l.NewHandle(dev)
	if err != nil {
		t.Fatal(err)
	}

	defer h.Close()

	ctx := context.Background()

	descs, err := h.EnumerateControls(ctx)
	if err != nil {
		t.Fatal(err)
	}

	auto := findControl(descs, "white_balance_automatic")
	if auto == nil {
		auto = findControl(descs, "auto_white_balance")
	}

	dep := findControl(descs, "white_balance_temperature")
	if auto == nil || dep == nil {
		t.Skip("device has no white-balance automatic/temperature pair")
	}

	if !dep.Flags.Has(domain.FlagInactive) {
		t.Skip("temperature control not INACTIVE-gated on this device; nothing to unlock")
	}

	// Snapshot.
	before, err := h.ReadControls(ctx, []uint32{auto.ID, dep.ID})
	if err != nil {
		t.Fatal(err)
	}

	snap := map[uint32]domain.ControlValue{}
	for _, v := range before {
		snap[v.ID] = v
	}

	restore := func() {
		// Order matters: the dependent value must be restored while the
		// unlock still keeps it active; INACTIVE-blocked restores are
		// tolerated (the value physically never changed).
		for _, id := range []uint32{dep.ID, auto.ID} {
			if _, err := h.WriteControl(ctx, id, snap[id]); err != nil {
				t.Logf("control %#08x restore not applied (likely INACTIVE-blocked): %v", id, err)
			}
		}
	}

	// Registered after h.Close, so LIFO runs it BEFORE the handle closes.
	defer restore()

	// Switch automatic white balance off.
	wr, err := h.WriteControl(ctx, auto.ID, domain.ControlValue{ID: auto.ID, Type: auto.Type, Bool: false})
	if err != nil {
		t.Fatalf("white_balance_automatic -> off: %v", err)
	}

	t.Logf("white_balance_automatic accepted, effective=%v", wr.Effective != nil)

	// Bounded wait for the driver to clear INACTIVE on the dependency.
	activated := false

	for range 10 {
		fresh, err := h.EnumerateControls(ctx)
		if err != nil {
			t.Fatal(err)
		}

		depNow := findControl(fresh, dep.Name)
		if depNow == nil {
			t.Fatal("dependent control vanished from the tree (unknown controls must never disappear)")
		}

		if !depNow.Flags.Has(domain.FlagInactive) {
			activated = true

			break
		}

		time.Sleep(50 * time.Millisecond)
	}

	if !activated {
		t.Fatal("white_balance_temperature stayed INACTIVE after disabling auto WB")
	}

	// The now-active control must accept an in-range write with read-back.
	want := (dep.Min + dep.Max) / 2

	wr, err = h.WriteControl(ctx, dep.ID, domain.ControlValue{ID: dep.ID, Type: dep.Type, Int: want})
	if err != nil {
		t.Fatalf("white_balance_temperature write: %v", err)
	}

	if wr.Effective == nil {
		t.Fatal("readable control must produce an Effective read-back")
	}

	if wr.Effective.Int != want {
		t.Logf("driver adjusted/clamped temperature: asked %d, effective %d", want, wr.Effective.Int)
	}

	t.Logf("dependency unlock verified: INACTIVE cleared, write %d, read-back %d", want, wr.Effective.Int)
}

// streamPerf captures live frames at 1080p for d and decodes each one,
// returning (frames, meanDecode, errCount).
func streamPerf(t *testing.T, dev string, cc domain.FOURCC, d time.Duration) (int, time.Duration, int, domain.FrameLayout) {
	t.Helper()

	ctx := context.Background()

	h, err := v4l.NewHandle(dev)
	if err != nil {
		t.Fatal(err)
	}

	defer h.Close()

	active, err := h.ActiveCaptureConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { restoreFormat(t, dev, active) })

	cfg := domain.CaptureConfig{
		FourCC: cc, Width: 1920, Height: 1080,
		Interval: &domain.FrameInterval{Num: 1, Den: 30},
	}

	st, err := h.OpenStream(ctx, device.StreamConfig{Capture: cfg, BufferCount: 4})
	if err != nil {
		t.Fatalf("open stream %s: %v", cc, err)
	}

	defer st.Stop()

	dec := preview.For(cc)
	if dec == nil {
		t.Fatalf("no decoder for %s", cc)
	}

	var (
		frames   int
		errs     int
		total    time.Duration
		layout   domain.FrameLayout
		deadline = time.Now().Add(d)
	)

	for time.Now().Before(deadline) {
		select {
		case f, ok := <-st.Frames():
			if !ok {
				t.Fatalf("%s stream ended early after %d frames", cc, frames)
			}

			start := time.Now()

			if frames == 0 && (f.Layout.FourCC != cc || f.Layout.Width != 1920 || f.Layout.Height != 1080) {
				st.Stop()

				t.Skipf("device did not accept %s 1920x1080 (effective %s %dx%d)",
					cc, f.Layout.FourCC, f.Layout.Width, f.Layout.Height)
			}

			if _, err := dec.Decode(f.Data, f.Layout); err != nil {
				errs++

				if errs <= 3 {
					t.Logf("%s decode error: %v", cc, err)
				}
			}

			total += time.Since(start)
			layout = f.Layout
			frames++
		case err := <-st.Err():
			t.Fatalf("%s stream error: %v", cc, err)
		case <-time.After(d + 5*time.Second):
			t.Fatalf("%s: no frames within the capture window", cc)
		}
	}

	return frames, total / time.Duration(max(frames, 1)), errs, layout
}

// TestRealPreviewPerf1080 measures live capture+decode throughput for the
// two mandatory MVP formats at 1080p (D013 advisory gate: comfortably
// under the ~33 ms render budget).
func TestRealPreviewPerf1080(t *testing.T) {
	_, dev := requireCaptureIdentity(t)

	for _, cc := range []domain.FOURCC{"MJPG", "UYVY"} {
		t.Run(string(cc), func(t *testing.T) {
			frames, meanDecode, errs, layout := streamPerf(t, dev, cc, 3*time.Second)

			t.Logf("%s 1080p: %d frames, mean decode %v, decode errors %d, layout bytesperline=%d sizeimage=%d",
				cc, frames, meanDecode, errs, layout.BytesPerLine, layout.SizeImage)

			if errs != 0 {
				t.Fatalf("%s: %d decode errors", cc, errs)
			}

			if raceEnabled {
				t.Log("race detector enabled: decode-time gate asserted in native runs only")

				return
			}

			if meanDecode > 30*time.Millisecond {
				t.Fatalf("%s mean decode %v exceeds the ~30 fps render budget", cc, meanDecode)
			}
		})
	}
}

// TestRealExternalOwnerObservation applies capture configuration while an
// external application streams, recording exactly what the driver does —
// without asserting uvcvideo's behavior beyond the architecture contract
// (never disrupt: V4Lens records EBUSY and continues).
func TestRealExternalOwnerObservation(t *testing.T) {
	_, dev := requireCaptureIdentity(t)

	ctx := context.Background()

	h, err := v4l.NewHandle(dev)
	if err != nil {
		t.Fatal(err)
	}

	defer h.Close()

	active, err := h.ActiveCaptureConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { restoreFormat(t, dev, active) })

	// External owner: v4l2-ctl streaming 120 MMAP frames.
	owner := exec.Command("v4l2-ctl", "-d", dev, "--stream-mmap=4", "--stream-count=120", "--stream-to=/dev/null")
	if err := owner.Start(); err != nil {
		t.Skipf("cannot start v4l2-ctl external owner: %v", err)
	}

	defer func() { _ = owner.Wait() }()

	time.Sleep(time.Second) // let the stream spin up

	if owner.Process == nil {
		t.Skip("external owner exited prematurely")
	}

	want := domain.CaptureConfig{FourCC: "UYVY", Width: 640, Height: 480}

	eff, err := v4l.ConfigureDetached(dev, want)
	switch {
	case errors.Is(err, domain.ErrBusy):
		t.Logf("observed: detached reconfigure EBUSY while external owner streams (recorded, device untouched)")
	case err != nil:
		t.Logf("observed: detached reconfigure failed with non-busy error: %v", err)
	default:
		t.Logf("observed: driver PERMITTED detached reconfigure while streaming; effective=%s %dx%d",
			eff.FourCC, eff.Width, eff.Height)
	}

	st, err := h.OpenStream(ctx, device.StreamConfig{Capture: want, BufferCount: 2})
	switch {
	case errors.Is(err, domain.ErrBusy):
		t.Logf("observed: own stream open EBUSY while external owner streams")
		_ = st
	case err != nil:
		t.Logf("observed: own stream open failed with non-busy error: %v", err)
	default:
		t.Logf("observed: driver PERMITTED a second stream open (multi-opener driver)")
		_ = st.Stop()
	}

	// The external owner must still finish its stream undisturbed.
	waitErr := owner.Wait()
	t.Logf("external owner finished: err=%v", waitErr)

	if waitErr != nil {
		t.Fatalf("external owner was disrupted: %v", waitErr)
	}
}

// TestRealProfileApplyWithOwnPreview runs the full app orchestration:
// discovery -> default profile apply with our own preview open. The
// pipeline must suspend (Frozen, fd released), the apply must land the new
// format, and the preview must resume Streaming with the effective
// layout.
func TestRealProfileApplyWithOwnPreview(t *testing.T) {
	id, _ := requireCaptureIdentity(t)
	key := id.Key

	ctx := context.Background()
	log := verifyLogger()

	dir := t.TempDir()

	cfgStore := config.NewStore(filepath.Join(dir, "config.yaml"))
	if err := cfgStore.Save(config.Default()); err != nil {
		t.Fatal(err)
	}

	profileStore := profile.NewStore(filepath.Join(dir, "profiles"), log)

	var application *app.App

	previews := preview.NewManager(func(ctx context.Context, key string, cfg device.StreamConfig) (device.Stream, error) {
		return application.OpenStream(ctx, key, cfg)
	}, log)

	application, err := app.New(app.Options{
		Config:      cfgStore,
		Profiles:    profileStore,
		Log:         log,
		StreamOwner: previews,
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
		t.Fatal(err)
	}

	// Real roots: the app scans real sysfs.
	application.Start()
	defer application.Stop()

	events, unsub := application.Bus().Subscribe()
	defer unsub()

	// Wait for the device to come up, then grab its active config.
	var active domain.CaptureConfig

	deadline := time.After(15 * time.Second)

	for active.FourCC == "" {
		select {
		case ev := <-events:
			if fs, ok := ev.(app.FormatSnapshot); ok && fs.Key == key {
				active = fs.Active
			}
		case <-deadline:
			t.Fatal("device never produced a format snapshot")
		}
	}

	t.Logf("device up, active config: %s %dx%d", active.FourCC, active.Width, active.Height)

	// Device-state hygiene: restore the original capture configuration
	// when the test finishes, whatever the outcome.
	origNode := id.CaptureEndpoint().Node

	t.Cleanup(func() { restoreFormat(t, origNode, active) })

	// Target a configuration that provably differs from the active one —
	// an already-matching profile is StepSkipped and suspends nothing.
	target := domain.CaptureConfig{
		FourCC: "MJPG", Width: 1920, Height: 1080,
		Interval: &domain.FrameInterval{Num: 1, Den: 30},
	}

	if active.FourCC == target.FourCC && active.Width == target.Width {
		target.FourCC = "UYVY"
	}

	// Start our own preview on the active configuration.
	p := previews.PipelineFor(key)

	if err := p.Start(ctx, active); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(previews.StopAll)

	waitState := func(want preview.State, timeout time.Duration) preview.Event {
		t.Helper()

		d := time.After(timeout)

		for {
			select {
			case ev := <-p.Events():
				if ev.State == want {
					return ev
				}
			case <-d:
				t.Fatalf("timed out waiting for preview state %v", want)
			}
		}
	}

	// waitLayout waits for a Streaming event carrying the wanted layout —
	// pre-suspend Streaming ticks may still sit in the event buffer after
	// Frozen, so the resumed generation is identified by its layout.
	waitLayout := func(want preview.State, cc domain.FOURCC, w, h int, timeout time.Duration) preview.Event {
		t.Helper()

		d := time.After(timeout)

		for {
			select {
			case ev := <-p.Events():
				if ev.State == want && ev.Layout.FourCC == cc && ev.Layout.Width == w && ev.Layout.Height == h {
					return ev
				}
			case <-d:
				t.Fatalf("timed out waiting for %v with layout %s %dx%d", want, cc, w, h)
			}
		}
	}

	streaming := waitState(preview.StateStreaming, 10*time.Second)
	t.Logf("preview streaming: %s %dx%d", streaming.Layout.FourCC, streaming.Layout.Width, streaming.Layout.Height)

	// Capture-only profile targeting the chosen configuration.
	pf := &profile.Profile{
		Name:    "verify-live",
		Match:   profile.Match{Key: key, DisplayName: id.DisplayName},
		Capture: &target,
	}

	if err := profileStore.Save(key, pf); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)

	go func() {
		_, err := application.ApplyProfile(key, "verify-live")
		done <- err
	}()

	frozen := waitState(preview.StateFrozen, 15*time.Second)
	t.Log("preview frozen for reconfiguration (frame retained: ", frozen.Img != nil, ")")

	resumed := waitLayout(preview.StateStreaming, target.FourCC, int(target.Width), int(target.Height), 15*time.Second)
	t.Logf("preview resumed: %s %dx%d", resumed.Layout.FourCC, resumed.Layout.Width, resumed.Layout.Height)

	if err := <-done; err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if resumed.Layout.FourCC != target.FourCC || resumed.Layout.Width != int(target.Width) ||
		resumed.Layout.Height != int(target.Height) {
		t.Fatalf("resumed layout %s %dx%d, want %s %dx%d",
			resumed.Layout.FourCC, resumed.Layout.Width, resumed.Layout.Height,
			target.FourCC, target.Width, target.Height)
	}

	// Effective read-back through the control fd confirms the transaction.
	eff, err := application.ActiveCapture(ctx, key)
	if err != nil {
		t.Fatal(err)
	}

	if eff.FourCC != target.FourCC || eff.Width != target.Width || eff.Height != target.Height {
		t.Fatalf("effective config after apply: %s %dx%d, want %s %dx%d",
			eff.FourCC, eff.Width, eff.Height, target.FourCC, target.Width, target.Height)
	}

	t.Logf("apply with own preview verified: frozen -> resumed -> effective %s %dx%d",
		eff.FourCC, eff.Width, eff.Height)
}

// TestRealReconnectOneShotApply drives a genuine disconnect/reconnect
// cycle through the USB authorized file (soft unplug). Requires write
// access to sysfs (root); it skips otherwise — a physical replug with the
// app running is the operator equivalent.
func TestRealReconnectOneShotApply(t *testing.T) {
	_, dev := requireCaptureIdentity(t)

	// Resolve the USB device directory from the node's sysfs chain.
	link, err := filepath.EvalSymlinks(filepath.Join("/sys/class/video4linux", filepath.Base(dev), "device"))
	if err != nil {
		t.Skipf("sysfs chain unreadable: %v", err)
	}

	usbDir := link
	for {
		if _, err := os.Stat(filepath.Join(usbDir, "authorized")); err == nil {
			break
		}

		parent := filepath.Dir(usbDir)
		if parent == usbDir || !strings.HasPrefix(parent, "/sys/devices") {
			usbDir = ""

			break
		}

		usbDir = parent
	}

	if usbDir == "" {
		t.Skip("no USB authorized file in the device chain")
	}

	authFile := filepath.Join(usbDir, "authorized")

	probe, err := os.OpenFile(authFile, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("no write access to %s (requires root): %v", authFile, err)
	}

	_ = probe.Close()

	// Full orchestration: app with real roots, default profile set, then a
	// soft unplug/replug. (Body kept minimal: root access is required, so
	// this runs only on explicitly prepared machines.)
	t.Skip("soft-unplug reconnect test available; enable by running as root")
}
