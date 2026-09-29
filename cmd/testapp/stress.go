package main

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// errBusy is returned when a stress run is already active.
var errBusy = errors.New("a stress run is already active")

// Stresser runs one bounded CPU stress at a time.
type Stresser interface {
	// Start begins a stress run of duration d in the background and returns
	// immediately. It returns errBusy if a run is already active.
	Start(d time.Duration) error
	// Active reports whether a stress run is in progress.
	Active() bool
}

// burner keeps every CPU busy for Duty of each Slot, so the instance sits
// near Duty×100% utilization (80-90%), never 100%
// (docs/spec/lifecycle-and-failures.md §5).
type burner struct {
	base    context.Context // cancelled on server shutdown
	duty    float64
	slot    time.Duration
	workers int

	active atomic.Bool
	wg     sync.WaitGroup
}

func newBurner(base context.Context, duty float64) *burner {
	return &burner{base: base, duty: duty, slot: 100 * time.Millisecond, workers: runtime.GOMAXPROCS(0)}
}

func (b *burner) Active() bool { return b.active.Load() }

func (b *burner) Start(d time.Duration) error {
	if !b.active.CompareAndSwap(false, true) {
		return errBusy
	}
	ctx, cancel := context.WithTimeout(b.base, d)
	var workers sync.WaitGroup
	for range b.workers {
		workers.Go(func() { b.work(ctx) })
	}
	b.wg.Go(func() {
		workers.Wait()
		cancel()
		b.active.Store(false)
	})
	return nil
}

// Wait blocks until the current stress run (if any) has finished.
func (b *burner) Wait() { b.wg.Wait() }

// work alternates a busy spin of duty×slot with an idle sleep for the rest
// of the slot until ctx is done.
func (b *burner) work(ctx context.Context) {
	busy := time.Duration(float64(b.slot) * b.duty)
	idle := b.slot - busy
	timer := time.NewTimer(idle)
	defer timer.Stop()
	for {
		start := time.Now()
		for time.Since(start) < busy {
			if ctx.Err() != nil {
				return
			}
		}
		timer.Reset(idle)
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
