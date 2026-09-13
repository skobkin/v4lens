package profile

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"git.skobk.in/skobkin/v4lens/internal/platform"
)

// Store manages per-device profile files under
// $XDG_CONFIG_HOME/v4lens/profiles/<device-key>/<profile-name>.yaml
// (ARCHITECTURE §4). Profiles are never removed merely because their device
// is disconnected; writes are atomic with restrictive permissions.
type Store struct {
	root string
	log  *slog.Logger
}

// NewStore builds a store rooted at root (platform.Dirs.ProfilesRoot()).
func NewStore(root string, log *slog.Logger) *Store {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Store{root: root, log: log}
}

// Root returns the store root directory.
func (s *Store) Root() string {
	return s.root
}

// nameRE keeps profile file names filesystem-safe and deterministic.
var nameRE = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// fileName maps a profile name to its file name (slug + .yaml).
func fileName(name string) (string, error) {
	slug := nameRE.ReplaceAllString(strings.TrimSpace(name), "-")
	slug = strings.Trim(slug, "-.")

	if slug == "" {
		return "", fmt.Errorf("profile name %q has no usable file name", name)
	}

	if len(slug) > 64 {
		slug = slug[:64]
	}

	return slug + ".yaml", nil
}

func (s *Store) deviceDir(key string) string {
	return filepath.Join(s.root, key)
}

// Path returns the file path one profile is stored at.
func (s *Store) Path(key, name string) (string, error) {
	f, err := fileName(name)
	if err != nil {
		return "", err
	}

	return filepath.Join(s.deviceDir(key), f), nil
}

// List returns the stored profile names for one device, sorted. A missing
// device directory yields an empty list, not an error.
func (s *Store) List(key string) ([]string, error) {
	entries, err := os.ReadDir(s.deviceDir(key))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("list profiles: %w", err)
	}

	out := make([]string, 0, len(entries))

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}

		out = append(out, strings.TrimSuffix(e.Name(), ".yaml"))
	}

	sort.Strings(out)

	return out, nil
}

// Load reads one profile.
func (s *Store) Load(key, name string) (*Profile, error) {
	path, err := s.Path(key, name)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path) //nolint:gosec // path built from the store root owned here
	if err != nil {
		return nil, fmt.Errorf("load profile %q: %w", name, err)
	}

	var in profileYAML

	if err := yaml.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("parse profile %q: %w", name, err)
	}

	p, err := profileFromYAML(in)
	if err != nil {
		return nil, fmt.Errorf("profile %q: %w", name, err)
	}

	if p.Name == "" {
		p.Name = name
	}

	return p, nil
}

// Save writes the profile atomically (temp file + rename) with 0600
// permissions.
func (s *Store) Save(key string, p *Profile) error {
	if p.Name == "" {
		return fmt.Errorf("profile name is empty")
	}

	p.SchemaVersion = SchemaVersion

	data, err := yaml.Marshal(profileToYAML(p))
	if err != nil {
		return fmt.Errorf("encode profile %q: %w", p.Name, err)
	}

	path, err := s.Path(key, p.Name)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create profile dir: %w", err)
	}

	if err := platform.WriteFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("save profile %q: %w", p.Name, err)
	}

	s.log.Debug("profile saved", "device", key, "profile", p.Name)

	return nil
}

// Delete removes one profile file.
func (s *Store) Delete(key, name string) error {
	path, err := s.Path(key, name)
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete profile %q: %w", name, err)
	}

	return nil
}

// Rename rewrites the profile under the new name (keeping content and
// stored name consistent) and removes the old file.
func (s *Store) Rename(key, oldName, newName string) error {
	p, err := s.Load(key, oldName)
	if err != nil {
		return err
	}

	p.Name = newName

	if err := s.Save(key, p); err != nil {
		return err
	}

	return s.Delete(key, oldName)
}
