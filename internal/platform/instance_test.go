package platform

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testDirs builds a Dirs view rooted at a fresh temp tree. XDG_RUNTIME_DIR
// and XDG_CONFIG_HOME both point inside it; HOME keeps the fallbacks off
// the developer's real directories.
func testDirs(t *testing.T) (Dirs, string) {
	t.Helper()

	root := t.TempDir()

	dirs := ForTest(func(name string) string {
		switch name {
		case "XDG_RUNTIME_DIR":

			return filepath.Join(root, "runtime")
		case "XDG_CONFIG_HOME":

			return filepath.Join(root, "config")
		case "XDG_DATA_HOME":

			return filepath.Join(root, "data")
		case "HOME":

			return filepath.Join(root, "home")
		default:

			return ""
		}
	}, 4242)

	return dirs, root
}

func TestInstanceLockAcquireAndContention(t *testing.T) {
	dirs, _ := testDirs(t)

	lock, err := AcquireInstanceLock(dirs)
	if err != nil {
		t.Fatal(err)
	}

	// A second lock attempt (even in the same process — flock conflicts on
	// the new open file description) must be rejected.
	if _, err := AcquireInstanceLock(dirs); !errors.Is(err, ErrInstanceAlreadyRunning) {
		t.Fatalf("second acquire: %v, want ErrInstanceAlreadyRunning", err)
	}

	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}

	// After release the lock is free again; Release is idempotent.
	if err := lock.Release(); err != nil {
		t.Fatalf("double release: %v", err)
	}

	lock2, err := AcquireInstanceLock(dirs)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}

	if err := lock2.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestInstanceLockPath(t *testing.T) {
	dirs, root := testDirs(t)

	lock, err := AcquireInstanceLock(dirs)
	if err != nil {
		t.Fatal(err)
	}

	defer lock.Release()

	path := filepath.Join(root, "runtime", "v4lens", "instance.lock")

	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("lock file missing: %v", err)
	}

	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("lock file perm %o, want 600", perm)
	}
}

func TestStartActivationServerAndForward(t *testing.T) {
	dirs, _ := testDirs(t)

	activated := make(chan struct{}, 1)

	srv, err := StartActivationServer(dirs, nil, func() { activated <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}

	if err := ForwardActivation(dirs); err != nil {
		t.Fatalf("forward: %v", err)
	}

	select {
	case <-activated:
	case <-time.After(3 * time.Second):
		t.Fatal("activation handler never ran")
	}

	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}

	// The state file is cleaned up with the server.
	if _, err := os.Stat(filepath.Join(dirs.AppRuntimeDir(), activationStateFilename)); !os.IsNotExist(err) {
		t.Fatalf("activation state file survived Close: %v", err)
	}
}

func TestForwardActivationWrongTokenRejected(t *testing.T) {
	dirs, _ := testDirs(t)

	activated := make(chan struct{}, 1)

	srv, err := StartActivationServer(dirs, nil, func() { activated <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}

	defer srv.Close()

	// Tamper with the token VALUE (a changed key would not help: Go's JSON
	// decoder matches field names case-insensitively): the server must
	// reject the request.
	statePath := filepath.Join(dirs.AppRuntimeDir(), activationStateFilename)

	data, err := os.ReadFile(statePath) // #nosec G304 -- test-owned temp path
	if err != nil {
		t.Fatal(err)
	}

	var state activationState

	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}

	tampered := `{"address":"` + state.Address + `","token":"` + strings.Repeat("0", 64) + `"}`

	if err := os.WriteFile(statePath, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ForwardActivation(dirs); err == nil {
		t.Fatal("forward with wrong token must fail")
	}

	select {
	case <-activated:
		t.Fatal("handler must not run for rejected requests")
	default:
	}
}

func TestForwardActivationStaleState(t *testing.T) {
	dirs, _ := testDirs(t)

	// No server ever ran: state file absent.
	if err := ForwardActivation(dirs); err == nil {
		t.Fatal("forward without state file must fail")
	}

	// Stale state from a crashed predecessor: forward fails, too.
	srv, err := StartActivationServer(dirs, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}

	if err := ForwardActivation(dirs); err == nil {
		t.Fatal("forward to closed server must fail")
	}
}
