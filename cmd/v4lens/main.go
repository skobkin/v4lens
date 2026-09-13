// Command v4lens runs the V4Lens camera configuration tray application.
//
// This is the Phase 1 bootstrap: preferences and logging come up before
// anything else; device, UI, tray and single-instance wiring are added by
// the later phases.
package main

import (
	"os"

	"git.skobk.in/skobkin/v4lens/internal/config"
	"git.skobk.in/skobkin/v4lens/internal/logging"
	"git.skobk.in/skobkin/v4lens/internal/platform"
)

// version is overridden at build time via -ldflags.
var version = "0.0.0-dev"

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	dirs := platform.NewDirs()

	prefs, err := config.NewStore(dirs.AppConfigFile()).Load()
	if err != nil {
		// No logger exists yet, and per ARCHITECTURE §12 startup must not
		// fail because of configuration problems; continue with defaults.
		prefs = config.Default()
	}

	mgr := logging.New(logging.Options{
		Level:  prefs.LogLevel,
		ToFile: prefs.LogToFile,
		Path:   dirs.LogFile(),
	})
	defer func() { _ = mgr.Close() }()

	logger := mgr.Logger()

	if reason := mgr.DegradedReason(); reason != "" {
		logger.Warn("file logging unavailable, using stderr", "reason", reason)
	}

	logger.Info("v4lens starting",
		"version", version,
		"config", dirs.AppConfigFile(),
	)

	return nil
}
