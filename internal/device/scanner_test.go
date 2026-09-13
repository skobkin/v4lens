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

	events = scanCollect(t, sc, time.Second)

	if len(events) != 1 || events[0].Kind != DeviceRemoved {
		t.Fatalf("expected removal event: %+v", events)
	}

	if events[0].Identity.Key != key {
		t.Fatalf("removal key mismatch: %q vs %q", events[0].Identity.Key, key)
	}

	// Replant: genuine reconnect now.
	plantNode(t, roots, "video0", "81:0", 0, deviceRelCapture)
	plantNode(t, roots, "video1", "81:1", 1, deviceRelMetadata)

	events = scanCollect(t, sc, time.Second)

	if len(events) != 1 || events[0].Kind != DeviceReconnected {
		t.Fatalf("reappearance must be a reconnect: %+v", events)
	}

	if events[0].Identity.Key != key {
		t.Fatalf("reconnect identity key changed: %q vs %q", events[0].Identity.Key, key)
	}

	// Stability: repeated scans produce no further events.
	if got := scanCollect(t, sc, 70*time.Millisecond); len(got) != 0 {
		t.Fatalf("stable tree must not emit events: %+v", got)
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
