package viewmodel

import (
	"context"
	"sync"
	"time"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// WriteFunc performs one device write (routed through the device Session).
type WriteFunc func(ctx context.Context, id uint32, v domain.ControlValue) (domain.WriteResult, error)

// ResultFunc receives the outcome of every issued write, including writes
// whose result arrived after the value was superseded. The UI refreshes the
// renderer only from WriteResult.Effective.
type ResultFunc func(id uint32, wr domain.WriteResult, err error)

// DefaultCoalescePeriod is the slider-write throttle (~1 write / 200 ms).
const DefaultCoalescePeriod = 200 * time.Millisecond

// Coalescer implements newest-value coalescing for live control writes:
// at most one write per control per period while movement continues,
// superseded intermediate values discarded, OnChangeEnded-style commits
// bypass the wait, and an exclusive mutation start invalidates pending
// writes (Reset). Never blocks the UI thread.
type Coalescer struct {
	write   WriteFunc
	onWrite ResultFunc
	period  time.Duration
	clock   Clock

	mu      sync.Mutex
	pending map[uint32]domain.ControlValue
	timers  map[uint32]*fire
	gen     uint64 // bumped by Reset to invalidate scheduled fires
	stopped bool

	changed chan struct{} // signaled whenever pending becomes empty
	stopCh  chan struct{}
	stopOne sync.Once
	wg      sync.WaitGroup
}

type fire struct {
	timer <-chan time.Time
	gen   uint64
}

// NewCoalescer builds a coalescer writing through write.
func NewCoalescer(write WriteFunc, onWrite ResultFunc, period time.Duration, clock Clock) *Coalescer {
	if period <= 0 {
		period = DefaultCoalescePeriod
	}

	if clock == nil {
		clock = RealClock{}
	}

	c := &Coalescer{
		write:   write,
		onWrite: onWrite,
		period:  period,
		clock:   clock,
		pending: map[uint32]domain.ControlValue{},
		timers:  map[uint32]*fire{},
		changed: make(chan struct{}, 1),
		stopCh:  make(chan struct{}),
	}

	return c
}

// Offer records the newest value for id and schedules a write after the
// coalescing period if none is pending. Intermediate values overwrite the
// pending one without rescheduling.
func (c *Coalescer) Offer(id uint32, v domain.ControlValue) {
	c.mu.Lock()

	if c.stopped {
		c.mu.Unlock()

		return
	}

	c.pending[id] = v

	if _, scheduled := c.timers[id]; scheduled {
		c.mu.Unlock()

		return
	}

	gen := c.gen
	c.timers[id] = &fire{timer: c.clock.After(c.period), gen: gen}
	c.mu.Unlock()

	c.wg.Add(1)

	go c.awaitFire(id, gen)
}

func (c *Coalescer) awaitFire(id uint32, gen uint64) {
	defer c.wg.Done()

	c.mu.Lock()
	f := c.timers[id]
	c.mu.Unlock()

	if f == nil {
		return
	}

	select {
	case <-f.timer:
	case <-c.stopCh:

		return
	}

	c.mu.Lock()

	// Only the currently scheduled fire may consume the pending value: a
	// Commit or a newer Offer replaced timers[id], and Reset bumped gen.
	if cur := c.timers[id]; cur != f || gen != c.gen {
		c.mu.Unlock()

		return
	}

	v, ok := c.pending[id]
	delete(c.pending, id)
	delete(c.timers, id)
	c.mu.Unlock()

	if !ok {
		c.signalIdle()

		return
	}

	c.emit(context.Background(), id, v)
}

func (c *Coalescer) emit(ctx context.Context, id uint32, v domain.ControlValue) {
	wr, err := c.write(ctx, id, v)

	if c.onWrite != nil {
		c.onWrite(id, wr, err)
	}

	c.signalIdle()
}

func (c *Coalescer) signalIdle() {
	select {
	case c.changed <- struct{}{}:
	default:
	}
}

// Commit writes the final value immediately, cancelling any pending
// coalesced write for the same control.
func (c *Coalescer) Commit(ctx context.Context, id uint32, v domain.ControlValue) {
	c.mu.Lock()

	if c.stopped {
		c.mu.Unlock()

		return
	}

	delete(c.pending, id)
	delete(c.timers, id)
	c.mu.Unlock()

	c.emit(ctx, id, v)
}

// Pending reports whether a coalesced write for id is waiting.
func (c *Coalescer) Pending(id uint32) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, ok := c.pending[id]

	return ok
}

// Flush waits until no pending coalesced writes remain (or ctx is done).
func (c *Coalescer) Flush(ctx context.Context) error {
	for {
		c.mu.Lock()
		empty := len(c.pending) == 0
		c.mu.Unlock()

		if empty {
			return nil
		}

		select {
		case <-c.changed:
		case <-ctx.Done():

			return ctx.Err()
		case <-c.stopCh:

			return nil
		}
	}
}

// Reset invalidates ALL pending coalesced writes: an exclusive mutation
// (profile apply / format apply) has started, so the device state is about
// to change under us and stale intents must never fire afterwards.
func (c *Coalescer) Reset() {
	c.mu.Lock()

	c.gen++
	c.pending = map[uint32]domain.ControlValue{}
	c.timers = map[uint32]*fire{}
	c.mu.Unlock()

	c.signalIdle()
}

// Stop halts the coalescer. Scheduled fires are dropped, not flushed.
func (c *Coalescer) Stop() {
	c.stopOne.Do(func() {
		c.mu.Lock()
		c.stopped = true
		c.mu.Unlock()

		close(c.stopCh)
	})

	c.wg.Wait()
}
