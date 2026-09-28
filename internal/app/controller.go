// Package app is the application layer: the imperative shell around the
// pure decision core. It runs the observe -> analyze -> decide -> act -> log
// loop over the five ports (docs/spec/architecture.md §4). It never reads
// the system clock directly; all time comes from the Clock port.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

// maxSeenActivities bounds the persisted set of activity IDs; older IDs
// also fall out of the activity lookback window.
const maxSeenActivities = 1000

// Deps are the adapters the controller runs on.
type Deps struct {
	Metrics     ports.MetricsSource
	Provisioner ports.InstanceProvisioner
	State       ports.StateStore
	Log         ports.DecisionLogger
	Clock       ports.Clock
	// Logger receives operational logs (not decision records). Optional.
	Logger *slog.Logger
}

// Controller is one controller process run. It is not safe for concurrent
// use: one goroutine drives Start, Cycle and Stop (or Run).
type Controller struct {
	cfg    Config
	run    ports.RunInfo
	deps   Deps
	logger *slog.Logger
	state  ports.State
}

// New validates the configuration and dependencies and returns a controller.
func New(cfg Config, mode core.Mode, runID string, deps Deps) (*Controller, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	if deps.Metrics == nil || deps.Provisioner == nil || deps.State == nil || deps.Log == nil || deps.Clock == nil {
		return nil, errors.New("app: every port dependency is required")
	}
	if runID == "" {
		return nil, errors.New("app: run id is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Controller{
		cfg:    cfg,
		run:    ports.RunInfo{RunID: runID, Mode: mode, Profile: cfg.Policy.Profile, ConfigHash: cfg.Hash()},
		deps:   deps,
		logger: logger,
	}, nil
}

// RunInfo returns the run metadata written in every record.
func (c *Controller) RunInfo() ports.RunInfo { return c.run }

// State returns a copy of the controller memory (for tests and tooling).
func (c *Controller) State() ports.State {
	st := c.state
	st.Memory.Entries = slices.Clone(st.Memory.Entries)
	st.SeenActivities = slices.Clone(st.SeenActivities)
	st.LastInstances = slices.Clone(st.LastInstances)
	return st
}

// Run starts the controller, runs a cycle every evaluation interval until
// ctx is done, then stops. Cycle errors are reported through the logger and
// do not stop the loop.
func (c *Controller) Run(ctx context.Context) error {
	if err := c.Start(ctx); err != nil {
		return err
	}
	interval := c.cfg.Policy.EvaluationInterval
	for ctx.Err() == nil {
		began := c.deps.Clock.Now()
		if err := c.Cycle(ctx); err != nil {
			c.logger.ErrorContext(ctx, "cycle failed", "err", err)
		}
		wait := interval - c.deps.Clock.Now().Sub(began)
		if err := c.deps.Clock.Sleep(ctx, max(0, wait)); err != nil {
			break
		}
	}
	return c.Stop(context.WithoutCancel(ctx))
}

// Start restores the persisted memory or rebuilds it from the provisioner,
// and logs CONTROLLER_STARTED.
func (c *Controller) Start(ctx context.Context) error {
	now := c.deps.Clock.Now()
	st, err := c.deps.State.Load(ctx)
	if err == nil {
		c.state = st
	} else {
		reason := "unreadable"
		switch {
		case errors.Is(err, ports.ErrStateNotFound):
			reason = "not_found"
		case errors.Is(err, ports.ErrStateCorrupt):
			reason = "corrupt"
		}
		if logErr := c.rebuild(ctx, now, reason, err); logErr != nil {
			return logErr
		}
	}
	if c.state.NextCycle < 1 {
		c.state.NextCycle = 1
	}
	c.state.RunID = c.run.RunID
	return c.logEvent(ctx, now, ports.EventControllerStarted, map[string]any{
		"mode":       string(c.run.Mode),
		"profile":    string(c.run.Profile),
		"restored":   err == nil,
		"next_cycle": c.state.NextCycle,
	})
}

// rebuild starts from empty memory and reads the instance view from the
// provisioner (ADR-0009), logging STATE_REBUILT.
func (c *Controller) rebuild(ctx context.Context, now time.Time, reason string, cause error) error {
	c.state = ports.State{NextCycle: 1, ActivityWatermark: now}
	details := map[string]any{"reason": reason, "error": cause.Error()}

	cctx, cancel := context.WithTimeout(ctx, c.cfg.CallTimeout)
	snap, err := c.deps.Provisioner.DescribeCapacity(cctx)
	cancel()
	if err != nil {
		details["capacity_error"] = err.Error()
	} else {
		c.state.LastInstances = snap.Instances
		details["instances"] = len(snap.Instances)
	}
	return c.logEvent(ctx, now, ports.EventStateRebuilt, details)
}

// Stop logs CONTROLLER_STOPPED. It keeps working after ctx is cancelled.
func (c *Controller) Stop(ctx context.Context) error {
	ctx = context.WithoutCancel(ctx)
	return c.logEvent(ctx, c.deps.Clock.Now(), ports.EventControllerStopped, map[string]any{
		"last_cycle": c.state.NextCycle - 1,
	})
}

func (c *Controller) logEvent(ctx context.Context, ts time.Time, t ports.EventType, details map[string]any) error {
	if err := c.deps.Log.LogEvent(ctx, ports.EventRecord{Run: c.run, TS: ts, Type: t, Details: details}); err != nil {
		return fmt.Errorf("log %s event: %w", t, err)
	}
	return nil
}
