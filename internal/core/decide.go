package core

// PolicyInput is the immutable snapshot the decision is made from.
// Memory must already be advanced with this cycle (see Advance).
type PolicyInput struct {
	CycleID  int64
	Config   PolicyConfig
	Signals  Signals
	Capacity CapacitySnapshot
	Memory   Memory
	Breaker  BreakerStatus
}

// Condition is one evaluated condition of the justification.
// Value and Threshold are nil when not applicable or not available.
type Condition struct {
	Name      string
	Value     *float64
	Threshold *float64
	Met       bool
}

// RequestedAction is the action a decision asks the shell to execute.
// TargetDesired is always an absolute capacity (never a delta).
type RequestedAction struct {
	Type          ActionType
	TargetDesired int
	Step          int // signed change relative to in-service, for explanation only
}

// Decision is the output of Decide.
type Decision struct {
	Decision   DecisionKind
	Reason     ReasonCode
	Conditions []Condition
	Action     RequestedAction
	Alerts     []Alert
	Blind      bool
}

// Decide is the pure decision function of docs/spec/decision-policy.md.
// See the precedence list in the M1 design; the first matching rule wins.
func Decide(in PolicyInput) Decision {
	cfg := in.Config
	capacity := in.Capacity
	a := Assess(in.Signals, capacity, cfg)
	out := in.Memory.ScaleOut(cfg)
	scaleIn := in.Memory.ScaleIn(cfg)
	blind := in.Signals.Blind()
	outFlight := capacity.Known && capacity.ScaleOutInFlight()
	inFlight := capacity.Known && capacity.ScaleInInFlight()

	d := Decision{
		Decision:   MaintainCapacity,
		Conditions: conditions(justification{in: in, a: a, out: out, scaleIn: scaleIn, blind: blind, outFlight: outFlight}),
		Action:     RequestedAction{Type: ActionNone},
		Blind:      blind,
	}
	if blind && in.Memory.BlindStreak == cfg.BlindAlertThreshold {
		d.Alerts = append(d.Alerts, AlertBlind)
	}

	maintain := func(r ReasonCode) Decision {
		d.Reason = r
		return d
	}

	switch {
	case !capacity.Known:
		return maintain(ReasonMaintainStateUnknown)

	case capacity.HealthyTargets == 0:
		if outFlight || inFlight {
			return maintain(ReasonMaintainPendingCapacity)
		}
		d.Alerts = append(d.Alerts, AlertZeroHealthyTargets)
		return maintain(ReasonMaintainNoHealthyTargets)

	case blind:
		return maintain(ReasonMaintainBlind)

	case out.Satisfied:
		switch {
		case outFlight:
			return maintain(ReasonMaintainPendingCapacity)
		case a.N >= cfg.MaxInstances:
			return maintain(ReasonMaintainAtMax)
		}
		step := ScaleOutStep(a.N, out.Latest.CPU, out.Latest.Trigger, cfg)
		d.Decision = IncreaseCapacity
		d.Reason = increaseReason(out.Latest.Trigger)
		d.Action = RequestedAction{Type: ActionSetDesiredCapacity, TargetDesired: a.N + step, Step: step}
		return d

	case scaleIn.Satisfied:
		switch {
		case outFlight || inFlight:
			return maintain(ReasonMaintainPendingCapacity)
		case a.N <= cfg.MinInstances:
			return maintain(ReasonMaintainAtMin)
		}
		d.Decision = ReduceCapacity
		d.Reason = ReasonReduceProjectionOK
		d.Action = RequestedAction{Type: ActionSetDesiredCapacity, TargetDesired: a.N - 1, Step: -1}
		return d

	case outFlight:
		return maintain(ReasonMaintainPendingCapacity)
	case a.LowLoadAtMin:
		return maintain(ReasonMaintainAtMin)
	case a.SLOBreachLowCPU:
		return maintain(ReasonMaintainSLOBreachLowCPU)
	case a.Overloaded:
		return maintain(ReasonMaintainWindowPending)
	case a.Comfortable && scaleIn.Blocked:
		return maintain(ReasonMaintainScaleInBlocked)
	case a.Comfortable && inFlight:
		return maintain(ReasonMaintainPendingCapacity)
	case a.Comfortable:
		return maintain(ReasonMaintainWindowPending)
	default:
		return maintain(ReasonMaintainStable)
	}
}

func increaseReason(t Trigger) ReasonCode {
	switch t {
	case TriggerLatency:
		return ReasonIncreaseLatencySLO
	case TriggerCapacityErrors:
		return ReasonIncreaseCapacityErrors
	case TriggerAppErrors:
		return ReasonIncreaseAppErrorsWithLoad
	default:
		return ReasonIncreaseCPUHigh
	}
}

func num(v float64) *float64 { return &v }

func numIf(ok bool, v float64) *float64 {
	if !ok {
		return nil
	}
	return num(v)
}

// justification bundles everything conditions() needs to build the evaluated
// condition list, keeping the parameter count low.
type justification struct {
	in        PolicyInput
	a         Assessment
	out       ScaleOutWindow
	scaleIn   ScaleInWindow
	blind     bool
	outFlight bool
}

func conditions(j justification) []Condition {
	in := j.in
	a := j.a
	out := j.out
	scaleIn := j.scaleIn
	blind := j.blind
	outFlight := j.outFlight
	cfg := in.Config
	sig := in.Signals
	c := in.Capacity
	latOK, errOK := sig.LatencyP95.Valid(), sig.ErrorRate.Valid()

	return []Condition{
		{Name: "capacity_known", Met: c.Known},
		{Name: "healthy_targets", Value: num(float64(c.HealthyTargets)), Threshold: num(1), Met: c.HealthyTargets >= 1},
		{Name: "blind", Met: blind},
		{Name: "cpu_valid", Met: a.CPUValid},
		{Name: "cpu_high", Value: numIf(a.CPUValid, a.CPU), Threshold: num(cfg.ScaleOutCPU), Met: a.CPUHigh},
		{Name: "cpu_elevated", Value: numIf(a.CPUValid, a.CPU), Threshold: num(cfg.ScaleOutCPUWithSLO), Met: a.CPUElevated},
		{Name: "latency_p95_breach", Value: numIf(latOK, sig.LatencyP95.Value), Threshold: num(cfg.LatencySLOMs), Met: a.LatencyBreach},
		{Name: "error_rate_breach", Value: numIf(errOK, sig.ErrorRate.Value), Threshold: num(cfg.ErrorSLOPct), Met: a.ErrorBreach},
		{Name: "overload", Met: a.Overloaded},
		{Name: "projected_cpu", Value: numIf(a.HasProjection, a.ProjectedCPU), Threshold: num(cfg.ScaleInProjectedCPU), Met: a.HasProjection && a.ProjectedCPU <= cfg.ScaleInProjectedCPU+epsilon},
		{Name: "latency_p95_comfort", Value: numIf(latOK, sig.LatencyP95.Value), Threshold: num(cfg.LatencyComfortMs), Met: a.LatencyComfort},
		{Name: "error_rate_comfort", Value: numIf(errOK, sig.ErrorRate.Value), Threshold: num(cfg.ErrorComfortPct), Met: a.ErrorComfort},
		{Name: "comfortable", Met: a.Comfortable},
		{Name: "scale_out_window", Value: num(float64(len(out.Breaching))), Threshold: num(float64(out.M)), Met: out.Satisfied},
		{Name: "scale_in_window", Value: num(float64(len(scaleIn.Comfortable))), Threshold: num(float64(scaleIn.N)), Met: scaleIn.Satisfied},
		{Name: "scale_in_window_cpu_valid", Met: !scaleIn.Blocked},
		{Name: "scale_out_in_flight", Value: num(float64(c.Pending())), Met: outFlight},
		{Name: "draining", Value: num(float64(c.Draining())), Met: c.Draining() > 0},
		{Name: "in_service", Value: num(float64(a.N)), Met: a.N >= 1},
		{Name: "at_max", Value: num(float64(a.N)), Threshold: num(float64(cfg.MaxInstances)), Met: a.N >= cfg.MaxInstances},
		{Name: "at_min", Value: num(float64(a.N)), Threshold: num(float64(cfg.MinInstances)), Met: a.N <= cfg.MinInstances},
	}
}
