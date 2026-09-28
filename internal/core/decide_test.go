package core

import (
	"reflect"
	"slices"
	"testing"
)

// TestEveryReasonCode drives the pipeline into each of the 14 catalog codes.
func TestEveryReasonCode(t *testing.T) {
	cfg := RealisticConfig()
	tests := []struct {
		reason ReasonCode
		run    func(h *harness) Decision
		target int // expected absolute target for INCREASE/REDUCE
	}{
		{ReasonIncreaseCPUHigh, func(h *harness) Decision {
			h.load(fleet(2), hot)
			return h.load(fleet(2), hot)
		}, 3},
		{ReasonIncreaseLatencySLO, func(h *harness) Decision {
			s := obsSpec{cpu: 60, requests: 100, latencyMs: 700}
			h.load(fleet(2), s)
			return h.load(fleet(2), s)
		}, 3},
		{ReasonIncreaseCapacityErrors, func(h *harness) Decision {
			s := obsSpec{cpu: 60, requests: 100, latencyMs: 100, elb5xx: 4, target5xx: 1}
			h.load(fleet(3), s)
			return h.load(fleet(3), s)
		}, 4},
		{ReasonIncreaseAppErrorsWithLoad, func(h *harness) Decision {
			s := obsSpec{cpu: 60, requests: 100, latencyMs: 100, target5xx: 4}
			h.load(fleet(3), s)
			return h.load(fleet(3), s)
		}, 4},
		{ReasonReduceProjectionOK, func(h *harness) Decision {
			for range 4 {
				h.load(fleet(4), cool)
			}
			return h.load(fleet(4), cool)
		}, 3},
		{ReasonMaintainStable, func(h *harness) Decision {
			return h.load(fleet(2), warm)
		}, 0},
		{ReasonMaintainWindowPending, func(h *harness) Decision {
			return h.load(fleet(2), hot)
		}, 0},
		{ReasonMaintainPendingCapacity, func(h *harness) Decision {
			return h.load(fleet(2, InstancePending), hot)
		}, 0},
		{ReasonMaintainScaleInBlocked, func(h *harness) Decision {
			h.now = h.now.Add(cfg.EvaluationInterval)
			h.step(fleet(3), Observation{Now: h.now})
			return h.load(fleet(3), cool)
		}, 0},
		{ReasonMaintainAtMax, func(h *harness) Decision {
			h.load(fleet(5), hot)
			return h.load(fleet(5), hot)
		}, 0},
		{ReasonMaintainAtMin, func(h *harness) Decision {
			return h.load(fleet(1), cool)
		}, 0},
		{ReasonMaintainBlind, func(h *harness) Decision {
			h.now = h.now.Add(cfg.EvaluationInterval)
			return h.step(fleet(2), Observation{Now: h.now})
		}, 0},
		{ReasonMaintainStateUnknown, func(h *harness) Decision {
			return h.load(CapacitySnapshot{Known: false}, hot)
		}, 0},
		{ReasonMaintainNoHealthyTargets, func(h *harness) Decision {
			c := fleet(3)
			c.HealthyTargets = 0
			return h.load(c, hot)
		}, 0},
		{ReasonMaintainSLOBreachLowCPU, func(h *harness) Decision {
			return h.load(fleet(2), obsSpec{cpu: 30, requests: 100, latencyMs: 900})
		}, 0},
	}

	covered := map[ReasonCode]bool{}
	for _, tt := range tests {
		t.Run(string(tt.reason), func(t *testing.T) {
			d := tt.run(newHarness(cfg))
			requireReason(t, d, tt.reason)
			covered[tt.reason] = true
			switch d.Decision {
			case MaintainCapacity:
				if d.Action.Type != ActionNone {
					t.Fatalf("MAINTAIN with action %+v", d.Action)
				}
			default:
				if d.Action.Type != ActionSetDesiredCapacity || d.Action.TargetDesired != tt.target {
					t.Fatalf("action = %+v, want SET_DESIRED_CAPACITY to %d", d.Action, tt.target)
				}
			}
		})
	}
	for _, r := range ReasonCodes() {
		if !covered[r] {
			t.Errorf("reason code %s not exercised", r)
		}
	}
}

func TestLatencyTriggeredScaleOutIsCappedAtOne(t *testing.T) {
	h := newHarness(RealisticConfig())
	s := obsSpec{cpu: 69, requests: 100, latencyMs: 900} // CPU alone would justify +2 on N=2 (ceil(2.51)=3)
	h.load(fleet(2), s)
	d := h.load(fleet(2), s)
	requireReason(t, d, ReasonIncreaseLatencySLO)
	if d.Action.Step != 1 || d.Action.TargetDesired != 3 {
		t.Fatalf("action = %+v, want +1 to 3", d.Action)
	}
}

func TestCPUTriggeredProportionalStep(t *testing.T) {
	h := newHarness(RealisticConfig())
	h.load(fleet(2), obsSpec{cpu: 95})
	d := h.load(fleet(2), obsSpec{cpu: 95})
	requireReason(t, d, ReasonIncreaseCPUHigh)
	if d.Action.Step != 2 || d.Action.TargetDesired != 4 {
		t.Fatalf("action = %+v, want +2 to 4", d.Action)
	}
}

func TestScaleOutNeverExceedsMax(t *testing.T) {
	h := newHarness(RealisticConfig())
	h.load(fleet(4), obsSpec{cpu: 100})
	d := h.load(fleet(4), obsSpec{cpu: 100})
	if d.Action.TargetDesired != 5 {
		t.Fatalf("target = %d, want 5 (max)", d.Action.TargetDesired)
	}
}

func TestZeroHealthyTargets(t *testing.T) {
	h := newHarness(RealisticConfig())

	c := fleet(2)
	c.HealthyTargets = 0
	h.load(c, hot)
	d := h.load(c, hot)
	requireReason(t, d, ReasonMaintainNoHealthyTargets)
	if !slices.Contains(d.Alerts, AlertZeroHealthyTargets) {
		t.Fatalf("alerts = %v, want %s", d.Alerts, AlertZeroHealthyTargets)
	}

	// Instances launching explains zero healthy targets: no alert.
	launching := fleet(0, InstancePending)
	launching.HealthyTargets = 0
	d = h.load(launching, hot)
	requireReason(t, d, ReasonMaintainPendingCapacity)
	if len(d.Alerts) != 0 {
		t.Fatalf("alerts = %v, want none while launching", d.Alerts)
	}
}

func TestBlindAlertAfterConsecutiveBlindCycles(t *testing.T) {
	for _, cfg := range []PolicyConfig{RealisticConfig(), DemoConfig()} {
		t.Run(string(cfg.Profile), func(t *testing.T) {
			h := newHarness(cfg)
			capacity := fleet(2)
			alerts := 0
			for i := 1; i <= cfg.BlindAlertThreshold+3; i++ {
				h.now = h.now.Add(cfg.EvaluationInterval)
				d := h.step(capacity, Observation{Now: h.now})
				requireReason(t, d, ReasonMaintainBlind)
				if slices.Contains(d.Alerts, AlertBlind) {
					alerts++
					if i != cfg.BlindAlertThreshold {
						t.Fatalf("blind alert at cycle %d, want %d", i, cfg.BlindAlertThreshold)
					}
				}
			}
			if alerts != 1 {
				t.Fatalf("blind alerts = %d, want exactly 1", alerts)
			}

			// A non-blind cycle resets the streak.
			h.load(capacity, warm)
			if h.mem.BlindStreak != 0 {
				t.Fatalf("blind streak = %d after a non-blind cycle", h.mem.BlindStreak)
			}
		})
	}
}

func TestCPUMissingWithValidSLOIsBlind(t *testing.T) {
	cfg := RealisticConfig()
	h := newHarness(cfg)
	capacity := fleet(2)
	obs := observe(h.now.Add(cfg.EvaluationInterval), cfg, capacity, obsSpec{cpu: 50, requests: 100, latencyMs: 900})
	h.now = obs.Now
	obs.CPU = nil
	d := h.step(capacity, obs)
	requireReason(t, d, ReasonMaintainBlind)
	if !d.Blind {
		t.Fatal("unusable CPU must be blind even with valid latency")
	}
	// The valid latency is still part of the justification.
	for _, c := range d.Conditions {
		if c.Name == "latency_p95_breach" && (c.Value == nil || *c.Value != 900 || !c.Met) {
			t.Fatalf("latency evidence lost: %+v", c)
		}
	}
}

// TestAtMaxKeepsOverloadEvidence: capped at max, the decision is MAINTAIN
// but the justification must still show the controller wanted to scale.
func TestAtMaxKeepsOverloadEvidence(t *testing.T) {
	h := newHarness(RealisticConfig())
	h.load(fleet(5), hot)
	d := h.load(fleet(5), hot)
	requireReason(t, d, ReasonMaintainAtMax)
	met := map[string]bool{}
	for _, c := range d.Conditions {
		met[c.Name] = c.Met
	}
	for _, name := range []string{"cpu_high", "overload", "scale_out_window", "at_max"} {
		if !met[name] {
			t.Errorf("condition %s must be met: true at max capacity", name)
		}
	}
}

func TestScaleInStopsAtMinimum(t *testing.T) {
	h := newHarness(DemoConfig())
	c := fleet(2)
	for range 3 {
		h.load(c, obsSpec{cpu: 5})
	}
	if d := h.load(c, obsSpec{cpu: 5}); d.Decision == ReduceCapacity && d.Action.TargetDesired < 1 {
		t.Fatalf("scaled below min: %+v", d.Action)
	}
	one := fleet(1)
	for range 10 {
		d := h.load(one, obsSpec{cpu: 1})
		if d.Decision == ReduceCapacity {
			t.Fatalf("REDUCE at N=1: %+v", d)
		}
		requireReason(t, d, ReasonMaintainAtMin)
	}
}

func TestProjectionBlocksScaleIn(t *testing.T) {
	h := newHarness(RealisticConfig())
	c := fleet(3)
	// 40% on 3 instances projects to 60% on 2: never comfortable.
	for range 10 {
		d := h.load(c, obsSpec{cpu: 40})
		if d.Decision == ReduceCapacity {
			t.Fatal("scaled in although the projection exceeds 55%")
		}
		requireReason(t, d, ReasonMaintainStable)
	}
}

func TestDecideIsDeterministicAndDoesNotMutateInput(t *testing.T) {
	cfg := RealisticConfig()
	h := newHarness(cfg)
	c := fleet(3, InstanceDraining)
	h.load(c, hot)
	obs := observe(h.now.Add(cfg.EvaluationInterval), cfg, c, hot)
	sig := Classify(obs, c, h.mem.LastConsumedCPU, cfg)
	mem := Advance(h.mem, 99, sig, c, cfg)
	in := PolicyInput{CycleID: 99, Config: cfg, Signals: sig, Capacity: c, Memory: mem}

	memBefore := slices.Clone(mem.Entries)
	instBefore := slices.Clone(c.Instances)
	first := Decide(in)
	second := Decide(in)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Decide not deterministic:\n%+v\n%+v", first, second)
	}
	if !slices.Equal(mem.Entries, memBefore) || !slices.Equal(c.Instances, instBefore) {
		t.Fatal("Decide mutated its input")
	}

	prevEntries := slices.Clone(h.mem.Entries)
	_ = Advance(h.mem, 100, sig, c, cfg)
	if !slices.Equal(h.mem.Entries, prevEntries) {
		t.Fatal("Advance mutated its input memory")
	}
}

func TestConditionsAreRecorded(t *testing.T) {
	h := newHarness(RealisticConfig())
	d := h.load(fleet(2), hot)
	names := map[string]Condition{}
	for _, c := range d.Conditions {
		names[c.Name] = c
	}
	for _, want := range []string{"cpu_high", "overload", "projected_cpu", "scale_out_window", "scale_in_window", "latency_p95_breach"} {
		if _, ok := names[want]; !ok {
			t.Errorf("condition %q missing", want)
		}
	}
	cpu := names["cpu_high"]
	if !cpu.Met || cpu.Value == nil || *cpu.Value != 80 || *cpu.Threshold != 70 {
		t.Fatalf("cpu_high = %+v", cpu)
	}
	if lat := names["latency_p95_breach"]; lat.Value != nil {
		t.Fatal("latency value must be null when not evaluable")
	}
}

// TestDecisionInvariantsGrid sweeps a grid of inputs and checks invariants
// that must hold for any input.
func TestDecisionInvariantsGrid(t *testing.T) {
	cfg := RealisticConfig()
	cpus := []float64{0, 10, 27.5, 40, 54.9, 55, 60, 69.9, 70, 85, 100}
	lats := []float64{0, 100, 350, 351, 500, 501, 900}
	errs := []float64{0, 1, 3}
	for n := 1; n <= 5; n++ {
		for _, cpu := range cpus {
			for _, lat := range lats {
				for _, e := range errs {
					h := newHarness(cfg)
					spec := obsSpec{cpu: cpu, requests: 100, latencyMs: lat, target5xx: e}
					for range 6 {
						d := h.load(fleet(n), spec)
						kind, ok := d.Reason.Decision()
						if !ok || kind != d.Decision {
							t.Fatalf("n=%d spec=%+v: reason %s inconsistent with %s", n, spec, d.Reason, d.Decision)
						}
						switch d.Decision {
						case IncreaseCapacity:
							if d.Action.TargetDesired <= n || d.Action.TargetDesired > cfg.MaxInstances || d.Action.Step > cfg.MaxScaleOutStep {
								t.Fatalf("n=%d spec=%+v: bad increase %+v", n, spec, d.Action)
							}
						case ReduceCapacity:
							if d.Action.TargetDesired != n-1 || d.Action.TargetDesired < cfg.MinInstances {
								t.Fatalf("n=%d spec=%+v: bad reduce %+v", n, spec, d.Action)
							}
						}
						if d.Decision == ReduceCapacity && cpu*float64(n)/float64(n-1) > cfg.ScaleInProjectedCPU+epsilon {
							t.Fatalf("n=%d cpu=%v: reduced despite projection", n, cpu)
						}
					}
				}
			}
		}
	}
}
