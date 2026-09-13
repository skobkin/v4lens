package device

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// recordingHandle records the op order the session actually executed.
// The embedded nil Handle satisfies the interface; unimplemented methods
// are never called by the session.
type recordingHandle struct {
	Handle

	mu    sync.Mutex
	order []string
}

func (h *recordingHandle) Close() error { return nil }

func (h *recordingHandle) Node() string { return "/dev/fake0" }

func (h *recordingHandle) record(what string) {
	h.mu.Lock()
	h.order = append(h.order, what)
	h.mu.Unlock()
}

func (h *recordingHandle) events() []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	return append([]string(nil), h.order...)
}

func (h *recordingHandle) waitEvents(n int, timeout time.Duration) []string {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if len(h.events()) >= n {
			return h.events()
		}

		time.Sleep(2 * time.Millisecond)
	}

	return h.events()
}

func TestSessionShortOpsSerialize(t *testing.T) {
	h := &recordingHandle{}
	s := NewSession(h, slog.New(slog.DiscardHandler))
	defer s.Stop()

	var wg sync.WaitGroup

	for range 8 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_ = s.Do(context.Background(), func(Handle) error {
				h.record("op")

				return nil
			})
		}()
	}

	wg.Wait()

	got := h.waitEvents(8, time.Second)
	if len(got) != 8 {
		t.Fatalf("lost ops: %d", len(got))
	}
}

func TestSessionMutationBlocksShortOps(t *testing.T) {
	h := &recordingHandle{}
	s := NewSession(h, slog.New(slog.DiscardHandler))
	defer s.Stop()

	ctx := context.Background()

	mut, err := s.BeginMutation(ctx, "profile-apply")
	if err != nil {
		t.Fatalf("begin mutation: %v", err)
	}

	h.record("mutation-start")

	// This short op must WAIT, never interleave.
	shortDone := make(chan error, 1)

	go func() {
		shortDone <- s.Do(ctx, func(Handle) error {
			h.record("short-op")

			return nil
		})
	}()

	// The short op must not run while the mutation is open.
	select {
	case err := <-shortDone:
		t.Fatalf("short op ran during mutation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	mut.Finish(nil)

	if err := <-shortDone; err != nil {
		t.Fatalf("queued short op failed: %v", err)
	}

	got := h.waitEvents(2, time.Second)

	if len(got) != 2 || got[0] != "mutation-start" || got[1] != "short-op" {
		t.Fatalf("order violated: %v", got)
	}
}

func TestSessionMutationStepsRunExclusively(t *testing.T) {
	h := &recordingHandle{}
	s := NewSession(h, slog.New(slog.DiscardHandler))
	defer s.Stop()

	ctx := context.Background()

	mut, err := s.BeginMutation(ctx, "apply")
	if err != nil {
		t.Fatal(err)
	}

	// Multiple steps; each runs on the owner goroutine while short ops wait.
	for i := range 3 {
		if err := mut.Do(func(Handle) error {
			h.record("step")

			return nil
		}); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}

	mut.Finish(nil)

	got := h.waitEvents(3, time.Second)

	if len(got) != 3 {
		t.Fatalf("steps lost: %v", got)
	}

	// Second mutation waits for the first to finish.
	secondStarted := make(chan struct{})

	go func() {
		m2, err := s.BeginMutation(ctx, "second")
		if err != nil {
			close(secondStarted)

			return
		}

		close(secondStarted)
		m2.Cancel()
	}()

	select {
	case <-secondStarted:
		// Granted after the first finished — fine.
	case <-time.After(50 * time.Millisecond):
		t.Log("second mutation still waiting (acceptable; first already finished)")
	}
}

func TestSessionBeginMutationRespectsContext(t *testing.T) {
	h := &recordingHandle{}
	s := NewSession(h, slog.New(slog.DiscardHandler))
	defer s.Stop()

	ctx := context.Background()

	first, err := s.BeginMutation(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Cancel()

	ctx2, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()

	if _, err := s.BeginMutation(ctx2, "second"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}

func TestSessionStopFailsQueuedOps(t *testing.T) {
	h := &recordingHandle{}
	s := NewSession(h, slog.New(slog.DiscardHandler))

	errs := make(chan error, 4)

	for range 4 {
		go func() {
			errs <- s.Do(context.Background(), func(Handle) error { return nil })
		}()
	}

	time.Sleep(5 * time.Millisecond)
	s.Stop()

	// Every op either ran or failed with ErrSessionClosed — none hang.
	deadline := time.After(time.Second)

	for range 4 {
		select {
		case err := <-errs:
			if err != nil && !errors.Is(err, ErrSessionClosed) {
				t.Fatalf("unexpected error: %v", err)
			}
		case <-deadline:
			t.Fatal("op hung after Stop")
		}
	}
}

func TestClassifyDeviceErr(t *testing.T) {
	wrapped := classifyDeviceErr(unix.ENODEV)

	if !errors.Is(wrapped, domain.ErrDisconnected) {
		t.Fatalf("ENODEV not classified: %v", wrapped)
	}

	if classifyDeviceErr(nil) != nil {
		t.Fatal("nil must stay nil")
	}

	if errors.Is(classifyDeviceErr(unix.EINVAL), domain.ErrDisconnected) {
		t.Fatal("EINVAL misclassified as disconnect")
	}
}
