// Command analyze reads JSONL decision logs and prints the evaluation
// metrics of docs/spec/simulator.md §5 for every run it finds.
//
// Usage:
//
//	analyze [-json] [PATH ...]
//
// Each PATH is a .jsonl file or a directory searched recursively; the
// default is sim-logs. Records are grouped by run_id.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/evaluation"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the reports as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	paths := fs.Args()
	if len(paths) == 0 {
		paths = []string{"sim-logs"}
	}
	runs, err := evaluation.Load(paths...)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	reports := make([]evaluation.Report, 0, len(runs))
	for _, r := range runs {
		reports = append(reports, evaluation.Evaluate(r))
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(reports); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	for i, rep := range reports {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		printReport(stdout, rep)
	}
	if len(reports) > 1 {
		fmt.Fprintln(stdout)
		printSummary(stdout, reports)
	}
	return 0
}

func dur(d evaluation.Duration) string {
	return time.Duration(d).String()
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func printReport(w io.Writer, r evaluation.Report) {
	fmt.Fprintf(w, "== %s (%s, %s, config %s) ==\n", r.RunID, r.Mode, r.Profile, shortHash(r.ConfigHash))
	if r.Cycles == 0 {
		fmt.Fprintln(w, "no cycle records")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	defer tw.Flush()
	row := func(label, format string, a ...any) {
		fmt.Fprintf(tw, "%s\t%s\n", label, fmt.Sprintf(format, a...))
	}

	row("cycles", "%d every %s, %s .. %s", r.Cycles, dur(r.Interval),
		r.Start.Format(time.RFC3339), r.End.Format(time.RFC3339))

	s := r.SLO
	if s.EvaluableCycles == 0 {
		row("SLO compliance", "n/a (no cycle with evaluable latency and errors, e.g. no traffic)")
	} else {
		row("SLO compliance", "%.1f%% (%d/%d evaluable cycles; latency breaches %d, error breaches %d)",
			s.CompliancePct, s.CompliantCycles, s.EvaluableCycles, s.LatencyBreaches, s.ErrorBreaches)
	}

	p := r.Provisioning
	row("instance-minutes", "%.1f total; %.1f used vs %.1f minimum on %d CPU-valid cycles (%+.1f%%)",
		p.InstanceMinutes, p.UsedInstanceMinutes, p.MinimumInstanceMinutes, p.EvaluableCycles, p.ExcessPct)
	row("provisioning", "over %.1f%% of cycles (mean +%.2f), under %.1f%% (mean -%.2f), saturated %d",
		p.OverPct, p.OverMagnitude, p.UnderPct, p.UnderMagnitude, p.SaturatedCycles)

	c := r.Changes
	row("changes", "%d scale-out, %d scale-in, %d breaker resets, %d stuck terminations, %d skipped, %d failed",
		c.ScaleOuts, c.ScaleIns, c.Resets, c.Terminations, c.Skipped, c.Errors)
	row("oscillation", "%d direction reversals, %d increases within 10m of a reduce",
		c.Reversals, c.IncreaseAfterReduce)

	rel := r.Relief
	row("time-to-relief", "%d incidents, %d relieved by scale-out (median %s, max %s)",
		len(rel.Incidents), rel.Resolved, dur(rel.Median), dur(rel.Max))
	for i, in := range rel.Incidents {
		switch {
		case !in.Resolved:
			row("", "#%d from cycle %d: not relieved by the end of the run", i+1, in.StartCycle)
		case in.ScaledOut:
			row("", "#%d cycles %d-%d: %s = decision %s + reaction %s", i+1, in.StartCycle, in.EndCycle,
				dur(in.TimeToRelief), dur(in.DecisionLatency), dur(in.ReactionTime))
		default:
			row("", "#%d cycles %d-%d: %s, transient (no scale-out)", i+1, in.StartCycle, in.EndCycle, dur(in.TimeToRelief))
		}
	}

	f := r.Flags
	row("late/incorrect", "SLO breach with low CPU %d, overload at max %d, skipped %d, failed %d",
		f.SLOBreachLowCPU.Count, f.OverloadAtMax.Count, f.Skipped.Count, f.Failed.Count)
	for _, set := range []evaluation.FlagSet{f.SLOBreachLowCPU, f.OverloadAtMax, f.Skipped, f.Failed} {
		for _, ex := range set.Examples {
			cpu := "n/a"
			if ex.CPU != nil {
				cpu = fmt.Sprintf("%.1f", *ex.CPU)
			}
			row("", "cycle %d %s %s/%s cpu %s in_service %d", ex.CycleID, ex.TS.Format(time.RFC3339),
				ex.Decision, ex.ReasonCode, cpu, ex.InService)
		}
	}

	wu := r.Warmup
	if wu.Samples > 0 {
		row("warmup", "%d samples: min %s, median %s, max %s (assumed %s)",
			wu.Samples, dur(wu.Min), dur(wu.Median), dur(wu.Max), dur(wu.Assumed))
	} else {
		row("warmup", "no launched instance reached IN_SERVICE (assumed %s)", dur(wu.Assumed))
	}

	var reasons []string
	for _, code := range core.ReasonCodes() {
		if n := r.Reasons[code]; n > 0 {
			reasons = append(reasons, fmt.Sprintf("%s %d", code, n))
		}
	}
	row("reason codes", "%s", strings.Join(reasons, ", "))
}

func printSummary(w io.Writer, reports []evaluation.Report) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	defer tw.Flush()
	fmt.Fprintln(tw, "run\tcycles\tSLO %\tinst-min\tminimum\texcess %\tout\tin\treset\treversals\tflips\trelief median\t")
	for _, r := range reports {
		p, c := r.Provisioning, r.Changes
		slo := "n/a"
		if r.SLO.EvaluableCycles > 0 {
			slo = fmt.Sprintf("%.1f", r.SLO.CompliancePct)
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%.1f\t%.1f\t%+.1f\t%d\t%d\t%d\t%d\t%d\t%s\t\n",
			r.RunID, r.Cycles, slo, p.UsedInstanceMinutes, p.MinimumInstanceMinutes, p.ExcessPct,
			c.ScaleOuts, c.ScaleIns, c.Resets, c.Reversals, c.IncreaseAfterReduce, dur(r.Relief.Median))
	}
}
