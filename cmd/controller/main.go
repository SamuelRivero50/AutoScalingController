// Command controller runs the auto-scaling control loop
// (docs/spec/architecture.md). It is the composition root: it loads the
// configuration file, builds the adapters for the configured mode and wires
// them into the application layer by constructor injection (ADR-0017).
//
// Usage:
//
//	controller -config /etc/asc/controller.json
//
// Modes:
//
//   - real: CloudWatch metrics and the EC2 Auto Scaling group (the
//     deployment on the controller instance).
//   - sim: simulated metrics and group on the wall clock, for local smoke
//     runs without AWS. Closed-loop experiments use cmd/simulator instead.
//
// The controller stops cleanly on SIGINT or SIGTERM. It exits with status 1
// on a startup or run error and 2 on a usage error.
package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/asg"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/awsclient"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/clock"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/cloudwatch"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/fakeasg"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/filestate"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/jsonllog"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/mockmetrics"
	"github.com/SamuelRivero50/AutoScalingController/internal/app"
	"github.com/SamuelRivero50/AutoScalingController/internal/config"
	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

// metricsPollInterval is the wait between startup metric checks.
const metricsPollInterval = 30 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("controller", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", "", "path to the controller configuration file (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *path == "" || fs.NArg() > 0 {
		fmt.Fprintln(stderr, "usage: controller -config FILE")
		return 2
	}

	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	if err := start(ctx, *path, logger); err != nil {
		logger.Error("controller failed", "err", err)
		return 1
	}
	return 0
}

// start loads the configuration, wires the adapters and runs the loop
// until ctx is done.
func start(ctx context.Context, path string, logger *slog.Logger) error {
	cfg, err := config.LoadFile(path)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	sys := clock.System{}
	runID := cfg.RunID
	if runID == "" {
		runID = newRunID(sys.Now())
	}

	var deps app.Deps
	switch cfg.Mode {
	case core.ModeReal:
		d, src, err := realDeps(ctx, cfg)
		if err != nil {
			return err
		}
		logger.InfoContext(ctx, "waiting for target group metrics", "timeout", cfg.MetricsWaitTimeout.String())
		if err := waitForMetrics(ctx, src, sys, cfg.MetricsWaitTimeout, metricsPollInterval, logger); err != nil {
			return err
		}
		deps = d
	default:
		deps = simDeps(cfg, sys)
	}

	w, err := jsonllog.New(cfg.Paths.LogDir)
	if err != nil {
		return fmt.Errorf("open decision log: %w", err)
	}
	defer func() {
		if cerr := w.Close(); cerr != nil {
			logger.Warn("close decision log", "err", cerr)
		}
	}()
	deps.State = filestate.New(cfg.Paths.StateFile)
	deps.Log = w
	deps.Clock = sys
	deps.Logger = logger

	ctrl, err := app.New(cfg.App, cfg.Mode, runID, deps)
	if err != nil {
		return fmt.Errorf("build controller: %w", err)
	}
	logger.InfoContext(ctx, "controller starting",
		"run_id", runID, "mode", string(cfg.Mode), "profile", string(cfg.App.Policy.Profile), "config_hash", cfg.App.Hash())
	if err := ctrl.Run(ctx); err != nil {
		return fmt.Errorf("run controller: %w", err)
	}
	logger.Info("controller stopped", "run_id", runID)
	return nil
}

// realDeps builds the AWS adapters. It does not call AWS.
func realDeps(ctx context.Context, cfg config.Resolved) (app.Deps, *cloudwatch.Source, error) {
	awsCfg, err := awsclient.Load(ctx, awsclient.Settings{
		Region:         cfg.AWS.Region,
		AttemptTimeout: cfg.App.AttemptTimeout,
		MaxRetries:     cfg.App.MaxRetries,
		MaxBackoff:     cfg.App.RetryMaxBackoff,
	})
	if err != nil {
		return app.Deps{}, nil, err
	}
	src, err := cloudwatch.NewFromConfig(awsCfg, cloudwatch.Config{
		LoadBalancer: cfg.AWS.LoadBalancerDimension,
		TargetGroup:  cfg.AWS.TargetGroupDimension,
	})
	if err != nil {
		return app.Deps{}, nil, err
	}
	prov, err := asg.NewFromConfig(awsCfg, asg.Config{
		GroupName:      cfg.AWS.GroupName,
		TargetGroupARN: cfg.AWS.TargetGroupARN,
		Now:            clock.System{}.Now,
	})
	if err != nil {
		return app.Deps{}, nil, err
	}
	return app.Deps{Metrics: src, Provisioner: prov}, src, nil
}

// simDeps builds the simulated adapters on the wall clock.
func simDeps(cfg config.Resolved, c ports.Clock) app.Deps {
	group := fakeasg.New(c, fakeasg.Config{
		Min: cfg.App.Policy.MinInstances, Max: cfg.App.Policy.MaxInstances, InitialDesired: cfg.Sim.InitialDesired,
		Warmup: cfg.App.Warmup, DeregistrationDelay: cfg.App.DeregistrationDelay, LaunchRetryInterval: 30 * time.Second,
	})
	load := cfg.Sim.Load
	metrics := mockmetrics.New(cfg.Sim.Seed, c.Now(), mockmetrics.Profile{Load: func(time.Duration) float64 { return load }}, group)
	return app.Deps{Metrics: metrics, Provisioner: group}
}

// validator checks that the expected metrics exist.
type validator interface {
	Validate(ctx context.Context) error
}

// waitForMetrics polls v until the expected metrics exist, ctx is done or
// timeout elapses. A new deployment publishes the target-group metrics a
// few minutes after the first health checks, so a miss is retried.
func waitForMetrics(ctx context.Context, v validator, c ports.Clock, timeout, interval time.Duration, logger *slog.Logger) error {
	deadline := c.Now().Add(timeout)
	for attempt := 1; ; attempt++ {
		err := v.Validate(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("wait for metrics: %w", ctx.Err())
		}
		if !c.Now().Add(interval).Before(deadline) {
			return fmt.Errorf("expected metrics not available after %s: %w", timeout, err)
		}
		logger.WarnContext(ctx, "expected metrics not available yet", "attempt", attempt, "err", err)
		if err := c.Sleep(ctx, interval); err != nil {
			return fmt.Errorf("wait for metrics: %w", err)
		}
	}
}

// newRunID returns "run-<UTC timestamp>-<random suffix>".
func newRunID(now time.Time) string {
	return "run-" + now.UTC().Format("20060102T150405Z") + "-" + strings.ToLower(rand.Text()[:8])
}
