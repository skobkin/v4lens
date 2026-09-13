package device

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"golang.org/x/sys/unix"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// ErrSessionClosed is returned when a Session is stopped.
var ErrSessionClosed = errors.New("device session closed")

// shortOp is one queued control-lane operation.
type shortOp struct {
	fn    func(Handle) error
	reply chan error
	ctx   context.Context
}

// mutReq requests exclusive mutation ownership. abandoned is closed when
// the requester gives up (ctx/closed) before being granted, so a stale
// request can never wedge the worker.
type mutReq struct {
	granted   chan *Mutation
	abandoned chan struct{}
}

// Session serializes all control-lane operations for one device on a
// single worker goroutine. Two operation classes exist:
//
//   - Short ops (Do): single reads/writes/queries, executed FIFO when no
//     mutation is active.
//   - Exclusive multi-step mutations (BeginMutation): profile applies and
//     Format-Apply run atomically with respect to short ops — queued
//     manual/coalesced writes wait and can never interleave as
//     "profile write A → manual write → profile write B".
//
// Mutation bodies run on the mutation owner's goroutine so it may sequence
// many steps without holding UI locks; the worker stays parked until
// Finish, which is what enforces exclusivity. Device locks are never held
// across UI waits: UI code calls Do from its own goroutines.
type Session struct {
	h      Handle
	log    *slog.Logger
	shortQ chan shortOp
	mutQ   chan mutReq
	done   chan struct{}

	closeOnce sync.Once
	closedMu  sync.Mutex
	closed    bool
}

// NewSession starts the session worker for handle h.
func NewSession(h Handle, log *slog.Logger) *Session {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	s := &Session{
		h:      h,
		log:    log,
		shortQ: make(chan shortOp, 64),
		mutQ:   make(chan mutReq, 1),
		done:   make(chan struct{}),
	}

	go s.loop()

	return s
}

// Handle exposes the underlying backend handle to the session owner
// (lifecycle management). Callers must not use it for concurrent device
// work — that is what Do and Mutation are for.
func (s *Session) Handle() Handle {
	return s.h
}

func (s *Session) loop() {
	for {
		select {
		case op := <-s.shortQ:
			s.exec(op)
		case req := <-s.mutQ:
			s.grant(req)
		case <-s.done:
			s.drain()

			return
		}
	}
}

// grant hands exclusivity to the mutation owner and parks until Finish.
// A request abandoned before grant is skipped; one abandoned after grant
// releases the worker immediately (the owner is gone either way).
func (s *Session) grant(req mutReq) {
	m := &Mutation{s: s, finished: make(chan error, 1)}

	select {
	case req.granted <- m:
		select {
		case err := <-m.finished:
			if err != nil {
				s.log.Warn("exclusive mutation finished with error", "device", s.h.Node(), "err", err)
			} else {
				s.log.Debug("exclusive mutation finished", "device", s.h.Node())
			}
		case <-req.abandoned:
			s.log.Debug("exclusive mutation abandoned after grant", "device", s.h.Node())
		case <-s.done:
		}
	case <-req.abandoned:
		// stale request: the waiter is gone, nothing to grant
	}
}

func (s *Session) exec(op shortOp) {
	defer func() {
		if r := recover(); r != nil {
			op.reply <- fmt.Errorf("device op panic: %v", r)
		}
	}()

	if err := op.ctx.Err(); err != nil {
		op.reply <- err

		return
	}

	op.reply <- classifyDeviceErr(op.fn(s.h))
}

// drain fails queued short ops after shutdown.
func (s *Session) drain() {
	for {
		select {
		case op := <-s.shortQ:
			op.reply <- ErrSessionClosed
		default:

			return
		}
	}
}

// Do queues a short op (single read/write/query). Ops run FIFO; while an
// exclusive mutation is active they wait for it to finish.
func (s *Session) Do(ctx context.Context, op func(Handle) error) error {
	if s.IsClosed() {
		return ErrSessionClosed
	}

	q := shortOp{fn: op, reply: make(chan error, 1), ctx: ctx}

	select {
	case s.shortQ <- q:
	case <-ctx.Done():

		return ctx.Err()
	case <-s.done:

		return ErrSessionClosed
	}

	select {
	case err := <-q.reply:

		return err
	case <-ctx.Done():

		return ctx.Err()
	}
}

// BeginMutation waits for exclusive mutation ownership. While the returned
// Mutation is open, queued short ops wait; the owner sequences steps with
// Mutation.Do and MUST call Finish (or Cancel).
func (s *Session) BeginMutation(ctx context.Context, name string) (*Mutation, error) {
	if s.IsClosed() {
		return nil, ErrSessionClosed
	}

	req := mutReq{granted: make(chan *Mutation, 1), abandoned: make(chan struct{})}

	select {
	case s.mutQ <- req:
	case <-ctx.Done():

		return nil, ctx.Err()
	case <-s.done:

		return nil, ErrSessionClosed
	}

	select {
	case m := <-req.granted:
		m.name = name

		s.log.Debug("exclusive mutation granted", "device", s.h.Node(), "name", name)

		return m, nil
	case <-ctx.Done():
		close(req.abandoned)

		return nil, ctx.Err()
	case <-s.done:
		close(req.abandoned)

		return nil, ErrSessionClosed
	}
}

// IsClosed reports whether Stop has been called.
func (s *Session) IsClosed() bool {
	s.closedMu.Lock()
	defer s.closedMu.Unlock()

	return s.closed
}

// Stop shuts the session down. Idempotent. A pending mutation short-circuits
// the worker only through its own Finish — Stop waits for the worker loop,
// which exits between operations.
func (s *Session) Stop() {
	s.closeOnce.Do(func() {
		s.closedMu.Lock()
		s.closed = true
		s.closedMu.Unlock()

		close(s.done)
	})
}

// Mutation is one exclusive multi-step mutation grant.
type Mutation struct {
	s          *Session
	name       string
	finished   chan error
	finishOnce sync.Once
}

// Do runs one step against the device handle. Steps run on the mutation
// owner's goroutine; no short op can interleave between them.
func (m *Mutation) Do(op func(Handle) error) error {
	defer func() {
		if r := recover(); r != nil {
			panic(fmt.Sprintf("mutation %q step panic: %v", m.name, r)) //nolint:revive // re-panic with context on the owner goroutine
		}
	}()

	return classifyDeviceErr(op(m.s.h))
}

// Finish closes the mutation (err is the overall outcome, for logs).
func (m *Mutation) Finish(err error) {
	m.finishOnce.Do(func() {
		m.finished <- err
	})
}

// Cancel closes the mutation without an error (nothing applied).
func (m *Mutation) Cancel() {
	m.Finish(nil)
}

// classifyDeviceErr wraps backend errors with the domain sentinels so the
// rest of the app never touches errno values.
func classifyDeviceErr(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, unix.ENODEV) || errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %w", domain.ErrDisconnected, err)
	}

	return err
}
