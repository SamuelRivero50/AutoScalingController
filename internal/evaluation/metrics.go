package evaluation

import (
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/app"
	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

const (
	// flipWindow is the S5 anti-flapping window: no INCREASE_CAPACITY
	// decision may follow a REDUCE_CAPACITY decision within it.
	flipWindow = 10 * time.Minute
	// saturatedCPU marks a reading at which the CPU-based demand estimate
	// is only a lower bound.
	saturatedCPU = 99.5
	// maxExamples bounds the example cycles kept per flag.
	maxExamples = 5
	// defaultScaleOutCPU is used when a record carries no cpu_high threshold.
	defaultScaleOutCPU = 70
)

// Report is the evaluation of one run.
type Report struct {
	RunID      string       `json:"run_id"`
	Mode       core.Mode    `json:"mode"`
	Profile    core.Profile `json:"profile"`
	ConfigHash string       `json:"config_hash"`
	Cycles     int          `json:"cycles"`
	Start      time.Time    `json:"start"`
	End        time.Time    `json:"end"`
	Interval   Duration     `json:"cycle_interval"`

	SLO          SLO          `json:"slo"`
	Provisioning Provisioning `json:"provisioning"`
	Changes      Changes      `json:"changes"`
	Relief       Relief       `json:"time_to_relief"`
	Flags        Flags        `json:"late_or_incorrect"`
	Warmup       Warmup       `json:"warmup"`

	Decisions map[core.DecisionKind]int `json:"decisions"`
	Reasons   map[core.ReasonCode]int   `json:"reason_codes"`
}

// SLO is the SLO compliance over the cycles where latency and errors were
// both evaluable (docs/spec/simulator.md §5).
type SLO struct {
	EvaluableCycles int     `json:"evaluable_cycles"`
	CompliantCycles int     `json:"compliant_cycles"`
	LatencyBreaches int     `json:"latency_breach_cycles"`
	ErrorBreaches   int     `json:"error_breach_cycles"`
	CompliancePct   float64 `json:"compliance_pct"`
}

// Provisioning compares the capacity used with the theoretical minimum
// R = max(1, ceil(load/0.55)), capped at the maximum. The load (in instance
// units) is inferred from the log as fleet CPU × in-service / 100, so it is
// a slight overestimate (it includes the idle CPU floor) and only a lower
// bound on saturated cycles.
type Provisioning struct {
	InstanceMinutes        float64 `json:"instance_minutes"`
	EvaluableCycles        int     `json:"evaluable_cycles"`
	UsedInstanceMinutes    float64 `json:"used_instance_minutes_evaluable"`
	MinimumInstanceMinutes float64 `json:"minimum_instance_minutes"`
	ExcessPct              float64 `json:"excess_over_minimum_pct"`
	OverCycles             int     `json:"over_provisioned_cycles"`
	OverPct                float64 `json:"over_provisioned_pct"`
	OverMagnitude          float64 `json:"over_provisioned_mean_instances"`
	UnderCycles            int     `json:"under_provisioned_cycles"`
	UnderPct               float64 `json:"under_provisioned_pct"`
	UnderMagnitude         float64 `json:"under_provisioned_mean_instances"`
	SaturatedCycles        int     `json:"saturated_cycles"`
}

// Changes counts capacity changes and oscillation.
type Changes struct {
	ScaleOuts int `json:"scale_outs"`
	ScaleIns  int `json:"scale_ins"`
	// Resets are desired-capacity reductions the controller made without a
	// REDUCE_CAPACITY decision: the breaker capacity reset.
	Resets       int `json:"capacity_resets"`
	Terminations int `json:"stuck_terminations"`
	Skipped      int `json:"skipped_actions"`
	Errors       int `json:"failed_actions"`
	// Reversals counts direction changes between consecutive executed
	// capacity changes (out then in, or in then out).
	Reversals int `json:"direction_reversals"`
	// IncreaseAfterReduce counts INCREASE_CAPACITY decisions made within
	// 10 minutes of a REDUCE_CAPACITY decision (S5 criterion).
	IncreaseAfterReduce int `json:"increase_within_10m_of_reduce"`
}

// Relief summarizes the overload incidents.
type Relief struct {
	Incidents []Incident `json:"incidents"`
	// Summary over the resolved incidents that triggered a scale-out.
	Resolved int      `json:"resolved_with_scale_out"`
	Median   Duration `json:"median"`
	Max      Duration `json:"max"`
}

// Incident runs from the first cycle with CPU at or above the scale-out
// trigger to the first cycle where CPU is valid and back below it.
type Incident struct {
	StartCycle int64 `json:"start_cycle"`
	EndCycle   int64 `json:"end_cycle,omitempty"`
	Resolved   bool  `json:"resolved"`
	// ScaledOut tells whether an INCREASE_CAPACITY decision happened
	// during the incident; transient spikes resolve without one.
	ScaledOut       bool     `json:"scaled_out"`
	TimeToRelief    Duration `json:"time_to_relief"`
	DecisionLatency Duration `json:"decision_latency"`
	ReactionTime    Duration `json:"reaction_time"`
}

// Flags lists cycles whose outcome shows a late or incorrect decision, or
// an action that did not execute (REQ-PRESENT-6).
type Flags struct {
	SLOBreachLowCPU FlagSet `json:"slo_breach_low_cpu"`
	OverloadAtMax   FlagSet `json:"overload_at_max"`
	Skipped         FlagSet `json:"skipped_actions"`
	Failed          FlagSet `json:"failed_actions"`
}

// FlagSet is a count plus the first few examples.
type FlagSet struct {
	Count    int        `json:"count"`
	Examples []CycleRef `json:"examples"`
}

// CycleRef points at one cycle record.
type CycleRef struct {
	CycleID    int64             `json:"cycle_id"`
	TS         time.Time         `json:"ts"`
	Decision   core.DecisionKind `json:"decision"`
	ReasonCode core.ReasonCode   `json:"reason_code"`
	CPU        *float64          `json:"cpu"`
	InService  int               `json:"in_service"`
}

// Warmup is the measured launch-to-in-service time, observed at cycle
// granularity (so every sample is an upper bound).
type Warmup struct {
	Samples int      `json:"samples"`
	Min     Duration `json:"min"`
	Median  Duration `json:"median"`
	Max     Duration `json:"max"`
	Assumed Duration `json:"assumed"`
}

// Duration marshals as seconds.
type Duration time.Duration

// MarshalJSON encodes the duration as seconds.
func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(formatFloat(time.Duration(d).Seconds())), nil
}

// Evaluate computes the metrics of one run.
func Evaluate(run Run) Report {
	rep := Report{
		RunID:     run.ID,
		Cycles:    len(run.Cycles),
		Decisions: map[core.DecisionKind]int{},
		Reasons:   map[core.ReasonCode]int{},
	}
	if len(run.Cycles) == 0 {
		return rep
	}
	first := run.Cycles[0]
	rep.Mode, rep.Profile, rep.ConfigHash = first.Mode, first.Profile, first.ConfigHash
	rep.Start, rep.End = first.TS, run.Cycles[len(run.Cycles)-1].TS

	steps := stepDurations(run.Cycles)
	rep.Interval = Duration(median(steps))
	step := stepTargetCPU(first.Profile)

	for _, c := range run.Cycles {
		rep.Decisions[c.Decision]++
		rep.Reasons[c.ReasonCode]++
	}
	rep.SLO = sloCompliance(run.Cycles)
	rep.Provisioning = provisioning(run.Cycles, steps, step)
	rep.Changes = changes(run.Cycles)
	rep.Relief = relief(run.Cycles)
	rep.Flags = flags(run.Cycles)
	rep.Warmup = warmup(run.Cycles, first.Profile)
	return rep
}

// stepDurations returns how long each cycle's capacity was held: the gap to
// the next cycle, and the median gap for the last one.
func stepDurations(cycles []Cycle) []time.Duration {
	steps := make([]time.Duration, len(cycles))
	for i := range len(cycles) - 1 {
		steps[i] = cycles[i+1].TS.Sub(cycles[i].TS)
	}
	if n := len(cycles); n > 1 {
		steps[n-1] = median(steps[:n-1])
	}
	return steps
}

func stepTargetCPU(p core.Profile) float64 {
	cfg, err := app.ConfigForProfile(p)
	if err != nil {
		cfg = app.RealisticConfig()
	}
	return cfg.Policy.StepTargetCPU
}

func sloCompliance(cycles []Cycle) SLO {
	var s SLO
	for _, c := range cycles {
		lat, okLat := c.Condition("latency_p95_breach")
		errs, okErr := c.Condition("error_rate_breach")
		if !okLat || !okErr || lat.Value == nil || errs.Value == nil {
			continue
		}
		s.EvaluableCycles++
		if lat.Met {
			s.LatencyBreaches++
		}
		if errs.Met {
			s.ErrorBreaches++
		}
		if !lat.Met && !errs.Met {
			s.CompliantCycles++
		}
	}
	s.CompliancePct = pct(s.CompliantCycles, s.EvaluableCycles)
	return s
}

// minimumInstances is R for the cycle, and false when CPU was not valid.
func minimumInstances(c Cycle, stepTarget float64) (r int, saturated, ok bool) {
	cpu, ok := c.SignalValue(core.SignalCPU)
	if !ok || c.Capacity.InService == 0 {
		return 0, false, false
	}
	load := cpu * float64(c.Capacity.InService) / 100
	r = int(math.Ceil(load/(stepTarget/100) - 1e-9))
	lo, hi := max(1, c.Capacity.Min), c.Capacity.Max
	if hi < lo {
		hi = lo
	}
	return min(hi, max(lo, r)), cpu >= saturatedCPU, true
}

func provisioning(cycles []Cycle, steps []time.Duration, stepTarget float64) Provisioning {
	var p Provisioning
	var over, under int
	for i, c := range cycles {
		minutes := steps[i].Minutes()
		running := float64(c.Capacity.InService + c.Capacity.Pending + c.Capacity.Draining)
		p.InstanceMinutes += running * minutes

		r, saturated, ok := minimumInstances(c, stepTarget)
		if !ok {
			continue
		}
		p.EvaluableCycles++
		p.UsedInstanceMinutes += running * minutes
		p.MinimumInstanceMinutes += float64(r) * minutes
		if saturated {
			p.SaturatedCycles++
		}
		switch n := c.Capacity.InService; {
		case n > r:
			p.OverCycles++
			over += n - r
		case n < r:
			p.UnderCycles++
			under += r - n
		}
	}
	p.OverPct = pct(p.OverCycles, p.EvaluableCycles)
	p.UnderPct = pct(p.UnderCycles, p.EvaluableCycles)
	p.OverMagnitude = ratio(float64(over), float64(p.OverCycles))
	p.UnderMagnitude = ratio(float64(under), float64(p.UnderCycles))
	if p.MinimumInstanceMinutes > 0 {
		p.ExcessPct = (p.UsedInstanceMinutes/p.MinimumInstanceMinutes - 1) * 100
	}
	return p
}

func changes(cycles []Cycle) Changes {
	var ch Changes
	lastDir := 0
	var lastReduce time.Time
	for _, c := range cycles {
		switch c.Decision {
		case core.ReduceCapacity:
			lastReduce = c.TS
		case core.IncreaseCapacity:
			if !lastReduce.IsZero() && c.TS.Sub(lastReduce) < flipWindow {
				ch.IncreaseAfterReduce++
			}
		}
		switch c.Action.Status {
		case ports.ActionSkipped:
			ch.Skipped++
			continue
		case ports.ActionError:
			ch.Errors++
			continue
		}
		switch c.Action.Type {
		case core.ActionTerminateInstance:
			ch.Terminations++
		case core.ActionSetDesiredCapacity:
			dir := direction(c)
			switch dir {
			case 1:
				ch.ScaleOuts++
			case -1:
				if c.Decision == core.ReduceCapacity {
					ch.ScaleIns++
				} else {
					ch.Resets++
				}
			}
			if dir != 0 {
				if lastDir != 0 && dir != lastDir {
					ch.Reversals++
				}
				lastDir = dir
			}
		}
	}
	return ch
}

// direction compares the requested desired capacity with the capacity the
// cycle observed: 1 for scale-out, -1 for scale-in, 0 for no change.
func direction(c Cycle) int {
	v, ok := c.Action.Params["desired_capacity"].(float64)
	if !ok {
		switch c.Decision {
		case core.IncreaseCapacity:
			return 1
		case core.ReduceCapacity:
			return -1
		}
		return 0
	}
	switch target := int(v); {
	case target > c.Capacity.Desired:
		return 1
	case target < c.Capacity.Desired:
		return -1
	}
	return 0
}

func relief(cycles []Cycle) Relief {
	var rel Relief
	var cur *Incident
	var start, firstIncrease time.Time
	for _, c := range cycles {
		cpu, valid := c.SignalValue(core.SignalCPU)
		trigger := float64(defaultScaleOutCPU)
		if cond, ok := c.Condition("cpu_high"); ok && cond.Threshold != nil {
			trigger = *cond.Threshold
		}
		high := valid && cpu >= trigger
		switch {
		case cur == nil && high:
			cur = &Incident{StartCycle: c.CycleID}
			start, firstIncrease = c.TS, time.Time{}
		case cur != nil && valid && !high:
			cur.EndCycle, cur.Resolved = c.CycleID, true
			cur.TimeToRelief = Duration(c.TS.Sub(start))
			if cur.ScaledOut {
				cur.DecisionLatency = Duration(firstIncrease.Sub(start))
				cur.ReactionTime = Duration(c.TS.Sub(firstIncrease))
			}
			rel.Incidents = append(rel.Incidents, *cur)
			cur = nil
			continue
		}
		if cur != nil && !cur.ScaledOut && c.Decision == core.IncreaseCapacity {
			cur.ScaledOut, firstIncrease = true, c.TS
		}
	}
	if cur != nil {
		rel.Incidents = append(rel.Incidents, *cur)
	}
	var ttr []time.Duration
	for _, in := range rel.Incidents {
		if in.Resolved && in.ScaledOut {
			ttr = append(ttr, time.Duration(in.TimeToRelief))
		}
	}
	rel.Resolved = len(ttr)
	rel.Median = Duration(median(ttr))
	if len(ttr) > 0 {
		rel.Max = Duration(slices.Max(ttr))
	}
	return rel
}

func flags(cycles []Cycle) Flags {
	var f Flags
	for _, c := range cycles {
		if c.ReasonCode == core.ReasonMaintainSLOBreachLowCPU {
			f.SLOBreachLowCPU.add(c)
		}
		if c.ReasonCode == core.ReasonMaintainAtMax {
			if cond, ok := c.Condition("overload"); ok && cond.Met {
				f.OverloadAtMax.add(c)
			}
		}
		switch c.Action.Status {
		case ports.ActionSkipped:
			f.Skipped.add(c)
		case ports.ActionError:
			f.Failed.add(c)
		}
	}
	return f
}

func (f *FlagSet) add(c Cycle) {
	f.Count++
	if len(f.Examples) >= maxExamples {
		return
	}
	ref := CycleRef{CycleID: c.CycleID, TS: c.TS, Decision: c.Decision, ReasonCode: c.ReasonCode, InService: c.Capacity.InService}
	if v, ok := c.SignalValue(core.SignalCPU); ok {
		ref.CPU = &v
	}
	f.Examples = append(f.Examples, ref)
}

func warmup(cycles []Cycle, profile core.Profile) Warmup {
	var w Warmup
	if cfg, err := app.ConfigForProfile(profile); err == nil {
		w.Assumed = Duration(cfg.Warmup)
	}
	initial := map[string]bool{}
	for _, in := range cycles[0].Capacity.Instances {
		initial[in.ID] = true
	}
	done := map[string]bool{}
	var samples []time.Duration
	for _, c := range cycles {
		for _, in := range c.Capacity.Instances {
			if initial[in.ID] || done[in.ID] || in.State != core.InstanceInService || in.LaunchedAt == nil {
				continue
			}
			done[in.ID] = true
			samples = append(samples, c.TS.Sub(*in.LaunchedAt))
		}
	}
	w.Samples = len(samples)
	if len(samples) > 0 {
		w.Min = Duration(slices.Min(samples))
		w.Max = Duration(slices.Max(samples))
		w.Median = Duration(median(samples))
	}
	return w
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// median returns the lower median, or 0 for no values.
func median(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[(len(s)-1)/2]
}

func pct(n, total int) float64 {
	return ratio(float64(n), float64(total)) * 100
}

func ratio(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}
