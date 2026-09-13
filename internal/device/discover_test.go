package device

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// fixture builds a synthetic sysfs/dev tree shaped like the real PW513
// dual-node layout with clearly synthetic identifiers (TEST- serial etc.)
// and returns Roots pointed at it.
func fixture(t *testing.T) Roots {
	t.Helper()

	base := t.TempDir()

	sysClass := filepath.Join(base, "sys", "class", "video4linux")
	devDir := filepath.Join(base, "dev")
	byID := filepath.Join(devDir, "v4l", "by-id")
	byPath := filepath.Join(devDir, "v4l", "by-path")

	for _, d := range []string{sysClass, byID, byPath,
		filepath.Join(sysClass, "video0"), filepath.Join(sysClass, "video1"),
	} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}

	// Interface + physical device dirs.
	intf0 := filepath.Join(base, "sys", "devices", "pci0000:00", "usb3", "3-1", "3-1:1.0")
	intf2 := filepath.Join(base, "sys", "devices", "pci0000:00", "usb3", "3-1", "3-1:1.2")
	phys := filepath.Join(base, "sys", "devices", "pci0000:00", "usb3", "3-1")

	for _, d := range []string{intf0, intf2} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}

	write := func(path, content string) {
		t.Helper()

		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// video0: capture, interface 1.0.
	write(filepath.Join(sysClass, "video0", "dev"), "81:0\n")
	write(filepath.Join(sysClass, "video0", "index"), "0\n")

	if err := os.Symlink(filepath.Join("..", "..", "..", "devices", "pci0000:00", "usb3", "3-1", "3-1:1.0"), filepath.Join(sysClass, "video0", "device")); err != nil {
		t.Fatal(err)
	}

	// video1: metadata, interface 1.2.
	write(filepath.Join(sysClass, "video1", "dev"), "81:1\n")
	write(filepath.Join(sysClass, "video1", "index"), "1\n")

	if err := os.Symlink(filepath.Join("..", "..", "..", "devices", "pci0000:00", "usb3", "3-1", "3-1:1.2"), filepath.Join(sysClass, "video1", "device")); err != nil {
		t.Fatal(err)
	}

	// Physical USB device attributes — synthetic values only.
	write(filepath.Join(phys, "idVendor"), "1a86\n")
	write(filepath.Join(phys, "idProduct"), "7523\n")
	write(filepath.Join(phys, "serial"), "TEST-PW513-0001\n")
	write(filepath.Join(phys, "product"), "Synthetic Cam 4K\n")

	// by-id / by-path matching aids.
	if err := os.Symlink(filepath.Join("..", "..", "video0"), filepath.Join(byID, "usb-Synthetic_Cam_4K_TEST-PW513-0001-video-index0")); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(filepath.Join("..", "..", "video0"), filepath.Join(byPath, "pci-0000:00-usb-3-1_1.0-video-index0")); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(filepath.Join("..", "..", "video1"), filepath.Join(byPath, "pci-0000:00-usb-3-1_1.2-video-index1")); err != nil {
		t.Fatal(err)
	}

	return Roots{
		SysClassV4L:  sysClass,
		DevDir:       devDir,
		DevV4LByID:   byID,
		DevV4LByPath: byPath,
		Classify: func(node string) (NodeClass, error) {
			if filepath.Base(node) == "video0" {
				return NodeClass{Card: "Synthetic Cam 4K", SinglePlanar: true}, nil
			}

			return NodeClass{Metadata: true}, nil
		},
		// Privileged-free stand-in for the real char-device check.
		CheckNode: func(sysEntry, devNode string) bool {
			return filepath.Base(sysEntry) == filepath.Base(devNode)
		},
	}
}

func TestDiscoverGroupsDualNodeDevice(t *testing.T) {
	roots := fixture(t)

	devices, err := Discover(roots, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	if len(devices) != 1 {
		t.Fatalf("expected one physical device, got %d: %+v", len(devices), devices)
	}

	d := devices[0]

	if len(d.Endpoints) != 2 {
		t.Fatalf("expected two endpoints, got %+v", d.Endpoints)
	}

	// by-id (tier 1) wins and drives the key.
	wantSource := "usb-Synthetic_Cam_4K_TEST-PW513-0001-video-index0"

	if d.Key != BuildKey(wantSource) {
		t.Fatalf("key = %q, want %q", d.Key, BuildKey(wantSource))
	}

	if d.Serial != "TEST-PW513-0001" || d.USBVendor != "1a86" || d.USBProduct != "7523" {
		t.Fatalf("usb attrs wrong: %+v", d)
	}

	if d.ByID != wantSource {
		t.Fatalf("by-id = %q", d.ByID)
	}

	if d.DisplayName != "Synthetic Cam 4K" {
		t.Fatalf("display name = %q", d.DisplayName)
	}

	// Capture endpoint first (interface 1.0 sorts before 1.2).
	if d.Endpoints[0].Role != domain.RoleCapture || d.Endpoints[1].Role != domain.RoleMetadata {
		t.Fatalf("endpoint roles wrong: %+v", d.Endpoints)
	}

	if d.Endpoints[0].Index != 0 || d.Endpoints[1].Index != 1 {
		t.Fatalf("endpoint indexes wrong: %+v", d.Endpoints)
	}

	if d.Endpoints[0].Node != filepath.Join(roots.DevDir, "video0") {
		t.Fatalf("capture node wrong: %+v", d.Endpoints[0])
	}
}

func TestDiscoverSeriallessByPathFallback(t *testing.T) {
	roots := fixture(t)

	// Remove serial + by-id: identity falls to by-path (tier 3).
	if err := os.Remove(filepath.Join(roots.PhysicalPathOf(t), "serial")); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(roots.DevV4LByID, "usb-Synthetic_Cam_4K_TEST-PW513-0001-video-index0")); err != nil {
		t.Fatal(err)
	}

	devices, err := Discover(roots, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	if len(devices) != 1 {
		t.Fatalf("expected one device, got %d", len(devices))
	}

	wantSource := "pci-0000:00-usb-3-1_1.0-video-index0"

	if devices[0].Key != BuildKey(wantSource) {
		t.Fatalf("by-path fallback key = %q, want %q", devices[0].Key, BuildKey(wantSource))
	}

	if devices[0].Serial != "" {
		t.Fatalf("serial should be empty: %q", devices[0].Serial)
	}
}

func TestDiscoverVirtualDeviceFingerprint(t *testing.T) {
	base := t.TempDir()

	sysClass := filepath.Join(base, "sys", "class", "video4linux")
	devDir := filepath.Join(base, "dev")

	if err := os.MkdirAll(sysClass, 0o750); err != nil {
		t.Fatal(err)
	}

	intf := filepath.Join(base, "sys", "devices", "virtual", "video4linux", "video0")

	if err := os.MkdirAll(intf, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(sysClass, "video0"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(sysClass, "video0", "dev"), []byte("81:9\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("../../../devices/virtual/video4linux/video0", filepath.Join(sysClass, "video0", "device")); err != nil {
		t.Fatal(err)
	}

	roots := Roots{
		SysClassV4L:  sysClass,
		DevDir:       devDir,
		DevV4LByID:   filepath.Join(devDir, "v4l", "by-id"),
		DevV4LByPath: filepath.Join(devDir, "v4l", "by-path"),
		Classify: func(string) (NodeClass, error) {
			return NodeClass{Card: "Synthetic Loopback", SinglePlanar: true}, nil
		},
		CheckNode: func(sysEntry, devNode string) bool {
			return filepath.Base(sysEntry) == filepath.Base(devNode)
		},
	}

	devices, err := Discover(roots, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	if len(devices) != 1 {
		t.Fatalf("expected one device, got %d", len(devices))
	}

	d := devices[0]

	// No by-id/by-path/serial: the physical path is the fingerprint.
	if d.Key != BuildKey(d.PhysicalPath) {
		t.Fatalf("fingerprint key = %q, want %q", d.Key, BuildKey(d.PhysicalPath))
	}

	if d.Fingerprint == "" {
		t.Fatal("fingerprint must be set for tier-4 identity")
	}
}

// PhysicalPathOf exposes the fixture's physical device dir for mutations.
func (r Roots) PhysicalPathOf(t *testing.T) string {
	t.Helper()

	return filepath.Join(filepath.Dir(r.SysClassV4L), "..", "devices", "pci0000:00", "usb3", "3-1")
}
