package app

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

// TestNoSystemClock enforces that the application layer reads time only
// through the Clock port (issue #13).
func TestNoSystemClock(t *testing.T) {
	forbidden := map[string]bool{
		"time.Now": true, "time.Since": true, "time.Until": true,
		"time.Sleep": true, "time.After": true, "time.Tick": true,
		"time.NewTimer": true, "time.NewTicker": true,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && forbidden[pkg.Name+"."+sel.Sel.Name] {
					t.Errorf("%s reads the system clock: %s.%s", fset.Position(sel.Pos()), pkg.Name, sel.Sel.Name)
				}
			}
			return true
		})
	}
}

func TestNew(t *testing.T) {
	r := newRig(t)
	deps := Deps{Metrics: nil, Provisioner: r.asg, State: r.store, Log: r.log, Clock: r.clock}
	if _, err := New(RealisticConfig(), core.ModeSim, "run", deps); err == nil {
		t.Fatal("missing metrics dependency must fail")
	}
	bad := RealisticConfig()
	bad.CycleBudget = 0
	deps.Metrics = blockingMetrics{}
	if _, err := New(bad, core.ModeSim, "run", deps); err == nil {
		t.Fatal("invalid config must fail")
	}
	if _, err := New(RealisticConfig(), core.ModeSim, "", deps); err == nil {
		t.Fatal("empty run id must fail")
	}
	info := r.ctrl.RunInfo()
	if info.ConfigHash != RealisticConfig().Hash() || info.Mode != core.ModeSim || info.Profile != core.ProfileRealistic {
		t.Fatalf("run info = %+v", info)
	}
}

func TestController_StartRebuildsState(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(r *rig)
		reason string
	}{
		{"missing state", func(*rig) {}, "not_found"},
		{"corrupt state", func(r *rig) {
			_ = r.store.Save(context.Background(), ports.State{NextCycle: 7})
			r.store.CorruptNextLoad()
		}, "corrupt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			tt.setup(r)
			r.start(t)
			rebuilt := r.log.eventsOf(ports.EventStateRebuilt)
			if len(rebuilt) != 1 || rebuilt[0].Details["reason"] != tt.reason {
				t.Fatalf("STATE_REBUILT events = %+v, want one with reason %s", rebuilt, tt.reason)
			}
			if st := r.ctrl.State(); st.NextCycle != 1 || len(st.LastInstances) != 2 {
				t.Fatalf("rebuilt state = %+v, want cycle 1 and 2 instances from the provisioner", st)
			}
			if len(r.log.eventsOf(ports.EventControllerStarted)) != 1 {
				t.Fatal("CONTROLLER_STARTED not logged")
			}
		})
	}
}

func TestController_StartRestoresState(t *testing.T) {
	r := newRig(t)
	saved := ports.State{NextCycle: 42, Breaker: core.Breaker{State: core.BreakerOpen, ConsecutiveFailures: 3, OpenedAt: t0}}
	if err := r.store.Save(t.Context(), saved); err != nil {
		t.Fatal(err)
	}
	r.start(t)
	if len(r.log.eventsOf(ports.EventStateRebuilt)) != 0 {
		t.Fatal("a valid state must not be rebuilt")
	}
	r.cycles(t, 1)
	if rec := r.log.last(); rec.CycleID != 42 || rec.Breaker.State != core.BreakerOpen {
		t.Fatalf("cycle %d breaker %s, want 42 OPEN (restored)", rec.CycleID, rec.Breaker.State)
	}
}

func TestController_OneRecordPerCycleAndStatePersisted(t *testing.T) {
	r := newRig(t)
	r.start(t)
	r.cycles(t, 5)
	if n := len(r.log.cycles); n != 5 {
		t.Fatalf("cycle records = %d, want 5", n)
	}
	for i, rec := range r.log.cycles {
		if rec.CycleID != int64(i+1) || rec.Run.ConfigHash == "" {
			t.Fatalf("record %d = id %d hash %q", i, rec.CycleID, rec.Run.ConfigHash)
		}
		if rec.Action.Type != core.ActionNone || rec.Action.Status != ports.ActionOK {
			t.Fatalf("stable cycle action = %+v, want NONE/OK", rec.Action)
		}
	}
	saved, err := r.store.Load(t.Context())
	if err != nil || saved.NextCycle != 6 {
		t.Fatalf("persisted state = %+v, %v; want next cycle 6", saved, err)
	}
}

func TestController_ScaleOutSetsAbsoluteDesired(t *testing.T) {
	r := newRig(t, withDesired(1), withLoad(0.9))
	r.start(t)
	r.cycles(t, 4)
	var increase *ports.CycleRecord
	for i := range r.log.cycles {
		if r.log.cycles[i].Decision.Decision == core.IncreaseCapacity {
			increase = &r.log.cycles[i]
			break
		}
	}
	if increase == nil {
		t.Fatal("no INCREASE decision at CPU 92%")
	}
	a := increase.Action
	if a.Type != core.ActionSetDesiredCapacity || a.Status != ports.ActionOK || a.TargetDesired == nil || *a.TargetDesired != 2 || a.RequestID == "" {
		t.Fatalf("action = %+v, want SET_DESIRED_CAPACITY 2 OK", a)
	}
	snap, _ := r.asg.DescribeCapacity(t.Context())
	if snap.Desired != 2 {
		t.Fatalf("group desired = %d, want 2", snap.Desired)
	}
	changes := r.log.eventsOf(ports.EventInstanceStateChange)
	if len(changes) == 0 || changes[0].Details["to"] != string(core.InstancePending) {
		t.Fatalf("INSTANCE_STATE_CHANGE events = %+v, want the new PENDING instance", changes)
	}
}

func TestController_BudgetExhaustion(t *testing.T) {
	cfg := RealisticConfig()
	cfg.CycleBudget = 50 * time.Millisecond
	r := newRig(t, withConfig(cfg), withMetrics(blockingMetrics{}))
	r.start(t)
	r.cycles(t, 1)

	rec := r.log.last()
	if rec.Signals.CPU.Quality != core.QualityMissing || rec.Signals.RequestCount.Quality != core.QualityMissing {
		t.Fatalf("unread signals = CPU %s RequestCount %s, want MISSING", rec.Signals.CPU.Quality, rec.Signals.RequestCount.Quality)
	}
	if rec.Decision.Reason != core.ReasonMaintainBlind {
		t.Fatalf("reason = %s, want MAINTAIN_BLIND", rec.Decision.Reason)
	}
	budget := false
	for _, e := range r.log.eventsOf(ports.EventFetchFailure) {
		if e.Details["source"] == "cycle_budget" {
			budget = true
		}
	}
	if !budget {
		t.Fatal("budget exhaustion not reported as FETCH_FAILURE")
	}
}

func TestController_BudgetExhaustionSkipsIntendedAction(t *testing.T) {
	cfg := RealisticConfig()
	cfg.CycleBudget = 50 * time.Millisecond
	r := newRig(t, withConfig(cfg), withMetrics(blockingMetrics{}))
	r.asg.StickNextLaunches(1)
	if _, err := r.asg.SetDesiredCapacity(t.Context(), 3); err != nil {
		t.Fatal(err)
	}
	r.start(t)
	r.clock.Advance(cfg.PendingTimeout + time.Minute)
	r.cycles(t, 1)

	a := r.log.last().Action
	if a.Type != core.ActionTerminateInstance || a.Status != ports.ActionSkipped || a.SkipReason != ports.SkipBudgetExhausted {
		t.Fatalf("action = %+v, want TERMINATE_INSTANCE SKIPPED BUDGET_EXHAUSTED", a)
	}
	if snap, _ := r.asg.DescribeCapacity(t.Context()); snap.Pending() != 1 {
		t.Fatal("no action may be executed once the budget is exhausted")
	}
}

func TestController_StuckPendingTerminated(t *testing.T) {
	r := newRig(t)
	r.asg.StickNextLaunches(1)
	if _, err := r.asg.SetDesiredCapacity(t.Context(), 3); err != nil {
		t.Fatal(err)
	}
	r.start(t)
	// Launched at t0: cycles at 0..6 min are within the 360s timeout
	// (exactly 360s is not yet beyond it).
	r.cycles(t, 7)
	for _, rec := range r.log.cycles {
		if rec.Action.Type == core.ActionTerminateInstance {
			t.Fatalf("terminated at cycle %d, before the pending timeout", rec.CycleID)
		}
	}
	r.cycles(t, 1)
	rec := r.log.last()
	if rec.Action.Type != core.ActionTerminateInstance || rec.Action.Status != ports.ActionOK {
		t.Fatalf("action = %+v, want TERMINATE_INSTANCE OK", rec.Action)
	}
	if rec.Breaker.ConsecutiveFailures != 1 {
		t.Fatalf("breaker failures = %d, want 1 (stuck termination counts once)", rec.Breaker.ConsecutiveFailures)
	}
	snap, _ := r.asg.DescribeCapacity(t.Context())
	if snap.Desired != 2 || snap.Pending() != 0 {
		t.Fatalf("after termination desired=%d pending=%d, want 2 and 0", snap.Desired, snap.Pending())
	}
	r.cycles(t, 1)
	gone := false
	for _, e := range r.log.eventsOf(ports.EventInstanceStateChange) {
		if e.Details["to"] == string(core.InstanceTerminated) {
			gone = true
		}
	}
	if !gone {
		t.Fatal("terminated instance not reported as TERMINATED")
	}
}

func TestController_APIErrorIsNotABreakerFailure(t *testing.T) {
	r := newRig(t, withDesired(1), withLoad(0.9))
	r.asg.FailNextAPICalls(1)
	r.start(t)
	r.cycles(t, 3)
	var sawError bool
	for _, rec := range r.log.cycles {
		if rec.Action.Status == ports.ActionError {
			sawError = true
			if rec.Action.Error == "" {
				t.Fatal("ERROR action without an error message")
			}
		}
		if rec.Breaker.ConsecutiveFailures != 0 {
			t.Fatalf("cycle %d breaker failures = %d, want 0", rec.CycleID, rec.Breaker.ConsecutiveFailures)
		}
	}
	if !sawError {
		t.Fatal("injected API failure did not produce an ERROR action")
	}
}

func TestController_FailedLaunchesCountOncePerCycle(t *testing.T) {
	r := newRig(t, withDesired(1), withLoad(1.2))
	r.asg.FailLaunchesUntil(t0.Add(time.Hour))
	r.start(t)
	r.cycles(t, 3) // cycle 2 scales out; the group retries every 30s
	failures := r.log.last().Breaker.ConsecutiveFailures
	if failures != 1 {
		t.Fatalf("failures after one cycle of retries = %d, want 1 (max one per cycle)", failures)
	}
	r.cycles(t, 2)
	rec := r.log.last()
	if rec.Breaker.State != core.BreakerOpen || rec.Breaker.ConsecutiveFailures != 3 {
		t.Fatalf("breaker = %+v, want OPEN after 3 failing cycles", rec.Breaker)
	}
	if opened := r.log.eventsOf(ports.EventBreakerStateChange); len(opened) != 1 || opened[0].Details["to"] != string(core.BreakerOpen) {
		t.Fatalf("BREAKER_STATE_CHANGE events = %+v", opened)
	}
	if rec.Action.Type != core.ActionSetDesiredCapacity || *rec.Action.TargetDesired != 1 {
		t.Fatalf("action on opening = %+v, want desired reset to the healthy count 1", rec.Action)
	}
}

func TestController_LoggerErrorIsReturned(t *testing.T) {
	r := newRig(t)
	r.start(t)
	r.log.fail = errLogDown
	if err := r.ctrl.Cycle(t.Context()); !errors.Is(err, errLogDown) {
		t.Fatalf("Cycle = %v, want the logger error", err)
	}
	if r.ctrl.State().NextCycle != 2 {
		t.Fatal("the cycle must still advance when logging fails")
	}
}

func TestController_Run(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r.log.onCycle = func(n int) {
		if n == 3 {
			cancel()
		}
	}
	if err := r.ctrl.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(r.log.cycles); n != 3 {
		t.Fatalf("cycles = %d, want 3", n)
	}
	if len(r.log.eventsOf(ports.EventControllerStopped)) != 1 {
		t.Fatal("CONTROLLER_STOPPED not logged after cancellation")
	}
	if got := r.clock.Now(); !got.Equal(t0.Add(2 * time.Minute)) {
		t.Fatalf("clock = %v, want two intervals of sleep", got)
	}
}
