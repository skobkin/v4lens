package viewmodel

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// fakeClock advances manually; After fires only when the test advances.
type fakeClock struct {
	now    time.Time
	timers []chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(0, 0)}
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.timers = append(c.timers, ch)

	return ch
}

func (c *fakeClock) fireAll() {
	for _, ch := range c.timers {
		c.now = c.now.Add(time.Millisecond)
		ch <- c.now
	}

	c.timers = nil
}

func TestCoalescerNewestWinsSingleWrite(t *testing.T) {
	clock := newFakeClock()

	writes := make(chan domain.ControlValue, 16)

	c := NewCoalescer(
		func(_ context.Context, id uint32, v domain.ControlValue) (domain.WriteResult, error) {
			writes <- v

			return domain.WriteResult{Accepted: true, Effective: &v}, nil
		},
		nil,
		200*time.Millisecond,
		clock,
	)
	defer c.Stop()

	c.Offer(1, domain.NewIntValue(1, domain.TypeInteger, 10))
	c.Offer(1, domain.NewIntValue(1, domain.TypeInteger, 11))
	c.Offer(1, domain.NewIntValue(1, domain.TypeInteger, 12)) // supersedes all

	if !c.Pending(1) {
		t.Fatal("value should be pending")
	}

	clock.fireAll()

	select {
	case v := <-writes:
		if v.Int != 12 {
			t.Fatalf("only the newest value must be written, got %d", v.Int)
		}
	case <-time.After(time.Second):
		t.Fatal("expected exactly one write after the period")
	}

	select {
	case v := <-writes:
		t.Fatalf("superseded values leaked: %+v", v)
	default:
	}
}

func TestCoalescerCommitBypassesWait(t *testing.T) {
	clock := newFakeClock()

	writes := make(chan domain.ControlValue, 16)

	c := NewCoalescer(
		func(_ context.Context, id uint32, v domain.ControlValue) (domain.WriteResult, error) {
			writes <- v

			return domain.WriteResult{Accepted: true}, nil
		},
		nil,
		time.Hour, // period never elapses in this test
		clock,
	)
	defer c.Stop()

	c.Offer(1, domain.NewIntValue(1, domain.TypeInteger, 5))
	c.Commit(context.Background(), 1, domain.NewIntValue(1, domain.TypeInteger, 9))

	select {
	case v := <-writes:
		if v.Int != 9 {
			t.Fatalf("commit wrote %d, want 9", v.Int)
		}
	default:
		t.Fatal("commit must write immediately")
	}

	if c.Pending(1) {
		t.Fatal("pending coalesced write must be cancelled by Commit")
	}
}

func TestCoalescerResetInvalidatesPending(t *testing.T) {
	clock := newFakeClock()

	writes := make(chan domain.ControlValue, 16)

	c := NewCoalescer(
		func(_ context.Context, id uint32, v domain.ControlValue) (domain.WriteResult, error) {
			writes <- v

			return domain.WriteResult{Accepted: true}, nil
		},
		nil,
		50*time.Millisecond,
		clock,
	)
	defer c.Stop()

	c.Offer(1, domain.NewIntValue(1, domain.TypeInteger, 7))
	c.Reset()

	if c.Pending(1) {
		t.Fatal("Reset must drop pending writes")
	}

	// Fire whatever timer state remains: nothing may reach the device.
	clock.fireAll()
	time.Sleep(20 * time.Millisecond)

	select {
	case v := <-writes:
		t.Fatalf("stale write fired after Reset: %+v", v)
	default:
	}
}

func TestCoalescerFlushDrainsPending(t *testing.T) {
	clock := newFakeClock()

	var writes atomic.Int32

	c := NewCoalescer(
		func(_ context.Context, _ uint32, _ domain.ControlValue) (domain.WriteResult, error) {
			writes.Add(1)

			return domain.WriteResult{Accepted: true}, nil
		},
		nil,
		200*time.Millisecond,
		clock,
	)
	defer c.Stop()

	c.Offer(1, domain.NewIntValue(1, domain.TypeInteger, 1))
	c.Offer(2, domain.NewIntValue(2, domain.TypeInteger, 2))

	// The writes run on fire goroutines; fire the timers and flush.
	clock.fireAll()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := c.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	if n := writes.Load(); n != 2 {
		t.Fatalf("flush saw %d writes, want 2", n)
	}
}

func TestCoalescerWriteErrorsReachResultHook(t *testing.T) {
	clock := newFakeClock()

	results := make(chan error, 4)

	c := NewCoalescer(
		func(_ context.Context, id uint32, _ domain.ControlValue) (domain.WriteResult, error) {
			return domain.WriteResult{}, errors.New("boom")
		},
		func(id uint32, _ domain.WriteResult, err error) {
			results <- err
		},
		10*time.Millisecond,
		clock,
	)
	defer c.Stop()

	c.Offer(1, domain.NewIntValue(1, domain.TypeInteger, 1))
	clock.fireAll()

	select {
	case err := <-results:
		if err == nil || err.Error() != "boom" {
			t.Fatalf("wrong error surfaced: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("result hook never called")
	}
}

func TestCoalescerReofferAfterCommitStillWrites(t *testing.T) {
	clock := newFakeClock()

	writes := make(chan domain.ControlValue, 16)

	c := NewCoalescer(
		func(_ context.Context, id uint32, v domain.ControlValue) (domain.WriteResult, error) {
			writes <- v

			return domain.WriteResult{Accepted: true}, nil
		},
		nil,
		10*time.Millisecond,
		clock,
	)
	defer c.Stop()

	// Commit cancels the first schedule; a later Offer must still fire.
	c.Offer(1, domain.NewIntValue(1, domain.TypeInteger, 1))
	c.Commit(context.Background(), 1, domain.NewIntValue(1, domain.TypeInteger, 2))
	c.Offer(1, domain.NewIntValue(1, domain.TypeInteger, 3))

	clock.fireAll()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := c.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	var got []int64

	for {
		select {
		case v := <-writes:
			got = append(got, v.Int)
		default:

			goto done
		}
	}

done:
	if len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("writes = %v, want [2 3]", got)
	}
}
