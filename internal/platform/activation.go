package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// The activation channel lets a second instance hand control back to the
// running one: the first instance serves a token-protected POST /activate
// on 127.0.0.1 and records the address+token in the V4Lens runtime
// directory; the second instance reads that state and forwards.
const (
	activationStateFilename = "activation.json"
	activationTimeout       = 2 * time.Second
)

// ActivationServer serves the running instance's activation endpoint.
type ActivationServer struct {
	server    *http.Server
	listener  net.Listener
	statePath string
	closeOnce sync.Once
}

type activationState struct {
	Address string `json:"address"`
	Token   string `json:"token"`
}

// StartActivationServer begins serving POST /activate on a loopback
// ephemeral port. Every accepted request runs handler — publish it onto
// the app event bus so the UI reacts on its own goroutine (the handler is
// called from the HTTP server goroutine, never the UI thread).
func StartActivationServer(dirs Dirs, log *slog.Logger, handler func()) (*ActivationServer, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	dir := dirs.AppRuntimeDir()

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create activation state dir: %w", err)
	}

	statePath := filepath.Join(dir, activationStateFilename)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for activations: %w", err)
	}

	token, err := randomToken()
	if err != nil {
		_ = listener.Close()

		return nil, err
	}

	state := activationState{Address: listener.Addr().String(), Token: token}

	if err := writeActivationState(statePath, state); err != nil {
		_ = listener.Close()

		return nil, err
	}

	s := &ActivationServer{listener: listener, statePath: statePath}

	s.server = &http.Server{
		ReadHeaderTimeout: activationTimeout,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/activate" {
				http.NotFound(w, r)

				return
			}

			if r.Header.Get("X-V4Lens-Token") != state.Token {
				http.Error(w, "forbidden", http.StatusForbidden)

				return
			}

			// Bound the body even though the activation payload is empty.
			_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<16))
			_ = r.Body.Close()

			log.Info("activation request accepted")

			if handler != nil {
				handler()
			}

			w.WriteHeader(http.StatusNoContent)
		}),
	}

	go func() {
		if err := s.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warn("activation server stopped", "err", err)
		}
	}()

	log.Info("activation server started", "address", state.Address)

	return s, nil
}

// Close shuts the server down and removes the state file.
func (s *ActivationServer) Close() error {
	if s == nil {
		return nil
	}

	var closeErr error

	s.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), activationTimeout)
		defer cancel()

		if err := s.server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			closeErr = fmt.Errorf("shutdown activation server: %w", err)
		}

		if err := os.Remove(s.statePath); err != nil && !os.IsNotExist(err) {
			closeErr = errors.Join(closeErr, fmt.Errorf("remove activation state: %w", err))
		}
	})

	return closeErr
}

// ForwardActivation sends an activation to the running instance. A stale
// state file (crashed predecessor) surfaces as an error; the caller logs
// it — the flock remains the authority on whether an instance is alive.
func ForwardActivation(dirs Dirs) error {
	state, err := readActivationState(filepath.Join(dirs.AppRuntimeDir(), activationStateFilename))
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: activationTimeout}

	req, err := http.NewRequest(http.MethodPost, "http://"+state.Address+"/activate", http.NoBody)
	if err != nil {
		return fmt.Errorf("create activation request: %w", err)
	}

	req.Header.Set("X-V4Lens-Token", state.Token)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send activation request: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

		return fmt.Errorf("activation request failed: status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func writeActivationState(path string, state activationState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode activation state: %w", err)
	}

	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write activation state: %w", err)
	}

	return nil
}

func readActivationState(path string) (activationState, error) {
	// G304: the path is built from the app-owned runtime directory.
	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return activationState{}, fmt.Errorf("read activation state: %w", err)
	}

	var state activationState

	if err := json.Unmarshal(data, &state); err != nil {
		return activationState{}, fmt.Errorf("decode activation state: %w", err)
	}

	if state.Address == "" || state.Token == "" {
		return activationState{}, errors.New("activation state is incomplete")
	}

	return state, nil
}

func randomToken() (string, error) {
	buf := make([]byte, 32)

	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate activation token: %w", err)
	}

	return hex.EncodeToString(buf), nil
}
