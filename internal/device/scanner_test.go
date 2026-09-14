package device

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// scanCollect drains scanner events until a timeout.
func scanCollect(t *testing.T, sc *Scanner, timeout time.Duration) []DeviceEvent {
	t.Helper()

	var got []DeviceEvent

	deadline := time.After(timeout)

	for {
		select {
		case ev := <-sc.Events():
			got = append(got, ev)
		case <-deadline:

			return got
		}
	}
}

// scanCollectUntil gathers events until pred is satisfied (deadline on
// failure). Used where fixture mutations can legitimately produce
// TRANSITIONAL identities: a scan racing a node deletion sees a partial
// endpoint set whose identity key differs from the full device — real
// hotplug never behaves this way (USB nodes vanish together), so the test
// tolerates the transitional events and asserts on the real key's event.
func scanCollectUntil(t *testing.T, sc *Scanner, pred func(ev []DeviceEvent) bool) []DeviceEvent {
	t.Helper()

	var got []DeviceEvent

	deadline := time.After(5 * time.Second)

	for {
		select {
		case ev := <-sc.Events():
			got = append(got, ev)

			if pred(got) {
				return got
			}
		case <-deadline:
			t.Fatalf("timed out waiting for expected scanner event; got %+v", got)
		}
	}
}

// plantNode recreates one sysfs v4l entry in the fixture.
func plantNode(t *testing.T, roots Roots, name, devNo string, index int, deviceRel string) {
	t.Helper()

	entry := filepath.Join(roots.SysClassV4L, name)

	if err := os.MkdirAll(entry, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(entry, "dev"), []byte(devNo+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(entry, "index"), []byte(strconv.Itoa(index)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if deviceRel != "" {
		if err := os.Symlink(deviceRel, filepath.Join(entry, "device")); err != nil {
			t.Fatal(err)
		}
	}
}

// unplantNode removes one sysfs v4l entry entirely (device gone).
func unplantNode(t *testing.T, roots Roots, name string) {
	t.Helper()

	if err := os.RemoveAll(filepath.Join(roots.SysClassV4L, name)); err != nil {
		t.Fatal(err)
	}
}

const (
	deviceRelCapture  = "../../../devices/pci0000:00/usb3/3-1/3-1:1.0"
	deviceRelMetadata = "../../../devices/pci0000:00/usb3/3-1/3-1:1.2"
)

func TestScannerInitialRemovedReconnected(t *testing.T) {
	roots := fixture(t)
	sc := NewScanner(roots, slog.New(slog.DiscardHandler), 20*time.Millisecond)
	sc.Start()
	defer sc.Stop()

	// First scan: initial discovery (NOT a reconnect).
	events := scanCollect(t, sc, time.Second)

	if len(events) != 1 || events[0].Kind != DeviceAddedInitial {
		t.Fatalf("first scan must be initial discovery: %+v", events)
	}

	key := events[0].Identity.Key

	// Device disappears entirely (both nodes).
	unplantNode(t, roots, "video0")
	unplantNode(t, roots, "video1")

	// The original identity must be reported removed (transitional
	// identities from mid-scan deletions are tolerated, see
	// scanCollectUntil).
	scanCollectUntil(t, sc, func(evs []DeviceEvent) bool {
		for _, ev := range evs {
			if ev.Kind == DeviceRemoved && ev.Identity.Key == key {
				return true
			}
		}

		return false
	})

	// Replant: genuine reconnect now.
	plantNode(t, roots, "video0", "81:0", 0, deviceRelCapture)
	plantNode(t, roots, "video1", "81:1", 1, deviceRelMetadata)

	// The reappearance of the ORIGINAL identity must be classified as a
	// reconnect (never initial discovery, never a duplicate).
	events = scanCollectUntil(t, sc, func(evs []DeviceEvent) bool {
		for _, ev := range evs {
			if ev.Identity.Key == key && ev.Kind != DeviceRemoved {
				return true
			}
		}

		return false
	})

	last := events[len(events)-1]

	if last.Kind != DeviceReconnected {
		t.Fatalf("reappearance must be a reconnect, got %+v", last)
	}

	// Stability: repeated scans produce no further lifecycle events for
	// the real device (a straggler transitional removal may still drain).
	if got := scanCollect(t, sc, 100*time.Millisecond); len(got) != 0 {
		for _, ev := range got {
			if ev.Identity.Key == key || ev.Kind == DeviceAddedInitial || ev.Kind == DeviceReconnected {
				t.Fatalf("stable tree must not emit lifecycle events: %+v", ev)
			}
		}
	}
}

func TestScannerNewDeviceLaterIsInitialDiscovery(t *testing.T) {
	roots := fixture(t)

	// Start with only the metadata node gone: the capture node forms a
	// single-endpoint device on first scan.
	unplantNode(t, roots, "video1")

	sc := NewScanner(roots, slog.New(slog.DiscardHandler), 15*time.Millisecond)
	sc.Start()
	defer sc.Stop()

	events := scanCollect(t, sc, time.Second)

	if len(events) != 1 || events[0].Kind != DeviceAddedInitial {
		t.Fatalf("initial event missing: %+v", events)
	}

	// A DIFFERENT physical device appearing later is still initial
	// discovery for THAT device (never a reconnect — it was never
	// connected before in this process).
	plantNode(t, roots, "video9", "81:9", 9, "")

	events = scanCollect(t, sc, time.Second)

	if len(events) != 1 || events[0].Kind != DeviceAddedInitial {
		t.Fatalf("new device must be initial discovery: %+v", events)
	}
}

func TestScannerTriggerScan(t *testing.T) {
	roots := fixture(t)
	sc := NewScanner(roots, slog.New(slog.DiscardHandler), time.Hour) // polling off
	sc.Start()
	defer sc.Stop()

	scanCollect(t, sc, time.Second) // initial

	// Out-of-band trigger discovers the change despite no polling.
	unplantNode(t, roots, "video0")
	unplantNode(t, roots, "video1")
	sc.TriggerScan()

	events := scanCollect(t, sc, time.Second)

	if len(events) != 1 || events[0].Kind != DeviceRemoved {
		t.Fatalf("triggered scan did not report removal: %+v", events)
	}
}

// Shutdown paths can run twice (tray quit, then the deferred teardown in
// main): a second Stop must be a no-op, not a panic on the stop channel.
func TestScannerStopIsIdempotent(t *testing.T) {
	roots := fixture(t)
	sc := NewScanner(roots, slog.New(slog.DiscardHandler), 20*time.Millisecond)
	sc.Start()

	sc.Stop()
	sc.Stop() // must not panic ("close of closed channel")
}
