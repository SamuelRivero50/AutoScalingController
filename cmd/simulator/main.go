// Command simulator runs the closed-loop scenarios S1-S10 (docs/spec/simulator.md)
// against the real decision core and control loop with simulated adapters,
// writes the JSONL decision logs, and reports each acceptance criterion.
//
// Usage:
//
//	simulator [-scenario all|S1..S10] [-seed N] [-out DIR]
//	simulator -demo [-load L] [-initial N] [-cycles N] [-seed N] [-out DIR]
//
// It exits with status 1 if any scenario fails its criterion. With -demo it
// runs the interactive live demo instead (docs/spec/simulator.md §4): the
// demo profile in real time, with the load set from standard input.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/clock"
	"github.com/SamuelRivero50/AutoScalingController/internal/simulator"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("simulator", flag.ContinueOnError)
	fs.SetOutput(stderr)
	which := fs.String("scenario", "all", "scenario ID (S1..S10) or all")
	seed := fs.Uint64("seed", 1, "random seed for the metrics generator")
	out := fs.String("out", "sim-logs", "directory for the JSONL decision logs (one subdirectory per scenario)")
	demo := fs.Bool("demo", false, "run the interactive live demo (demo profile) instead of the scenarios")
	load := fs.Float64("load", loadPresets["comfortable"], "demo: initial load in instance units (1.0 saturates one instance)")
	initial := fs.Int("initial", 2, "demo: initial instance count")
	cycles := fs.Int("cycles", 0, "demo: stop after this many cycles (0 runs until quit)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *demo {
		if *load < 0 || *load > maxDialLoad || *initial < 1 || *cycles < 0 {
			fmt.Fprintf(stderr, "invalid demo flags: load must be in [0, %g], initial >= 1, cycles >= 0\n", maxDialLoad)
			return 2
		}
		opts := demoOptions{
			Seed: *seed, Load: *load, Initial: *initial, Cycles: *cycles,
			LogDir: filepath.Join(*out, "demo"), Clock: clock.System{},
		}
		if err := runDemo(ctx, opts, stdin, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}

	scenarios := simulator.Scenarios()
	if !strings.EqualFold(*which, "all") {
		sc, ok := simulator.ByID(strings.ToUpper(*which))
		if !ok {
			fmt.Fprintf(stderr, "unknown scenario %q\n", *which)
			return 2
		}
		scenarios = []simulator.Scenario{sc}
	}

	failed := 0
	for _, sc := range scenarios {
		dir := filepath.Join(*out, fmt.Sprintf("%s-seed%d", sc.ID, *seed))
		r, err := simulator.Run(ctx, sc, simulator.Options{Seed: *seed, LogDir: dir})
		if err != nil {
			fmt.Fprintf(stderr, "%-4s ERROR %v\n", sc.ID, err)
			failed++
			continue
		}
		if err := sc.Check(r); err != nil {
			fmt.Fprintf(stdout, "%-4s FAIL  %-36s %v\n", sc.ID, sc.Name, err)
			failed++
			continue
		}
		fmt.Fprintf(stdout, "%-4s PASS  %-36s %3d cycles  log: %s\n", sc.ID, sc.Name, len(r.Cycles), dir)
	}
	if failed > 0 {
		fmt.Fprintf(stdout, "%d of %d scenarios failed\n", failed, len(scenarios))
		return 1
	}
	return 0
}
