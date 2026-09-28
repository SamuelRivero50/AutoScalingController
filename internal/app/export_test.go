package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/clock"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/fakeasg"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/memstate"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/mockmetrics"
	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

var t0 = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

// recorder is an in-memory DecisionLogger.
type recorder struct {
	mu      sync.Mutex
	cycles  []ports.CycleRecord
	events  []ports.EventRecord
	fail    error
	onCycle func(n int)
}

func (r *recorder) LogCycle(_ context.Context, rec ports.CycleRecord) error {
	r.mu.Lock()
	if r.fail != nil {
		r.mu.Unlock()
		return r.fail
	}
	r.cycles = append(r.cycles, rec)
	n, hook := len(r.cycles), r.onCycle
	r.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	return nil
}

func (r *recorder) LogEvent(_ context.Context, rec ports.EventRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	r.events = append(r.events, rec)
	return nil
}

func (r *recorder) eventsOf(t ports.EventType) []ports.EventRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []ports.EventRecord
	for _, e := range r.events {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

func (r *recorder) last() ports.CycleRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cycles[len(r.cycles)-1]
}

// rig is a controller wired to simulated adapters.
type rig struct {
	ctrl  *Controller
	clock *clock.Fake
	asg   *fakeasg.ASG
	store *memstate.Store
	log   *recorder
	cfg   Config
}

type rigOption func(*rigSetup)

type rigSetup struct {
	cfg     Config
	desired int
	load    float64
	metrics ports.MetricsSource
}

func withLoad(l float64) rigOption  { return func(s *rigSetup) { s.load = l } }
func withDesired(n int) rigOption   { return func(s *rigSetup) { s.desired = n } }
func withConfig(c Config) rigOption { return func(s *rigSetup) { s.cfg = c } }
func withMetrics(m ports.MetricsSource) rigOption {
	return func(s *rigSetup) { s.metrics = m }
}

func newRig(t *testing.T, opts ...rigOption) *rig {
	t.Helper()
	s := rigSetup{cfg: RealisticConfig(), desired: 2, load: 0.9}
	for _, o := range opts {
		o(&s)
	}
	c := clock.NewFake(t0)
	asg := fakeasg.New(c, fakeasg.Config{
		Min: s.cfg.Policy.MinInstances, Max: s.cfg.Policy.MaxInstances, InitialDesired: s.desired,
		Warmup: s.cfg.Warmup, DeregistrationDelay: s.cfg.DeregistrationDelay, LaunchRetryInterval: 30 * time.Second,
	})
	metrics := s.metrics
	if metrics == nil {
		load := s.load
		metrics = mockmetrics.New(1, t0, mockmetrics.Profile{Load: func(time.Duration) float64 { return load }}, asg)
	}
	r := &rig{clock: c, asg: asg, store: &memstate.Store{}, log: &recorder{}, cfg: s.cfg}
	ctrl, err := New(s.cfg, core.ModeSim, "run-test", Deps{
		Metrics: metrics, Provisioner: asg, State: r.store, Log: r.log, Clock: c,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.ctrl = ctrl
	return r
}

func (r *rig) start(t *testing.T) {
	t.Helper()
	if err := r.ctrl.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// cycles runs n cycles, advancing the clock one interval after each.
func (r *rig) cycles(t *testing.T, n int) {
	t.Helper()
	for range n {
		if err := r.ctrl.Cycle(t.Context()); err != nil {
			t.Fatal(err)
		}
		r.clock.Advance(r.cfg.Policy.EvaluationInterval)
	}
}

// blockingMetrics blocks every call until its context is done.
type blockingMetrics struct{}

func (blockingMetrics) InstanceCPU(ctx context.Context, _ ports.Period, _ []string) ([]core.InstanceCPU, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockingMetrics) LoadBalancer(ctx context.Context, _ ports.Period) (ports.LoadBalancerReadings, error) {
	<-ctx.Done()
	return ports.LoadBalancerReadings{}, ctx.Err()
}

var errLogDown = errors.New("log volume unavailable")
