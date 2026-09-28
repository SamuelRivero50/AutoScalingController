package simulator

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/fakeasg"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/mockmetrics"
	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

// Load is in instance units: 1.0 saturates one instance. Times in the
// profiles are the elapsed time at the start of the metric period; with the
// realistic profile a period is read two cycles later (60s period + 60s
// metric lag).

func constant(v float64) func(time.Duration) float64 {
	return func(time.Duration) float64 { return v }
}

// phases returns a piecewise-constant load: phase i applies from its start
// until the next phase begins.
func phases(steps ...phase) func(time.Duration) float64 {
	return func(e time.Duration) float64 {
		v := steps[0].load
		for _, s := range steps {
			if e >= s.from {
				v = s.load
			}
		}
		return v
	}
}

type phase struct {
	from time.Duration
	load float64
}

func between(from, to time.Duration) func(time.Duration) bool {
	return func(e time.Duration) bool { return e >= from && e < to }
}

// Scenarios returns S1-S10 in order.
func Scenarios() []Scenario {
	return []Scenario{s1(), s2(), s3(), s4(), s5(), s6(), s7(), s8(), s9(), s10()}
}

// ByID returns the scenario with the given ID.
func ByID(id string) (Scenario, bool) {
	for _, sc := range Scenarios() {
		if sc.ID == id {
			return sc, true
		}
	}
	return Scenario{}, false
}

func s1() Scenario {
	return Scenario{
		ID: "S1", Name: "stable load",
		Criterion: "MAINTAIN_CAPACITY / MAINTAIN_STABLE for the entire run; capacity never changes",
		Duration:  30 * time.Minute, Initial: 2,
		Metrics: mockmetrics.Profile{Load: constant(0.9), NoiseStdDev: 2},
		Check: func(r Result) error {
			for _, c := range r.Cycles {
				if c.Decision.Reason != core.ReasonMaintainStable {
					return fmt.Errorf("cycle %d: %s, want MAINTAIN_STABLE", c.CycleID, c.Decision.Reason)
				}
				if c.Capacity.Desired != 2 || c.Action.Type != core.ActionNone {
					return fmt.Errorf("cycle %d: desired %d action %s, want 2 and NONE", c.CycleID, c.Capacity.Desired, c.Action.Type)
				}
			}
			return nil
		},
	}
}

func s2() Scenario {
	const load = 2.0
	target := int(math.Ceil(load / 0.55)) // 4
	return Scenario{
		ID: "S2", Name: "sustained increase",
		Criterion: "reaches ceil(load/0.55) instances within the expected reaction time; increases carry INCREASE_CPU_HIGH",
		Duration:  45 * time.Minute, Initial: 1,
		Metrics: mockmetrics.Profile{Load: phases(phase{0, 0.5}, phase{5 * time.Minute, load}), NoiseStdDev: 2},
		Check: func(r Result) error {
			reached := time.Duration(-1)
			for _, c := range r.Cycles {
				if c.Decision.Decision == core.IncreaseCapacity && c.Decision.Reason != core.ReasonIncreaseCPUHigh {
					return fmt.Errorf("cycle %d: increase with %s, want INCREASE_CPU_HIGH", c.CycleID, c.Decision.Reason)
				}
				if c.Capacity.Desired > target {
					return fmt.Errorf("cycle %d: desired %d overshoots %d", c.CycleID, c.Capacity.Desired, target)
				}
				if reached < 0 && c.Capacity.InService() == target {
					reached = r.Elapsed(c.TS)
				}
			}
			// Load rises at 5 min; two 2-of-3 windows plus two warmups.
			if reached < 0 || reached > 25*time.Minute {
				return fmt.Errorf("reached %d in-service instances at %v, want by 25m", target, reached)
			}
			return nil
		},
	}
}

func s3() Scenario {
	spike := func(e time.Duration) float64 {
		if between(5*time.Minute, 6*time.Minute)(e) || between(15*time.Minute, 17*time.Minute)(e) {
			return 1.6 // CPU ~82% on 2 instances
		}
		return 0.9
	}
	return Scenario{
		ID: "S3", Name: "transient vs genuine spike",
		Criterion: "a single breaching cycle does not scale out (2-of-3 window); two consecutive breaching cycles do",
		Duration:  30 * time.Minute, Initial: 2,
		Metrics: mockmetrics.Profile{Load: spike, NoiseStdDev: 1},
		Check: func(r Result) error {
			var sawTransient bool
			increaseAt := time.Duration(-1)
			for _, c := range r.Cycles {
				e := r.Elapsed(c.TS)
				if c.Decision.Reason == core.ReasonMaintainWindowPending && e < 10*time.Minute {
					sawTransient = true
				}
				if c.Decision.Decision == core.IncreaseCapacity {
					if e < 17*time.Minute {
						return fmt.Errorf("scaled out at %v, before the genuine spike was confirmed", e)
					}
					if increaseAt < 0 {
						increaseAt = e
					}
				}
			}
			if !sawTransient {
				return errors.New("the transient spike was never observed as an overloaded cycle")
			}
			if increaseAt < 0 || increaseAt > 20*time.Minute {
				return fmt.Errorf("genuine spike scaled out at %v, want within 20m", increaseAt)
			}
			return nil
		},
	}
}

func s4() Scenario {
	return Scenario{
		ID: "S4", Name: "sustained decrease",
		Criterion: "scales in one instance at a time until the projection bound blocks further reduction or min=1",
		Duration:  60 * time.Minute, Initial: 5,
		Metrics: mockmetrics.Profile{Load: phases(phase{0, 2.4}, phase{5 * time.Minute, 0.8}), NoiseStdDev: 2},
		Check: func(r Result) error {
			reduces := 0
			for _, c := range r.Cycles {
				switch c.Decision.Decision {
				case core.IncreaseCapacity:
					return fmt.Errorf("cycle %d: unexpected increase", c.CycleID)
				case core.ReduceCapacity:
					reduces++
					if c.Decision.Action.Step != -1 || c.Decision.Action.TargetDesired != c.Capacity.InService()-1 {
						return fmt.Errorf("cycle %d: reduce %+v, want exactly -1", c.CycleID, c.Decision.Action)
					}
				}
			}
			last := r.Cycles[len(r.Cycles)-1].Capacity
			// 0.8 load: 2 instances project to 84% on 1 -> blocked at 2.
			if last.Desired != 2 || reduces != 3 {
				return fmt.Errorf("final desired %d after %d reductions, want 2 after 3", last.Desired, reduces)
			}
			return nil
		},
	}
}

func s5() Scenario {
	return Scenario{
		ID: "S5", Name: "edge-noise oscillation",
		Criterion: "no INCREASE_CAPACITY within 10 minutes after a REDUCE_CAPACITY",
		Duration:  90 * time.Minute, Initial: 3,
		Metrics: mockmetrics.Profile{Load: constant(1.0), NoiseStdDev: 10},
		Check: func(r Result) error {
			var reduces []time.Duration
			for _, c := range r.Cycles {
				e := r.Elapsed(c.TS)
				switch c.Decision.Decision {
				case core.ReduceCapacity:
					reduces = append(reduces, e)
				case core.IncreaseCapacity:
					for _, at := range reduces {
						if e > at && e <= at+10*time.Minute {
							return fmt.Errorf("increase at %v only %v after a reduce", e, e-at)
						}
					}
				}
			}
			if len(reduces) == 0 {
				return errors.New("no REDUCE_CAPACITY occurred; the scenario did not exercise the dead-band")
			}
			return nil
		},
	}
}

func s6() Scenario {
	return Scenario{
		ID: "S6", Name: "launch failures and circuit breaker",
		Criterion: "after 3 consecutive launch failures the breaker opens; no scale-out until the ~10-minute cool-off elapses; exactly one probe follows",
		Duration:  40 * time.Minute, Initial: 1,
		Metrics: mockmetrics.Profile{Load: constant(1.2), NoiseStdDev: 2},
		Setup: func(start time.Time, asg *fakeasg.ASG) {
			asg.FailLaunchesUntil(start.Add(9 * time.Minute))
		},
		Check: func(r Result) error {
			changes := r.EventsOf(ports.EventBreakerStateChange)
			if len(changes) == 0 || changes[0].Details["to"] != string(core.BreakerOpen) || changes[0].Details["consecutive_failures"] != 3 {
				return fmt.Errorf("first breaker change = %+v, want OPEN with 3 failures", changes)
			}
			openedAt := changes[0].TS
			probes, skipped := 0, 0
			for _, c := range r.Cycles {
				a := c.Action
				raises := a.Type == core.ActionSetDesiredCapacity && a.Status == ports.ActionOK &&
					a.TargetDesired != nil && *a.TargetDesired > c.Capacity.Desired
				if raises && !c.TS.Before(openedAt) && c.TS.Before(openedAt.Add(r.Config.Breaker.CoolOff)) {
					return fmt.Errorf("cycle %d raised capacity %v after the breaker opened", c.CycleID, c.TS.Sub(openedAt))
				}
				if a.Status == ports.ActionSkipped && a.SkipReason == ports.SkipBreakerOpen {
					skipped++
				}
				if raises && c.Breaker.State == core.BreakerHalfOpen {
					probes++
				}
			}
			if skipped == 0 {
				return errors.New("no INCREASE was skipped while the breaker was open")
			}
			if probes != 1 {
				return fmt.Errorf("probe attempts = %d, want exactly 1", probes)
			}
			if final := r.Cycles[len(r.Cycles)-1].Breaker.State; final != core.BreakerClosed {
				return fmt.Errorf("final breaker %s, want CLOSED after a successful probe", final)
			}
			return nil
		},
	}
}

func s7() Scenario {
	return Scenario{
		ID: "S7", Name: "stuck-pending instance",
		Criterion: "an instance that never becomes healthy within the pending timeout is terminated and capacity decremented; the next cycle re-evaluates cleanly",
		Duration:  30 * time.Minute, Initial: 1,
		Metrics: mockmetrics.Profile{Load: constant(1.2), NoiseStdDev: 2},
		Setup: func(_ time.Time, asg *fakeasg.ASG) {
			asg.StickNextLaunches(1)
		},
		Check: func(r Result) error {
			launchedAt := time.Duration(-1)
			for i, c := range r.Cycles {
				e := r.Elapsed(c.TS)
				if launchedAt < 0 && c.Decision.Decision == core.IncreaseCapacity && c.Action.Status == ports.ActionOK {
					launchedAt = e
				}
				if c.Action.Type != core.ActionTerminateInstance {
					continue
				}
				if c.Action.Status != ports.ActionOK {
					return fmt.Errorf("termination failed: %+v", c.Action)
				}
				waited := e - launchedAt
				timeout := r.Config.PendingTimeout
				if waited <= timeout || waited > timeout+2*r.Config.Policy.EvaluationInterval {
					return fmt.Errorf("terminated %v after launch, want just beyond %v", waited, timeout)
				}
				if i+1 >= len(r.Cycles) {
					return errors.New("no cycle after the termination")
				}
				next := r.Cycles[i+1].Capacity
				if next.Pending() != 0 || next.Desired != next.InService() {
					return fmt.Errorf("next cycle desired=%d in-service=%d pending=%d, want a clean state", next.Desired, next.InService(), next.Pending())
				}
				if r.Cycles[i+1].Breaker.State == core.BreakerOpen {
					return errors.New("a single stuck instance must not open the breaker")
				}
				return nil
			}
			return errors.New("the stuck instance was never terminated")
		},
	}
}

func s8() Scenario {
	outage := between(3*time.Minute, 11*time.Minute) // read by cycles at 5..12 min
	return Scenario{
		ID: "S8", Name: "metrics outage (blindness)",
		Criterion: "all signals MISSING -> MAINTAIN_BLIND; alert after the consecutive-blind threshold; no capacity change during blindness",
		Duration:  20 * time.Minute, Initial: 2,
		Metrics: mockmetrics.Profile{Load: constant(0.9), NoiseStdDev: 2, Outage: outage},
		Check: func(r Result) error {
			blind := 0
			for _, c := range r.Cycles {
				if c.Capacity.Desired != 2 || c.Action.Type != core.ActionNone {
					return fmt.Errorf("cycle %d changed capacity during the run", c.CycleID)
				}
				if c.Decision.Reason != core.ReasonMaintainBlind {
					continue
				}
				blind++
				for _, s := range c.Signals.List() {
					if s.Quality != core.QualityMissing {
						return fmt.Errorf("cycle %d: %s is %s, want MISSING", c.CycleID, s.Name, s.Quality)
					}
				}
			}
			if blind != 8 {
				return fmt.Errorf("blind cycles = %d, want 8", blind)
			}
			alerts := r.EventsOf(ports.EventBlindAlert)
			threshold := r.Config.Policy.BlindAlertThreshold
			if len(alerts) != 1 || alerts[0].Details["consecutive_blind_cycles"] != threshold {
				return fmt.Errorf("BLIND_ALERT events = %+v, want exactly one at %d blind cycles", alerts, threshold)
			}
			if len(r.EventsOf(ports.EventFetchFailure)) == 0 {
				return errors.New("no FETCH_FAILURE recorded during the outage")
			}
			return nil
		},
	}
}

func s9() Scenario {
	return Scenario{
		ID: "S9", Name: "no-traffic scale-to-1",
		Criterion: "with zero traffic (RequestCount absent) the controller scales in to the minimum of 1, never below",
		Duration:  45 * time.Minute, Initial: 3,
		Metrics: mockmetrics.Profile{Load: constant(0), Traffic: func(time.Duration) bool { return false }},
		Check: func(r Result) error {
			for _, c := range r.Cycles {
				if c.Capacity.Desired < 1 {
					return fmt.Errorf("cycle %d: desired %d below min", c.CycleID, c.Capacity.Desired)
				}
				if c.Signals.RequestCount.Quality != core.QualityNotEvaluable {
					return fmt.Errorf("cycle %d: RequestCount %s, want NOT_EVALUABLE", c.CycleID, c.Signals.RequestCount.Quality)
				}
				if c.Decision.Decision == core.ReduceCapacity && c.Decision.Action.Step != -1 {
					return fmt.Errorf("cycle %d: reduce step %d", c.CycleID, c.Decision.Action.Step)
				}
			}
			tail := r.Cycles[len(r.Cycles)-5:]
			for _, c := range tail {
				if c.Capacity.Desired != 1 || c.Decision.Reason != core.ReasonMaintainAtMin {
					return fmt.Errorf("cycle %d: desired %d %s, want 1 and MAINTAIN_AT_MIN", c.CycleID, c.Capacity.Desired, c.Decision.Reason)
				}
			}
			return nil
		},
	}
}

func s10() Scenario {
	slow := between(50*time.Minute, 60*time.Minute)
	return Scenario{
		ID: "S10", Name: "composite 90-minute run",
		Criterion: "combines ramp, spike, a low-CPU latency breach and a decrease; capacity stays within [1,5] and the documented blind spot appears",
		Duration:  90 * time.Minute, Initial: 2,
		Metrics: mockmetrics.Profile{
			Load: phases(
				phase{0, 0.9},
				phase{15 * time.Minute, 2.2},
				phase{35 * time.Minute, 3.5},
				phase{37 * time.Minute, 1.5},
				phase{50 * time.Minute, 0.6},
				phase{60 * time.Minute, 0.4},
			),
			NoiseStdDev: 3,
			LatencyOverlayMs: func(e time.Duration) float64 {
				if slow(e) {
					return 800 // slow downstream dependency: latency without CPU
				}
				return 0
			},
		},
		Check: func(r Result) error {
			var increases, reduces, lowCPU int
			for _, c := range r.Cycles {
				if d := c.Capacity.Desired; d < 1 || d > 5 {
					return fmt.Errorf("cycle %d: desired %d outside [1,5]", c.CycleID, d)
				}
				switch {
				case c.Decision.Decision == core.IncreaseCapacity:
					increases++
				case c.Decision.Decision == core.ReduceCapacity:
					reduces++
				case c.Decision.Reason == core.ReasonMaintainSLOBreachLowCPU:
					lowCPU++
				}
			}
			if increases == 0 || reduces == 0 || lowCPU == 0 {
				return fmt.Errorf("increases=%d reduces=%d slo-breach-low-cpu=%d, want all > 0", increases, reduces, lowCPU)
			}
			return nil
		},
	}
}
