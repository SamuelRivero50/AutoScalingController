// Package clock implements the Clock port: the system clock for real mode
// and a controllable fake clock for the simulator.
package clock

import (
	"context"
	"sync"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

var (
	_ ports.Clock = System{}
	_ ports.Clock = (*Fake)(nil)
)

// System is the real wall clock.
type System struct{}

// Now returns the current UTC time.
func (System) Now() time.Time { return time.Now().UTC() }

// Sleep waits for d or until ctx is done.
func (System) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Fake is a manually advanced clock. Sleep advances it instantly, so a
// simulated hour of cycles runs in milliseconds. It is safe for concurrent
// use.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

// NewFake returns a fake clock starting at start (converted to UTC).
func NewFake(start time.Time) *Fake {
	return &Fake{now: start.UTC()}
}

// Now returns the fake current time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Advance moves the clock forward by d. Negative durations are ignored so
// time never goes backwards.
func (f *Fake) Advance(d time.Duration) {
	if d <= 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// Sleep advances the clock by d without blocking, unless ctx is already done.
func (f *Fake) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.Advance(d)
	return nil
}
