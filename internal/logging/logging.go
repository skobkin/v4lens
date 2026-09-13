// Package logging owns the V4Lens application logger: slog setup, runtime
// level switching and the log-to-file path with stderr degradation
// (ARCHITECTURE §12, DECISION_LOG D010).
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ParseLevel maps a preference string to a slog level. The second result is
// false when the name is not one of debug/info/warn/error.
func ParseLevel(name string) (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":

		return slog.LevelDebug, true
	case "info", "":

		return slog.LevelInfo, true
	case "warn", "warning":

		return slog.LevelWarn, true
	case "error":

		return slog.LevelError, true
	default:

		return slog.LevelInfo, false
	}
}

// Options configure the application logger.
type Options struct {
	// Level is the initial level name; unknown names mean info.
	Level string

	// ToFile routes records to Path instead of stderr. When opening or
	// writing the file fails, the manager degrades to stderr and reports
	// the reason via DegradedReason (startup never fails because of the
	// log file).
	ToFile bool

	// Path is the log file path when ToFile is set (0600, appended).
	Path string

	// Stderr overrides the degradation sink; nil means os.Stderr.
	// Tests inject a buffer here.
	Stderr io.Writer
}

// Manager owns the logger lifecycle: the active sink, the switchable level
// and clean shutdown. All methods are safe for concurrent use.
type Manager struct {
	logger     *slog.Logger
	level      *slog.LevelVar
	stderr     io.Writer
	degradedMu sync.Mutex
	degraded   string
	closeMu    sync.Mutex
	file       *os.File
}

// New builds the manager and installs its logger via slog.SetDefault so
// library code logging through the default logger lands in the same sink.
func New(opts Options) *Manager {
	stderr := opts.Stderr

	if stderr == nil {
		stderr = os.Stderr
	}

	m := &Manager{
		level:  &slog.LevelVar{},
		stderr: stderr,
	}

	level, ok := ParseLevel(opts.Level)
	if !ok {
		level = slog.LevelInfo
	}

	m.level.Set(level)
	m.logger = slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: m.level}))

	if opts.ToFile {
		if file, err := openLogFile(opts.Path); err == nil {
			m.file = file
			m.logger = slog.New(slog.NewTextHandler(file, &slog.HandlerOptions{Level: m.level}))
		} else {
			m.degradedMu.Lock()
			m.degraded = err.Error()
			m.degradedMu.Unlock()
		}
	}

	slog.SetDefault(m.logger)

	return m
}

// Logger returns the application logger.
func (m *Manager) Logger() *slog.Logger {
	return m.logger
}

// DegradedReason returns non-empty when log-to-file was requested but the
// file could not be opened; the logger fell back to stderr.
func (m *Manager) DegradedReason() string {
	m.degradedMu.Lock()
	defer m.degradedMu.Unlock()

	return m.degraded
}

// SetLevel switches the level at runtime (Preferences "Log level").
func (m *Manager) SetLevel(name string) bool {
	level, ok := ParseLevel(name)
	if !ok {
		return false
	}

	m.level.Set(level)

	return true
}

// Close releases the log file, if one was opened.
func (m *Manager) Close() error {
	m.closeMu.Lock()
	defer m.closeMu.Unlock()

	if m.file == nil {
		return nil
	}

	err := m.file.Close()
	m.file = nil

	if err != nil {
		return fmt.Errorf("close log file: %w", err)
	}

	return nil
}

// openLogFile opens (and creates parents for) the log file for appending.
func openLogFile(path string) (*os.File, error) {
	if path == "" {
		return nil, fmt.Errorf("log file path is empty")
	}

	dir := filepath.Dir(path)

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create log dir %s: %w", dir, err)
	}

	//nolint:gosec // the path comes from the XDG configuration owned by the user, not from untrusted input
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	return file, nil
}
