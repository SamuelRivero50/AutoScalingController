package core

import (
	"slices"
	"testing"
)

var (
	hot  = obsSpec{cpu: 80}
	warm = obsSpec{cpu: 50}
	cool = obsSpec{cpu: 20}
)

func TestScaleOutWindow(t *testing.T) {
	tests := []struct {
		name   string
		cycles []obsSpec
		want   []ReasonCode
	}{
		{"single breaching cycle does not scale out", []obsSpec{warm, hot, warm},
			[]ReasonCode{ReasonMaintainStable, ReasonMaintainWindowPending, ReasonMaintainStable}},
		{"two consecutive breaching cycles scale out", []obsSpec{hot, hot},
			[]ReasonCode{ReasonMaintainWindowPending, ReasonIncreaseCPUHigh}},
		{"two of three non-consecutive scale out", []obsSpec{hot, warm, hot},
			[]ReasonCode{ReasonMaintainWindowPending, ReasonMaintainStable, ReasonIncreaseCPUHigh}},
		{"breaches four cycles apart do not", []obsSpec{hot, warm, warm, hot},
			[]ReasonCode{ReasonMaintainWindowPending, ReasonMaintainStable, ReasonMaintainStable, ReasonMaintainWindowPending}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(RealisticConfig())
			capacity := fleet(2)
			for i, spec := range tt.cycles {
				d := h.load(capacity, spec)
				if d.Reason != tt.want[i] {
					t.Fatalf("cycle %d reason = %s, want %s", i+1, d.Reason, tt.want[i])
				}
			}
		})
	}
}

func TestCapacityChangeResetsBothWindows(t *testing.T) {
	cfg := RealisticConfig()
	h := newHarness(cfg)

	h.load(fleet(2), hot)
	for range 4 {
		h.load(fleet(3), cool)
	}
	if n := len(h.mem.Entries); n != 4 {
		t.Fatalf("entries after reset = %d, want 4 (the pre-reset cycle must be dropped)", n)
	}
	if w := h.mem.ScaleOut(cfg); len(w.Breaching) != 0 {
		t.Fatalf("scale-out window kept %v across a capacity change", w.Breaching)
	}

	// Scale-in window also resets: 4 comfortable cycles, then capacity changes.
	d := h.load(fleet(4), cool)
	requireReason(t, d, ReasonMaintainWindowPending)
	if w := h.mem.ScaleIn(cfg); len(w.Comfortable) != 1 {
		t.Fatalf("scale-in comfortable = %v after reset, want exactly the current cycle", w.Comfortable)
	}

	// A reset scale-out window needs two new breaches.
	requireReason(t, h.load(fleet(3), hot), ReasonMaintainWindowPending)
	requireReason(t, h.load(fleet(3), hot), ReasonIncreaseCPUHigh)
}

func TestDesiredChangeAloneResetsWindows(t *testing.T) {
	cfg := RealisticConfig()
	h := newHarness(cfg)
	h.load(fleet(3), hot)
	c := fleet(3)
	c.Desired = 2 // scale-in requested, not yet draining
	h.load(c, hot)
	if len(h.mem.ScaleOut(cfg).Breaching) != 1 {
		t.Fatalf("desired change must reset the window, entries = %+v", h.mem.Entries)
	}
}

func TestScaleOutInFlightCyclesAreNotCounted(t *testing.T) {
	cfg := RealisticConfig()
	h := newHarness(cfg)
	requireReason(t, h.load(fleet(2), hot), ReasonMaintainWindowPending)

	pending := fleet(2, InstancePending)
	for range 3 {
		requireReason(t, h.load(pending, hot), ReasonMaintainPendingCapacity)
	}
	if n := len(h.mem.Entries); n != 0 {
		t.Fatalf("pending cycles counted: %d entries, want 0 (reset by desired change, nothing added)", n)
	}

	// Desired raised but instance not launched yet is also in flight.
	notLaunched := fleet(2)
	notLaunched.Desired = 3
	requireReason(t, h.load(notLaunched, hot), ReasonMaintainPendingCapacity)
}

func TestNoNewDataCycleIsNotCounted(t *testing.T) {
	cfg := RealisticConfig()
	h := newHarness(cfg)
	capacity := fleet(2)
	h.load(capacity, hot)

	// Same observation again: CPU datapoint already consumed.
	obs := observe(h.now, cfg, capacity, hot)
	d := h.step(capacity, obs)
	if len(h.mem.Entries) != 1 {
		t.Fatalf("NO_NEW_DATA counted: %d entries, want 1", len(h.mem.Entries))
	}
	requireReason(t, d, ReasonMaintainWindowPending)
	if d.Blind {
		t.Fatal("NO_NEW_DATA must not be a blind cycle")
	}
	requireReason(t, h.load(capacity, hot), ReasonIncreaseCPUHigh)
}

func TestScaleInWindowRealisticFiveOfFive(t *testing.T) {
	h := newHarness(RealisticConfig())
	capacity := fleet(3)
	for i := range 4 {
		d := h.load(capacity, cool)
		if d.Decision == ReduceCapacity {
			t.Fatalf("scaled in after %d comfortable cycles, want 5", i+1)
		}
		requireReason(t, d, ReasonMaintainWindowPending)
	}
	d := h.load(capacity, cool)
	requireReason(t, d, ReasonReduceProjectionOK)
	if d.Action.TargetDesired != 2 || d.Action.Step != -1 || d.Action.Type != ActionSetDesiredCapacity {
		t.Fatalf("action = %+v, want absolute desired 2 (step -1)", d.Action)
	}
}

func TestScaleInWindowDemoThreeOfThree(t *testing.T) {
	h := newHarness(DemoConfig())
	capacity := fleet(3)
	requireReason(t, h.load(capacity, cool), ReasonMaintainWindowPending)
	requireReason(t, h.load(capacity, cool), ReasonMaintainWindowPending)
	requireReason(t, h.load(capacity, cool), ReasonReduceProjectionOK)
}

func TestScaleInWindowNeedsEveryCycleComfortable(t *testing.T) {
	h := newHarness(RealisticConfig())
	capacity := fleet(3)
	for range 4 {
		h.load(capacity, cool)
	}
	requireReason(t, h.load(capacity, warm), ReasonMaintainStable)
	for range 4 {
		requireReason(t, h.load(capacity, cool), ReasonMaintainWindowPending)
	}
	requireReason(t, h.load(capacity, cool), ReasonReduceProjectionOK)
}

func TestMissingCPUInWindowBlocksScaleIn(t *testing.T) {
	cfg := RealisticConfig()
	h := newHarness(cfg)
	capacity := fleet(3)
	for range 4 {
		h.load(capacity, cool)
	}

	// One cycle without any CPU datapoint.
	h.now = h.now.Add(cfg.EvaluationInterval)
	requireReason(t, h.step(capacity, Observation{Now: h.now}), ReasonMaintainBlind)

	// Four comfortable cycles: the bad cycle is still inside the last 5.
	for i := range 4 {
		d := h.load(capacity, cool)
		if d.Decision == ReduceCapacity {
			t.Fatalf("scaled in at comfortable cycle %d with a MISSING CPU cycle in the window", i+1)
		}
		requireReason(t, d, ReasonMaintainScaleInBlocked)
	}
	// Fifth comfortable cycle pushes the bad one out of the window.
	requireReason(t, h.load(capacity, cool), ReasonReduceProjectionOK)
}

func TestScaleOutAllowedDuringDrain(t *testing.T) {
	h := newHarness(RealisticConfig())
	capacity := fleet(2, InstanceDraining)
	requireReason(t, h.load(capacity, hot), ReasonMaintainWindowPending)
	d := h.load(capacity, hot)
	requireReason(t, d, ReasonIncreaseCPUHigh)
	if d.Action.TargetDesired != 3 {
		t.Fatalf("target = %d, want 3", d.Action.TargetDesired)
	}
}

func TestScaleInBlockedDuringDrain(t *testing.T) {
	h := newHarness(DemoConfig())
	capacity := fleet(3, InstanceDraining)
	requireReason(t, h.load(capacity, cool), ReasonMaintainPendingCapacity)
	requireReason(t, h.load(capacity, cool), ReasonMaintainPendingCapacity)
	requireReason(t, h.load(capacity, cool), ReasonMaintainPendingCapacity)
}

func TestWindowViewsForLog(t *testing.T) {
	cfg := RealisticConfig()
	h := newHarness(cfg)
	capacity := fleet(2)
	h.load(capacity, hot)  // cycle 1
	h.load(capacity, warm) // cycle 2
	h.load(capacity, cool) // cycle 3 (projection 40 on N=2 -> comfortable)
	out := h.mem.ScaleOut(cfg)
	if out.M != 2 || out.N != 3 || !slices.Equal(out.Breaching, []int64{1}) {
		t.Fatalf("scale-out view = %+v", out)
	}
	in := h.mem.ScaleIn(cfg)
	if in.N != 5 || !slices.Equal(in.Comfortable, []int64{3}) || in.Satisfied {
		t.Fatalf("scale-in view = %+v", in)
	}
	if len(h.mem.Entries) > cfg.windowCapacity() {
		t.Fatalf("memory keeps %d entries, max %d", len(h.mem.Entries), cfg.windowCapacity())
	}
}

func TestMemoryIsBounded(t *testing.T) {
	cfg := RealisticConfig()
	h := newHarness(cfg)
	for range 50 {
		h.load(fleet(2), warm)
	}
	if n := len(h.mem.Entries); n != cfg.windowCapacity() {
		t.Fatalf("entries = %d, want %d", n, cfg.windowCapacity())
	}
}

func TestStalePeriodGuardDropsFirstPostScaleOutCycle(t *testing.T) {
	// After scale-out completes (ScaleOutInFlight just became false), the
	// first CPU datapoint whose period_start precedes the scale-out must
	// be discarded. Subsequent datapoints should accumulate normally.
	cfg := RealisticConfig()
	h := newHarness(cfg)

	// Simulate two overloaded cycles that drive a scale-out decision.
	requireReason(t, h.load(fleet(2), hot), ReasonMaintainWindowPending)
	requireReason(t, h.load(fleet(2), hot), ReasonIncreaseCPUHigh)

	// Scale-out in-flight: desired raised, new instance pending.
	// Window resets and pending cycles are not counted.
	for range 2 {
		requireReason(t, h.load(fleet(2, InstancePending), hot), ReasonMaintainPendingCapacity)
	}
	if len(h.mem.Entries) != 0 {
		t.Fatalf("pending cycles counted: %d entries, want 0", len(h.mem.Entries))
	}

	// New instance arrives; ScaleOutInFlight → false.
	// The harness constructs the observation with PeriodStart = h.now -
	// AggregationPeriod - MetricLag = h.now - 120s. Because LastCapacityChangedAt
	// was set at the cycle when in_service grew (from 2 to 3), and the
	// PeriodStart is before that moment, this cycle must be discarded.
	c1 := fleet(3) // all in service, no pending
	d1 := h.load(c1, hot)
	if d1.Decision == IncreaseCapacity {
		t.Fatal("stale-period cycle must not trigger another scale-out")
	}
	if len(h.mem.Entries) != 0 {
		t.Fatalf("stale cycle counted: entries = %d, want 0", len(h.mem.Entries))
	}

	// The NEXT cycle has a fresh datapoint (period_start >= scale-out time).
	// It should count normally even if CPU is still elevated.
	h.load(c1, warm)
	if len(h.mem.Entries) != 1 {
		t.Fatalf("fresh post-scale-out cycle not counted: entries = %d, want 1", len(h.mem.Entries))
	}
}

func TestStalePeriodGuardDoesNotFireOnScaleIn(t *testing.T) {
	// The guard must NOT activate when in_service decreases (scale-in direction).
	// Only scale-outs (in_service increasing) can produce stale CPU readings.
	cfg := RealisticConfig()
	h := newHarness(cfg)

	// Fill two cycles at fleet(4); no scale-out ever happened.
	c4 := fleet(4)
	h.load(c4, cool)
	h.load(c4, cool)

	// Now in_service drops to 3 (simulating a completed scale-in).
	// The window resets (in_service change), then the first post-change cycle
	// should be counted normally — the guard must not block it.
	c3 := fleet(3)
	h.load(c3, cool) // resets window; adds 1 entry
	if len(h.mem.Entries) != 1 {
		t.Fatalf("first post-scale-in cycle: entries = %d, want 1 (reset then counted)", len(h.mem.Entries))
	}

	// Second cycle at same in_service: should also count normally.
	h.load(c3, cool)
	if len(h.mem.Entries) != 2 {
		t.Fatalf("guard fired on second post-scale-in cycle: entries = %d, want 2", len(h.mem.Entries))
	}

	// Third and further cycles with hot load: must accumulate in the window.
	h.load(c3, hot)
	if len(h.mem.Entries) != 3 {
		t.Fatalf("hot post-scale-in cycle not counted: entries = %d, want 3", len(h.mem.Entries))
	}
}
