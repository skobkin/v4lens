package device

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// BuildKey derives the stable, filesystem-safe device key from an identity
// source string: a slug of the source plus a short content hash, so keys
// are deterministic, collision-checkable and safe as profile directory
// names (ARCHITECTURE §4).
func BuildKey(source string) string {
	return BuildKeyOf(source, 48, 8)
}

// BuildKeyOf is the parameterized form used by tests.
func BuildKeyOf(source string, maxSlug, hashLen int) string {
	slug := slugify(source, maxSlug)
	sum := sha256.Sum256([]byte(source))

	return slug + "-" + hex.EncodeToString(sum[:])[:hashLen]
}

var slugNoise = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// slugify lowercases and replaces noise runs so the source becomes one
// path-safe token.
func slugify(s string, max int) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugNoise.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")

	if s == "" {
		s = "device"
	}

	if len(s) > max {
		s = strings.TrimRight(s[:max], "-")
	}

	return s
}

// identitySource picks the strongest identity source string available,
// mirroring the ARCHITECTURE §3 priority:
//  1. /dev/v4l/by-id entry (serial-backed, stable, unique);
//  2. USB serial + VID/PID;
//  3. /dev/v4l/by-path entry (port-stable, breaks on replug elsewhere);
//  4. sysfs physical path fingerprint (weakest, flagged as such).
type identitySource struct {
	value       string
	tier        int // 1..4, for diagnostics only
	byID        string
	byPath      string
	fingerprint string
}

func pickIdentitySource(byID, vid, pid, serial, byPath, physicalPath string) identitySource {
	switch {
	case byID != "":
		return identitySource{value: byID, tier: 1, byID: byID, byPath: byPath, fingerprint: physicalPath}
	case serial != "" && vid != "" && pid != "":
		return identitySource{
			value:       "vid_" + strings.ToLower(vid) + "_pid_" + strings.ToLower(pid) + "_serial_" + serial,
			tier:        2,
			byID:        byID,
			byPath:      byPath,
			fingerprint: physicalPath,
		}
	case byPath != "":
		return identitySource{value: byPath, tier: 3, byID: byID, byPath: byPath, fingerprint: physicalPath}
	default:
		return identitySource{value: physicalPath, tier: 4, byID: byID, byPath: byPath, fingerprint: physicalPath}
	}
}
