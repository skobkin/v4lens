package device

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// Roots abstracts the filesystem locations discovery reads. Tests point
// these at a synthetic sysfs/dev fixture shaped like the real layout but
// carrying clearly synthetic identifiers (never real serials or topology).
type Roots struct {
	SysClassV4L  string // /sys/class/video4linux
	DevDir       string // /dev
	DevV4LByID   string // /dev/v4l/by-id
	DevV4LByPath string // /dev/v4l/by-path
	Classify     NodeClassifier

	// CheckNode verifies a /dev node exists for a sysfs entry. Nil means
	// the real char-device check (tests inject a privileged-free stub).
	CheckNode func(sysEntry, devNode string) bool
}

func (r Roots) check(sysEntry, devNode string) bool {
	if r.CheckNode != nil {
		return r.CheckNode(sysEntry, devNode)
	}

	return charNodeMatches(filepath.Dir(sysEntry), filepath.Base(sysEntry), devNode)
}

// DefaultRoots binds discovery to the real system locations.
func DefaultRoots(classify NodeClassifier) Roots {
	return Roots{
		SysClassV4L:  "/sys/class/video4linux",
		DevDir:       "/dev",
		DevV4LByID:   "/dev/v4l/by-id",
		DevV4LByPath: "/dev/v4l/by-path",
		Classify:     classify,
	}
}

// Discover enumerates physical devices sysfs-first (the source of truth),
// cross-checks node existence in /dev, classifies nodes via QUERYCAP and
// resolves one stable identity per physical device. It never issues S_INPUT
// and never keeps device fds open.
func Discover(roots Roots, log *slog.Logger) ([]domain.DeviceIdentity, error) {
	entries, err := os.ReadDir(roots.SysClassV4L)
	if err != nil {
		return nil, err
	}

	byID := symlinkMap(roots.DevV4LByID)
	byPath := symlinkMap(roots.DevV4LByPath)

	type nodeInfo struct {
		name    string
		devPath string // resolved sysfs device (interface) path
		intf    string // interface suffix (e.g. "1.0" / ":1.0" tail)
		index   int
		class   NodeClass
	}

	nodes := make([]nodeInfo, 0, len(entries))

	for _, e := range entries {
		name := e.Name()

		node := filepath.Join(roots.DevDir, name)

		if !roots.check(filepath.Join(roots.SysClassV4L, name), node) {
			log.Warn("sysfs v4l entry has no matching /dev char device, skipping", "entry", name)

			continue
		}

		class, err := roots.Classify(node)
		if err != nil {
			log.Warn("node classification failed, skipping", "node", node, "err", err)

			continue
		}

		if class.Role() == domain.RoleOther {
			continue // output/radio nodes are not consumable here
		}

		info := nodeInfo{name: name, class: class, devPath: resolveSysfsDevice(filepath.Join(roots.SysClassV4L, name))}
		info.index = sysfsInt(filepath.Join(roots.SysClassV4L, name, "index"))
		info.intf = interfaceSuffix(info.devPath)

		nodes = append(nodes, info)
	}

	// Group nodes by physical parent device.
	type physGroup struct {
		physicalPath string
		usb          usbAttrs
		nodes        []nodeInfo
	}

	groups := map[string]*physGroup{}

	for _, n := range nodes {
		physical, usb := physicalDeviceOf(n.devPath)

		g := groups[physical]

		if g == nil {
			g = &physGroup{physicalPath: physical, usb: usb}
			groups[physical] = g
		}

		g.nodes = append(g.nodes, n)
	}

	// Deterministic ordering.
	keys := make([]string, 0, len(groups))

	for k := range groups {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := make([]domain.DeviceIdentity, 0, len(groups))

	for _, k := range keys {
		g := groups[k]

		// Stable endpoint order: interface suffix, then sysfs index.
		sort.Slice(g.nodes, func(i, j int) bool {
			if g.nodes[i].intf != g.nodes[j].intf {
				return g.nodes[i].intf < g.nodes[j].intf
			}

			return g.nodes[i].index < g.nodes[j].index
		})

		capture := ""
		card := ""

		for _, n := range g.nodes {
			if n.class.Role() == domain.RoleCapture && capture == "" {
				capture = n.name
			}

			if card == "" && n.class.Card != "" {
				card = n.class.Card
			}
		}

		// Identity aid entries, preferring the capture node.
		idName := byID[capture]
		pathName := byPath[capture]

		if idName == "" {
			for _, n := range g.nodes {
				if byID[n.name] != "" {
					idName = byID[n.name]

					break
				}
			}
		}

		if pathName == "" {
			for _, n := range g.nodes {
				if byPath[n.name] != "" {
					pathName = byPath[n.name]

					break
				}
			}
		}

		src := pickIdentitySource(idName, g.usb.vid, g.usb.pid, g.usb.serial, pathName, g.physicalPath)

		if card == "" {
			card = sysfsString(filepath.Join(g.physicalPath, "product"))

			if card == "" {
				card = src.value
			}
		}

		identity := domain.DeviceIdentity{
			Key:          BuildKey(src.value),
			DisplayName:  card,
			USBVendor:    g.usb.vid,
			USBProduct:   g.usb.pid,
			Serial:       g.usb.serial,
			ByID:         src.byID,
			ByPath:       src.byPath,
			PhysicalPath: g.physicalPath,
			Fingerprint:  src.fingerprint,
		}

		for i, n := range g.nodes {
			identity.Endpoints = append(identity.Endpoints, domain.EndpointInfo{
				Node:        filepath.Join(roots.DevDir, n.name),
				Index:       i,
				Role:        n.class.Role(),
				CaptureCaps: n.class.CaptureCaps(),
			})
		}

		out = append(out, identity)
	}

	return out, nil
}

// usbAttrs carries the USB identification read from the physical device's
// sysfs directory.
type usbAttrs struct {
	vid, pid, serial string
}

// symlinkMap maps node basenames to their /dev/v4l/by-{id,path} entry names
// (e.g. "video0" -> "usb-...-video-index0"), the direction identity
// resolution consumes.
func symlinkMap(dir string) map[string]string {
	out := map[string]string{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return out // by-id/by-path are matching aids only; absence is fine
	}

	for _, e := range entries {
		target, err := os.Readlink(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}

		out[filepath.Base(target)] = e.Name()
	}

	return out
}

// charNodeMatches verifies the /dev node exists and is a character device
// with the same major:minor as the sysfs entry ("dev" file).
func charNodeMatches(sysClassDir, entry, devNode string) bool {
	st, err := os.Stat(devNode)
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return false
	}

	devStr, err := os.ReadFile(filepath.Join(sysClassDir, entry, "dev")) //nolint:gosec // path built from the sysfs class directory owned here
	if err != nil {
		return false
	}

	majorMinor := strings.TrimSpace(string(devStr))

	want := strings.SplitN(majorMinor, ":", 2)
	if len(want) != 2 {
		return false
	}

	sys, ok := st.Sys().(*syscall.Stat_t)

	if !ok {
		return false
	}

	major := (sys.Rdev >> 8) & 0xFFF
	minor := (sys.Rdev & 0xFF) | ((sys.Rdev >> 12) & 0xFFF00)

	return strconv.FormatUint(major, 10) == want[0] && strconv.FormatUint(minor, 10) == want[1]
}

// resolveSysfsDevice resolves the "device" symlink of a class entry to the
// interface device's absolute sysfs path ("" when absent). Relative targets
// resolve against the symlink's own directory (the class entry), matching
// kernel symlink semantics.
func resolveSysfsDevice(classEntry string) string {
	target, err := os.Readlink(filepath.Join(classEntry, "device"))
	if err != nil {
		return ""
	}

	if !filepath.IsAbs(target) {
		target = filepath.Join(classEntry, target)
	}

	return filepath.Clean(target)
}

// physicalDeviceOf climbs from the interface device to the physical device:
// the nearest ancestor exposing USB identification, else the interface path
// itself (virtual devices).
func physicalDeviceOf(interfacePath string) (string, usbAttrs) {
	dir := interfacePath

	for dir != "/" && dir != "." && dir != "" {
		if _, err := os.Stat(filepath.Join(dir, "idVendor")); err == nil {
			return dir, usbAttrs{
				vid:    sysfsString(filepath.Join(dir, "idVendor")),
				pid:    sysfsString(filepath.Join(dir, "idProduct")),
				serial: sysfsString(filepath.Join(dir, "serial")),
			}
		}

		prev := dir
		dir = filepath.Dir(dir)

		if prev == dir {
			break
		}
	}

	return interfacePath, usbAttrs{}
}

// interfaceSuffix extracts the stable interface part of a devpath tail
// like ".../3-2.2.1:1.0" -> "1.0"; virtual devices yield the path tail.
func interfaceSuffix(devPath string) string {
	if devPath == "" {
		return ""
	}

	tail := filepath.Base(devPath)

	if i := strings.LastIndex(tail, ":"); i >= 0 {
		return tail[i+1:]
	}

	return tail
}

func sysfsString(path string) string {
	b, err := os.ReadFile(path) //nolint:gosec // path constructed from the sysfs root owned here
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(b))
}

func sysfsInt(path string) int {
	s := sysfsString(path)

	v, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}

	return v
}
