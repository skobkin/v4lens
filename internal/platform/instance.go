package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrInstanceAlreadyRunning is returned by AcquireInstanceLock when another
// process already holds the single-instance lock.
var ErrInstanceAlreadyRunning = errors.New("instance already running")

// instanceLockFilename lives in the V4Lens runtime directory.
const instanceLockFilename = "instance.lock"

// InstanceLock is one acquired single-instance lock. The advisory flock is
// dropped when the process exits (by kernel fd cleanup) or via Release.
type InstanceLock struct {
	file *os.File
}

// AcquireInstanceLock takes an exclusive non-blocking flock on
// $XDG_RUNTIME_DIR/v4lens/instance.lock (private /tmp fallback per
// RuntimeDir). Lock contention means another instance of this user is
// running; that is reported as ErrInstanceAlreadyRunning.
func AcquireInstanceLock(dirs Dirs) (*InstanceLock, error) {
	dir := dirs.AppRuntimeDir()

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create instance lock dir: %w", err)
	}

	path := filepath.Join(dir, instanceLockFilename)

	// G304: path is the app-owned lock file under the runtime directory.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("open instance lock file: %w", err)
	}

	fd, err := fdInt(file)
	if err != nil {
		_ = file.Close()

		return nil, err
	}

	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()

		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrInstanceAlreadyRunning
		}

		return nil, fmt.Errorf("acquire instance file lock: %w", err)
	}

	return &InstanceLock{file: file}, nil
}

// Release drops the flock and closes the lock file. Releasing an already
// released lock is a no-op.
func (l *InstanceLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}

	fd, err := fdInt(l.file)
	if err == nil {
		if unlockErr := syscall.Flock(fd, syscall.LOCK_UN); unlockErr != nil && !errors.Is(unlockErr, syscall.EBADF) {
			_ = l.file.Close()
			l.file = nil

			return fmt.Errorf("unlock instance file lock: %w", unlockErr)
		}
	}

	closeErr := l.file.Close()
	l.file = nil

	if closeErr != nil {
		return fmt.Errorf("close instance lock file: %w", closeErr)
	}

	return nil
}

// fdInt converts an os.File's descriptor for syscall use without a
// silent uintptr truncation.
func fdInt(file *os.File) (int, error) {
	if file == nil {
		return 0, errors.New("instance lock file is nil")
	}

	fd := file.Fd()
	maxInt := uint64(^uint(0) >> 1)

	if uint64(fd) > maxInt {
		return 0, fmt.Errorf("file descriptor %d exceeds int range", fd)
	}

	return int(fd), nil
}
