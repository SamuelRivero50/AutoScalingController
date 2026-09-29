package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/fakeasg"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/jsonllog"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/memstate"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/mockmetrics"
	"github.com/SamuelRivero50/AutoScalingController/internal/app"
	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

// Live demo (docs/spec/simulator.md §4): the real control loop with the
// demo profile, the fake group and the mock metrics, where the presenter
// sets the load interactively while the decisions print in near real time.

const (
	maxDialLoad  = 10.0 // instance units
	dialStep     = 0.5
	demoNoiseStd = 3.0 // CPU percentage points
)

// loadPresets are named loads in instance units (1.0 saturates one instance).
var loadPresets = map[string]float64{
	"idle":        0,
	"comfortable": 0.8,
	"elevated":    1.6,
	"overload":    3,
	"peak":        4.5,
}

const demoHelp = `Load is in instance units: 1.0 saturates one instance.
Commands:
  <number>      set the load, e.g. 2.5 (0 to 10)
  + / -         raise or lower the load by 0.5
  idle | comfortable | elevated | overload | peak
  status        print the current load
  help          print this help
  quit          stop the demo (Ctrl+C also works)
`

// dial is the load the presenter controls. It is read by the metrics
// generator on the controller goroutine and written by the input reader.
type dial struct{ bits atomic.Uint64 }

func (d *dial) Get() float64  { return math.Float64frombits(d.bits.Load()) }
func (d *dial) Set(v float64) { d.bits.Store(math.Float64bits(clampLoad(v))) }

func clampLoad(v float64) float64 { return min(maxDialLoad, max(0, v)) }

// command is one parsed presenter input.
type command struct {
	quit, help, status bool
	set                bool
	load               float64
}

// parseCommand interprets one input line against the current load.
func parseCommand(line string, current float64) (command, error) {
	word := strings.ToLower(strings.TrimSpace(line))
	switch word {
	case "":
		return command{status: true}, nil
	case "q", "quit", "exit":
		return command{quit: true}, nil
	case "h", "help", "?":
		return command{help: true}, nil
	case "s", "status":
		return command{status: true}, nil
	case "+":
		return command{set: true, load: clampLoad(current + dialStep)}, nil
	case "-":
		return command{set: true, load: clampLoad(current - dialStep)}, nil
	}
	if v, ok := loadPresets[word]; ok {
		return command{set: true, load: v}, nil
	}
	v, err := strconv.ParseFloat(word, 64)
	if err != nil || math.IsNaN(v) || v < 0 || v > maxDialLoad {
		return command{}, fmt.Errorf("unknown command %q (type help)", line)
	}
	return command{set: true, load: v}, nil
}

// readCommands applies presenter input to d until quit or the end of in.
// It calls quit on a quit command; the end of input only stops reading.
func readCommands(in io.Reader, d *dial, out io.Writer, quit func()) {
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		cmd, err := parseCommand(sc.Text(), d.Get())
		switch {
		case err != nil:
			fmt.Fprintln(out, err)
		case cmd.quit:
			quit()
			return
		case cmd.help:
			fmt.Fprint(out, demoHelp)
		case cmd.set:
			d.Set(cmd.load)
			fmt.Fprintf(out, "load set to %.2f\n", d.Get())
		case cmd.status:
			fmt.Fprintf(out, "load is %.2f\n", d.Get())
		}
	}
}

// syncWriter serializes writes from the controller and input goroutines.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// printer is a DecisionLogger that prints one line per cycle.
type printer struct {
	out  io.Writer
	dial *dial
}

func (p printer) LogCycle(_ context.Context, r ports.CycleRecord) error {
	cpu := "  n/a"
	if r.Signals.CPU.Valid() && r.Signals.CPU.HasValue {
		cpu = fmt.Sprintf("%4.1f%%", r.Signals.CPU.Value)
	}
	action := string(r.Action.Type)
	if r.Action.TargetDesired != nil {
		action = fmt.Sprintf("%s=%d", action, *r.Action.TargetDesired)
	}
	if r.Action.Status != ports.ActionOK {
		action += " " + string(r.Action.Status)
		if r.Action.SkipReason != "" {
			action += " (" + r.Action.SkipReason + ")"
		}
	}
	_, err := fmt.Fprintf(p.out, "%s #%-3d load %4.2f  cpu %s  in-service %d pending %d draining %d  %-17s %-27s %s\n",
		r.TS.Format(time.TimeOnly), r.CycleID, p.dial.Get(), cpu,
		r.Capacity.InService(), r.Capacity.Pending(), r.Capacity.Draining(),
		r.Decision.Decision, r.Decision.Reason, action)
	return err
}

func (p printer) LogEvent(_ context.Context, e ports.EventRecord) error {
	switch e.Type {
	case ports.EventBreakerStateChange, ports.EventBlindAlert, ports.EventNoHealthyTargetsAlert:
		_, err := fmt.Fprintf(p.out, "%s event %s %v\n", e.TS.Format(time.TimeOnly), e.Type, e.Details)
		return err
	}
	return nil
}

// fanout writes every record to all loggers.
type fanout []ports.DecisionLogger

func (f fanout) LogCycle(ctx context.Context, c ports.CycleRecord) error {
	var errs []error
	for _, l := range f {
		errs = append(errs, l.LogCycle(ctx, c))
	}
	return errors.Join(errs...)
}

func (f fanout) LogEvent(ctx context.Context, e ports.EventRecord) error {
	var errs []error
	for _, l := range f {
		errs = append(errs, l.LogEvent(ctx, e))
	}
	return errors.Join(errs...)
}

// demoOptions configure one live demo.
type demoOptions struct {
	Seed    uint64
	Load    float64 // initial load, instance units
	Initial int     // initial instances
	LogDir  string
	Cycles  int // stop after this many cycles; 0 runs until quit
	Clock   ports.Clock
}

// runDemo runs the demo until ctx is done, the presenter quits or the cycle
// limit is reached. Commands are read from in; everything prints to out.
func runDemo(ctx context.Context, opts demoOptions, in io.Reader, out io.Writer) error {
	cfg := app.DemoConfig()
	clk := opts.Clock
	out = &syncWriter{w: out}

	d := &dial{}
	d.Set(opts.Load)
	group := fakeasg.New(clk, fakeasg.Config{
		Min: cfg.Policy.MinInstances, Max: cfg.Policy.MaxInstances, InitialDesired: opts.Initial,
		Warmup: cfg.Warmup, DeregistrationDelay: cfg.DeregistrationDelay, LaunchRetryInterval: 5 * time.Second,
	})
	metrics := mockmetrics.New(opts.Seed, clk.Now(), mockmetrics.Profile{
		Load:        func(time.Duration) float64 { return d.Get() },
		NoiseStdDev: demoNoiseStd,
	}, group)

	writer, err := jsonllog.New(opts.LogDir)
	if err != nil {
		return err
	}
	runID := "demo-" + clk.Now().Format("20060102T150405Z")
	ctrl, err := app.New(cfg, core.ModeSim, runID, app.Deps{
		Metrics:     metrics,
		Provisioner: group,
		State:       &memstate.Store{},
		Log:         fanout{writer, printer{out: out, dial: d}},
		Clock:       clk,
	})
	if err != nil {
		return errors.Join(err, writer.Close())
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	fmt.Fprintf(out, "live demo: demo profile, one cycle every %s, decision log in %s\n", cfg.Policy.EvaluationInterval, opts.LogDir)
	fmt.Fprint(out, demoHelp)
	go readCommands(in, d, out, cancel)

	loopErr := demoLoop(ctx, ctrl, clk, cfg.Policy.EvaluationInterval, opts.Cycles)
	return errors.Join(loopErr, writer.Close())
}

// demoLoop drives Start, the cycles and Stop. A cancelled ctx (quit or
// Ctrl+C) ends the demo normally: Start and Stop always complete, so the
// log is well formed even when the presenter quits immediately.
func demoLoop(ctx context.Context, ctrl *app.Controller, clk ports.Clock, interval time.Duration, limit int) error {
	if err := ctrl.Start(context.WithoutCancel(ctx)); err != nil {
		return err
	}
	for i := 1; ctx.Err() == nil; i++ {
		began := clk.Now()
		if err := ctrl.Cycle(ctx); err != nil && ctx.Err() == nil {
			return errors.Join(err, ctrl.Stop(ctx))
		}
		if limit > 0 && i >= limit {
			break
		}
		if err := clk.Sleep(ctx, max(0, interval-clk.Now().Sub(began))); err != nil {
			break
		}
	}
	return ctrl.Stop(ctx)
}
