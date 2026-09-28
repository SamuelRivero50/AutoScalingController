// Package simulator runs the closed-loop scenarios S1-S10 of
// docs/spec/simulator.md against the real decision core and control loop,
// with simulated adapters (fake clock, fake ASG, seeded metrics, in-memory
// state) and the real JSONL decision logger (ADR-0011).
package simulator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/clock"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/fakeasg"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/jsonllog"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/memstate"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/mockmetrics"
	"github.com/SamuelRivero50/AutoScalingController/internal/app"
	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

// Epoch is the simulated start time of every scenario, fixed so that runs
// are reproducible.
var Epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Scenario is one closed-loop experiment with an acceptance criterion.
type Scenario struct {
	ID        string
	Name      string
	Criterion string // acceptance criterion from docs/spec/simulator.md §2
	Duration  time.Duration
	Initial   int // initial desired and in-service instances
	Metrics   mockmetrics.Profile
	// Setup injects failures into the fake group before the run.
	Setup func(start time.Time, asg *fakeasg.ASG)
	// Check verifies the acceptance criterion.
	Check func(r Result) error
}

// Options control one run.
type Options struct {
	Seed   uint64
	LogDir string // where the JSONL decision log is written
}

// Result is everything a run produced.
type Result struct {
	Scenario Scenario
	Config   app.Config
	Start    time.Time
	Cycles   []ports.CycleRecord
	Events   []ports.EventRecord
}

// Elapsed returns the time between the scenario start and ts.
func (r Result) Elapsed(ts time.Time) time.Duration { return ts.Sub(r.Start) }

// EventsOf returns the events of one type, in order.
func (r Result) EventsOf(t ports.EventType) []ports.EventRecord {
	var out []ports.EventRecord
	for _, e := range r.Events {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

// Run executes the scenario with the realistic profile, one cycle per
// evaluation interval of simulated time.
func Run(ctx context.Context, sc Scenario, opts Options) (Result, error) {
	cfg := app.RealisticConfig()
	start := Epoch
	fake := clock.NewFake(start)
	asg := fakeasg.New(fake, fakeasg.Config{
		Min:                 cfg.Policy.MinInstances,
		Max:                 cfg.Policy.MaxInstances,
		InitialDesired:      sc.Initial,
		Warmup:              cfg.Warmup,
		DeregistrationDelay: cfg.DeregistrationDelay,
		LaunchRetryInterval: 30 * time.Second,
	})
	if sc.Setup != nil {
		sc.Setup(start, asg)
	}

	writer, err := jsonllog.New(opts.LogDir)
	if err != nil {
		return Result{}, err
	}
	rec := &recorder{}
	ctrl, err := app.New(cfg, core.ModeSim, fmt.Sprintf("sim-%s-seed%d", sc.ID, opts.Seed), app.Deps{
		Metrics:     mockmetrics.New(opts.Seed, start, sc.Metrics, asg),
		Provisioner: asg,
		State:       &memstate.Store{},
		Log:         tee{writer, rec},
		Clock:       fake,
	})
	if err != nil {
		return Result{}, errors.Join(err, writer.Close())
	}

	runErr := func() error {
		if err := ctrl.Start(ctx); err != nil {
			return err
		}
		cycles := int(sc.Duration / cfg.Policy.EvaluationInterval)
		for range cycles {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := ctrl.Cycle(ctx); err != nil {
				return err
			}
			fake.Advance(cfg.Policy.EvaluationInterval)
		}
		return ctrl.Stop(ctx)
	}()
	if err := errors.Join(runErr, writer.Close()); err != nil {
		return Result{}, fmt.Errorf("run %s: %w", sc.ID, err)
	}
	return Result{Scenario: sc, Config: cfg, Start: start, Cycles: rec.cycles, Events: rec.events}, nil
}

// recorder keeps the records in memory for the acceptance checks.
type recorder struct {
	mu     sync.Mutex
	cycles []ports.CycleRecord
	events []ports.EventRecord
}

func (r *recorder) LogCycle(_ context.Context, c ports.CycleRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cycles = append(r.cycles, c)
	return nil
}

func (r *recorder) LogEvent(_ context.Context, e ports.EventRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

// tee writes every record to all loggers.
type tee []ports.DecisionLogger

func (t tee) LogCycle(ctx context.Context, c ports.CycleRecord) error {
	var errs []error
	for _, l := range t {
		errs = append(errs, l.LogCycle(ctx, c))
	}
	return errors.Join(errs...)
}

func (t tee) LogEvent(ctx context.Context, e ports.EventRecord) error {
	var errs []error
	for _, l := range t {
		errs = append(errs, l.LogEvent(ctx, e))
	}
	return errors.Join(errs...)
}
