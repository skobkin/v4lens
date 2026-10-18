// Command v4lens runs the V4Lens camera configuration tray application.
//
// Preferences and logging come up before anything else; the device app and
// the main window are wired here. Tray, single-instance and autostart
// integration land with the platform phase.
package main

import (
	"context"
	"errors"
	"flag"
	"os"

	"git.skobk.in/skobkin/v4lens/internal/app"
	"git.skobk.in/skobkin/v4lens/internal/buildinfo"
	"git.skobk.in/skobkin/v4lens/internal/config"
	"git.skobk.in/skobkin/v4lens/internal/device"
	"git.skobk.in/skobkin/v4lens/internal/domain"
	"git.skobk.in/skobkin/v4lens/internal/logging"
	"git.skobk.in/skobkin/v4lens/internal/platform"
	"git.skobk.in/skobkin/v4lens/internal/preview"
	"git.skobk.in/skobkin/v4lens/internal/profile"
	"git.skobk.in/skobkin/v4lens/internal/ui"
	"git.skobk.in/skobkin/v4lens/internal/v4l"
)

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	background := flag.Bool("background", false, "start with the main window hidden (tray only)")
	flag.Parse()

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
		"version", buildinfo.Version,
		"build_date", buildinfo.BuildDate,
		"config", dirs.AppConfigFile(),
	)

	// Single instance (flock): a second launch forwards activation to the
	// running one and exits successfully.
	lock, err := platform.AcquireInstanceLock(dirs)
	if err != nil {
		if errors.Is(err, platform.ErrInstanceAlreadyRunning) {
			logger.Info("another instance is running, forwarding activation")

			if err := platform.ForwardActivation(dirs); err != nil {
				logger.Warn("activation forward failed", "err", err)

				return err
			}

			return nil
		}

		logger.Error("instance lock failed", "err", err)

		return err
	}

	defer func() {
		if err := lock.Release(); err != nil {
			logger.Warn("release instance lock", "err", err)
		}
	}()

	// The preview manager closes over the app variable to break the
	// construction cycle: the app is built with the manager as its
	// StreamOwner, while the manager's opener resolves through the app.
	var application *app.App

	previews := preview.NewManager(func(ctx context.Context, key string, cfg device.StreamConfig) (device.Stream, error) {
		return application.OpenStream(ctx, key, cfg)
	}, logger)

	application, err = app.New(app.Options{
		Config:      config.NewStore(dirs.AppConfigFile()),
		Profiles:    profile.NewStore(dirs.ProfilesRoot(), logger),
		Log:         logger,
		StreamOwner: previews,
		OpenBackend: func(identity domain.DeviceIdentity) (device.Handle, error) {
			ep := identity.CaptureEndpoint()

			return v4l.NewHandle(ep.Node)
		},
		Roots: device.DefaultRoots(v4l.ClassifyNode),
	})
	if err != nil {
		logger.Error("app bootstrap failed", "err", err)

		return err
	}

	// Second-instance activation flows through the bus: the HTTP handler
	// goroutine never touches Fyne directly.
	activationSrv, err := platform.StartActivationServer(dirs, logger, func() {
		application.Bus().Publish(app.ActivationRequested{})
	})
	if err != nil {
		logger.Warn("activation server unavailable", "err", err)
	} else {
		defer func() {
			if err := activationSrv.Close(); err != nil {
				logger.Warn("close activation server", "err", err)
			}
		}()
	}

	defer application.Stop()

	// ui.Run subscribes the window to the bus and starts the application
	// layer afterwards, so initial discovery events reach the UI.
	ui.Run(application, previews, dirs, *background, logger)

	return nil
}
