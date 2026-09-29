package evaluation

import (
	"encoding/json"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
	"github.com/SamuelRivero50/AutoScalingController/internal/simulator"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// cyc builds a realistic-profile cycle one minute after the previous id.
type cyc struct {
	cpu       float64 // negative means MISSING
	inService int
	decision  core.DecisionKind
	reason    core.ReasonCode
	action    core.ActionType
	status    ports.ActionStatus
	target    int // desired_capacity param when > 0
	latency   float64
	latBreach bool
	overload  bool
}

func build(specs ...cyc) Run {
	run := Run{ID: "r"}
	for i, s := range specs {
		c := Cycle{
			RunID: "r", CycleID: int64(i + 1), Mode: core.ModeSim, Profile: core.ProfileRealistic,
			TS:       t0.Add(time.Duration(i) * time.Minute),
			Capacity: Capacity{Desired: s.inService, InService: s.inService, Min: 1, Max: 5},
			Decision: s.decision, ReasonCode: s.reason,
			Action: Action{Type: s.action, Status: s.status, Params: map[string]any{}},
		}
		if c.Decision == "" {
			c.Decision = core.MaintainCapacity
		}
		if c.ReasonCode == "" {
			c.ReasonCode = core.ReasonMaintainStable
		}
		if c.Action.Type == "" {
			c.Action.Type, c.Action.Status = core.ActionNone, ports.ActionOK
		}
		if s.target > 0 {
			c.Action.Params["desired_capacity"] = float64(s.target)
		}
		threshold := 70.0
		if s.cpu >= 0 {
			v := s.cpu
			c.Observation.Signals = []Signal{{Name: core.SignalCPU, Value: &v, Quality: core.QualityValid}}
			c.Conditions = append(c.Conditions, Condition{Name: "cpu_high", Value: &v, Threshold: &threshold, Met: v >= threshold})
		} else {
			c.Observation.Signals = []Signal{{Name: core.SignalCPU, Quality: core.QualityMissing}}
			c.Conditions = append(c.Conditions, Condition{Name: "cpu_high", Threshold: &threshold})
		}
		lat, zero := s.latency, 0.0
		latPtr, errPtr := &lat, &zero
		if s.latency < 0 {
			latPtr, errPtr = nil, nil
		}
		c.Conditions = append(c.Conditions,
			Condition{Name: "latency_p95_breach", Value: latPtr, Met: s.latBreach},
			Condition{Name: "error_rate_breach", Value: errPtr},
			Condition{Name: "overload", Met: s.overload},
		)
		run.Cycles = append(run.Cycles, c)
	}
	return run
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestDuration_MarshalJSON(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(Duration(90 * time.Second))
	if err != nil || string(b) != "90" {
		t.Fatalf("Marshal = %s, %v; want 90", b, err)
	}
}

func TestEvaluate_Empty(t *testing.T) {
	t.Parallel()
	rep := Evaluate(Run{ID: "empty"})
	if rep.Cycles != 0 || rep.RunID != "empty" {
		t.Fatalf("report = %+v", rep)
	}
}

func TestEvaluate_Header(t *testing.T) {
	t.Parallel()
	rep := Evaluate(build(cyc{cpu: 40, inService: 2}, cyc{cpu: 40, inService: 2}, cyc{cpu: 40, inService: 2}))
	if rep.Cycles != 3 || rep.Interval != Duration(time.Minute) || !rep.End.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("header = %+v", rep)
	}
	if rep.Decisions[core.MaintainCapacity] != 3 || rep.Reasons[core.ReasonMaintainStable] != 3 {
		t.Fatalf("breakdowns = %v %v", rep.Decisions, rep.Reasons)
	}
}

func TestEvaluate_SLO(t *testing.T) {
	t.Parallel()
	rep := Evaluate(build(
		cyc{cpu: 40, inService: 2, latency: 100},
		cyc{cpu: 40, inService: 2, latency: 900, latBreach: true},
		cyc{cpu: 40, inService: 2, latency: -1}, // not evaluable
		cyc{cpu: 40, inService: 2, latency: 100},
	))
	s := rep.SLO
	if s.EvaluableCycles != 3 || s.CompliantCycles != 2 || s.LatencyBreaches != 1 || !near(s.CompliancePct, 200.0/3) {
		t.Fatalf("SLO = %+v", s)
	}
}

func TestMinimumInstances(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		cpu       float64
		inService int
		want      int
		ok        bool
		saturated bool
	}{
		// load = cpu*n/100 instance units; R = ceil(load/0.55) in [1, 5].
		{name: "exactly at the comfort bound", cpu: 55, inService: 2, want: 2, ok: true},
		{name: "just above the bound", cpu: 56, inService: 2, want: 3, ok: true},
		{name: "idle floors at one", cpu: 2, inService: 3, want: 1, ok: true},
		{name: "capped at max", cpu: 90, inService: 5, want: 5, ok: true},
		{name: "saturated", cpu: 100, inService: 2, want: 4, ok: true, saturated: true},
		{name: "cpu missing", cpu: -1, inService: 2},
		{name: "no instances", cpu: 50, inService: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := build(cyc{cpu: tt.cpu, inService: tt.inService}).Cycles[0]
			r, sat, ok := minimumInstances(c, 55)
			if r != tt.want || ok != tt.ok || sat != tt.saturated {
				t.Fatalf("minimumInstances = %d, %v, %v; want %d, %v, %v", r, sat, ok, tt.want, tt.saturated, tt.ok)
			}
		})
	}
}

func TestEvaluate_Provisioning(t *testing.T) {
	t.Parallel()
	rep := Evaluate(build(
		cyc{cpu: 20, inService: 3}, // R=2: over by 1
		cyc{cpu: 80, inService: 2}, // R=3: under by 1
		cyc{cpu: 50, inService: 2}, // R=2: exact
		cyc{cpu: -1, inService: 2}, // not evaluable
	))
	p := rep.Provisioning
	if !near(p.InstanceMinutes, 9) || p.EvaluableCycles != 3 {
		t.Fatalf("minutes/evaluable = %+v", p)
	}
	if !near(p.UsedInstanceMinutes, 7) || !near(p.MinimumInstanceMinutes, 7) || !near(p.ExcessPct, 0) {
		t.Fatalf("used/minimum = %+v", p)
	}
	if p.OverCycles != 1 || p.UnderCycles != 1 || !near(p.OverMagnitude, 1) || !near(p.UnderMagnitude, 1) || !near(p.OverPct, 100.0/3) {
		t.Fatalf("over/under = %+v", p)
	}
}

func TestEvaluate_Changes(t *testing.T) {
	t.Parallel()
	inc := cyc{cpu: 80, inService: 2, decision: core.IncreaseCapacity, reason: core.ReasonIncreaseCPUHigh, action: core.ActionSetDesiredCapacity, status: ports.ActionOK, target: 4}
	red := cyc{cpu: 20, inService: 4, decision: core.ReduceCapacity, reason: core.ReasonReduceProjectionOK, action: core.ActionSetDesiredCapacity, status: ports.ActionOK, target: 3}
	skipped := inc
	skipped.status = ports.ActionSkipped
	failed := inc
	failed.status = ports.ActionError
	term := cyc{cpu: 40, inService: 2, action: core.ActionTerminateInstance, status: ports.ActionOK}
	reset := cyc{cpu: 90, inService: 3, decision: core.IncreaseCapacity, reason: core.ReasonIncreaseCPUHigh, action: core.ActionSetDesiredCapacity, status: ports.ActionOK, target: 2}

	rep := Evaluate(build(inc, red, inc, skipped, failed, term, reset))
	ch := rep.Changes
	if ch.ScaleOuts != 2 || ch.ScaleIns != 1 || ch.Resets != 1 || ch.Reversals != 3 || ch.Skipped != 1 || ch.Errors != 1 || ch.Terminations != 1 {
		t.Fatalf("changes = %+v", ch)
	}
	// The four increase decisions after the reduce (executed, skipped,
	// failed, and the one overridden by the reset) all fall inside the
	// 10-minute window.
	if ch.IncreaseAfterReduce != 4 {
		t.Fatalf("IncreaseAfterReduce = %d, want 4", ch.IncreaseAfterReduce)
	}
}

func TestDirection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		spec cyc
		want int
	}{
		{name: "target above desired", spec: cyc{inService: 2, target: 4}, want: 1},
		{name: "target below desired", spec: cyc{inService: 2, target: 1}, want: -1},
		{name: "target equal", spec: cyc{inService: 2, target: 2}},
		{name: "no param increase", spec: cyc{inService: 2, decision: core.IncreaseCapacity}, want: 1},
		{name: "no param reduce", spec: cyc{inService: 2, decision: core.ReduceCapacity}, want: -1},
		{name: "no param maintain", spec: cyc{inService: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := direction(build(tt.spec).Cycles[0]); got != tt.want {
				t.Fatalf("direction = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEvaluate_Relief(t *testing.T) {
	t.Parallel()
	inc := cyc{cpu: 85, inService: 2, decision: core.IncreaseCapacity, reason: core.ReasonIncreaseCPUHigh}
	rep := Evaluate(build(
		cyc{cpu: 40, inService: 2},
		cyc{cpu: 75, inService: 2}, // incident 1 starts (minute 1)
		cyc{cpu: -1, inService: 2}, // missing CPU does not end it
		inc,                        // scale-out decided (minute 3)
		cyc{cpu: 60, inService: 4}, // relieved (minute 4)
		cyc{cpu: 90, inService: 4}, // incident 2: transient spike
		cyc{cpu: 50, inService: 4},
		cyc{cpu: 95, inService: 4}, // incident 3: unresolved
	))
	rel := rep.Relief
	if len(rel.Incidents) != 3 {
		t.Fatalf("incidents = %+v", rel.Incidents)
	}
	first := rel.Incidents[0]
	if !first.Resolved || !first.ScaledOut || first.StartCycle != 2 || first.EndCycle != 5 ||
		first.TimeToRelief != Duration(3*time.Minute) || first.DecisionLatency != Duration(2*time.Minute) || first.ReactionTime != Duration(time.Minute) {
		t.Fatalf("incident 1 = %+v", first)
	}
	if second := rel.Incidents[1]; !second.Resolved || second.ScaledOut {
		t.Fatalf("incident 2 = %+v, want resolved without scale-out", second)
	}
	if third := rel.Incidents[2]; third.Resolved {
		t.Fatalf("incident 3 = %+v, want unresolved", third)
	}
	if rel.Resolved != 1 || rel.Median != Duration(3*time.Minute) || rel.Max != Duration(3*time.Minute) {
		t.Fatalf("summary = %+v", rel)
	}
}

func TestEvaluate_Flags(t *testing.T) {
	t.Parallel()
	blind := cyc{cpu: 30, inService: 2, reason: core.ReasonMaintainSLOBreachLowCPU}
	atMax := cyc{cpu: 90, inService: 5, reason: core.ReasonMaintainAtMax, overload: true}
	atMaxCalm := cyc{cpu: 40, inService: 5, reason: core.ReasonMaintainAtMax}
	skipped := cyc{cpu: 80, inService: 2, decision: core.IncreaseCapacity, reason: core.ReasonIncreaseCPUHigh, action: core.ActionSetDesiredCapacity, status: ports.ActionSkipped}
	specs := []cyc{atMax, atMaxCalm, skipped}
	for range maxExamples + 2 {
		specs = append(specs, blind)
	}
	f := Evaluate(build(specs...)).Flags
	if f.SLOBreachLowCPU.Count != maxExamples+2 || len(f.SLOBreachLowCPU.Examples) != maxExamples {
		t.Fatalf("SLOBreachLowCPU = %+v", f.SLOBreachLowCPU)
	}
	if ex := f.SLOBreachLowCPU.Examples[0]; ex.CycleID != 4 || ex.CPU == nil || *ex.CPU != 30 {
		t.Fatalf("example = %+v", ex)
	}
	if f.OverloadAtMax.Count != 1 || f.Skipped.Count != 1 || f.Failed.Count != 0 {
		t.Fatalf("flags = %+v", f)
	}
}

func TestEvaluate_Warmup(t *testing.T) {
	t.Parallel()
	run := build(cyc{cpu: 80, inService: 1}, cyc{cpu: 80, inService: 1}, cyc{cpu: 60, inService: 2})
	launched := t0.Add(30 * time.Second)
	old := t0.Add(-time.Hour)
	run.Cycles[0].Capacity.Instances = []Instance{{ID: "i-old", State: core.InstanceInService, LaunchedAt: &old}}
	run.Cycles[1].Capacity.Instances = []Instance{
		{ID: "i-old", State: core.InstanceInService, LaunchedAt: &old},
		{ID: "i-new", State: core.InstancePending, LaunchedAt: &launched},
	}
	run.Cycles[2].Capacity.Instances = []Instance{
		{ID: "i-old", State: core.InstanceInService, LaunchedAt: &old},
		{ID: "i-new", State: core.InstanceInService, LaunchedAt: &launched},
	}
	w := Evaluate(run).Warmup
	if w.Samples != 1 || w.Median != Duration(90*time.Second) || w.Assumed != Duration(180*time.Second) {
		t.Fatalf("warmup = %+v", w)
	}
}

// TestEvaluate_Scenarios runs the closed-loop scenarios, reads back the
// JSONL they wrote and checks the metrics agree with each scenario's intent.
func TestEvaluate_Scenarios(t *testing.T) {
	t.Parallel()
	checks := map[string]func(t *testing.T, r Report){
		"S1": func(t *testing.T, r Report) {
			if r.Changes.ScaleOuts+r.Changes.ScaleIns != 0 || r.SLO.CompliancePct != 100 {
				t.Errorf("stable run changed capacity or breached the SLO: %+v %+v", r.Changes, r.SLO)
			}
		},
		"S2": func(t *testing.T, r Report) {
			if r.Changes.ScaleOuts == 0 || r.Relief.Resolved == 0 {
				t.Errorf("sustained increase not relieved by scale-out: %+v %+v", r.Changes, r.Relief)
			}
		},
		"S4": func(t *testing.T, r Report) {
			if r.Changes.ScaleIns == 0 {
				t.Errorf("sustained decrease did not scale in: %+v", r.Changes)
			}
		},
		"S5": func(t *testing.T, r Report) {
			if r.Changes.IncreaseAfterReduce != 0 {
				t.Errorf("edge noise flapped: %+v", r.Changes)
			}
		},
		"S6": func(t *testing.T, r Report) {
			if r.Changes.Skipped+r.Changes.Errors == 0 {
				t.Errorf("launch failures left no trace: %+v", r.Changes)
			}
		},
		"S7": func(t *testing.T, r Report) {
			if r.Changes.Terminations == 0 {
				t.Errorf("stuck instance not terminated: %+v", r.Changes)
			}
		},
		"S8": func(t *testing.T, r Report) {
			if r.Reasons[core.ReasonMaintainBlind] == 0 || r.SLO.EvaluableCycles == r.Cycles {
				t.Errorf("blind cycles not visible: %v %+v", r.Reasons, r.SLO)
			}
		},
	}
	for _, sc := range simulator.Scenarios() {
		t.Run(sc.ID, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(t.TempDir(), sc.ID)
			res, err := simulator.Run(t.Context(), sc, simulator.Options{Seed: 1, LogDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			runs, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(runs) != 1 {
				t.Fatalf("got %d runs, want 1", len(runs))
			}
			rep := Evaluate(runs[0])
			if rep.Cycles != len(res.Cycles) || rep.Interval != Duration(res.Config.Policy.EvaluationInterval) {
				t.Fatalf("report covers %d cycles every %v, run had %d every %v",
					rep.Cycles, time.Duration(rep.Interval), len(res.Cycles), res.Config.Policy.EvaluationInterval)
			}
			if rep.Provisioning.InstanceMinutes <= 0 {
				t.Fatalf("no instance-minutes: %+v", rep.Provisioning)
			}
			if check, ok := checks[sc.ID]; ok {
				check(t, rep)
			}
		})
	}
}
