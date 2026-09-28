package core

import "math"

// epsilon absorbs float rounding at exact boundary values, e.g. CPU 27.5 on
// 2 instances projects to exactly 55.
const epsilon = 1e-9

// Trigger is what made a cycle overloaded.
type Trigger string

// Overload triggers.
const (
	TriggerNone           Trigger = ""
	TriggerCPU            Trigger = "CPU"
	TriggerLatency        Trigger = "LATENCY"
	TriggerCapacityErrors Trigger = "CAPACITY_ERRORS" // ELB 5xx dominate
	TriggerAppErrors      Trigger = "APP_ERRORS"      // target 5xx dominate
)

// Assessment is the evaluation of a single cycle against the thresholds of
// docs/spec/decision-policy.md §1.
type Assessment struct {
	N        int  // in-service instances
	CPUValid bool // CPU is VALID or NO_NEW_DATA
	CPU      float64

	CPUHigh       bool // CPU >= 70
	CPUElevated   bool // CPU >= 55
	LatencyBreach bool // p95 > 500ms (VALID only)
	ErrorBreach   bool // error rate > 1% (VALID only)

	Overloaded bool
	Trigger    Trigger

	HasProjection  bool // false when N <= min (N/(N-1) undefined or not allowed)
	ProjectedCPU   float64
	LatencyComfort bool // p95 <= 350ms, or not evaluable
	ErrorComfort   bool // error <= 0.5%, or not evaluable
	Comfortable    bool

	// LowLoadAtMin: at the minimum, and the load would be comfortable.
	LowLoadAtMin bool
	// SLOBreachLowCPU: SLO breached while valid CPU is below 55.
	SLOBreachLowCPU bool
}

// Assess evaluates one cycle.
func Assess(sig Signals, capacity CapacitySnapshot, cfg PolicyConfig) Assessment {
	// NO_NEW_DATA is a real measurement already consumed by a previous
	// cycle: it explains this cycle's decision but Advance never counts it
	// in a window, so it cannot confirm anything twice.
	a := Assessment{
		N:        capacity.InService(),
		CPUValid: sig.CPU.Valid() || sig.CPU.Quality == QualityNoNewData,
		CPU:      sig.CPU.Value,
	}
	lat, latValid := sig.LatencyP95.Value, sig.LatencyP95.Valid()
	errRate, errValid := sig.ErrorRate.Value, sig.ErrorRate.Valid()

	a.LatencyBreach = latValid && lat > cfg.LatencySLOMs
	a.ErrorBreach = errValid && errRate > cfg.ErrorSLOPct
	a.LatencyComfort = !latValid || lat <= cfg.LatencyComfortMs+epsilon
	a.ErrorComfort = !errValid || errRate <= cfg.ErrorComfortPct+epsilon

	if !a.CPUValid {
		return a
	}

	a.CPUHigh = a.CPU >= cfg.ScaleOutCPU
	a.CPUElevated = a.CPU >= cfg.ScaleOutCPUWithSLO
	slo := a.LatencyBreach || a.ErrorBreach
	a.Overloaded = a.CPUHigh || (a.CPUElevated && slo)
	a.SLOBreachLowCPU = !a.CPUElevated && slo

	switch {
	case !a.Overloaded:
	case a.CPUHigh:
		a.Trigger = TriggerCPU
	case a.LatencyBreach:
		a.Trigger = TriggerLatency
	case sig.ELB5xx >= sig.Target5xx:
		a.Trigger = TriggerCapacityErrors
	default:
		a.Trigger = TriggerAppErrors
	}

	sloComfort := a.LatencyComfort && a.ErrorComfort
	if a.N > cfg.MinInstances {
		a.HasProjection = true
		a.ProjectedCPU = a.CPU * float64(a.N) / float64(a.N-1)
		a.Comfortable = a.ProjectedCPU <= cfg.ScaleInProjectedCPU+epsilon && sloComfort
	} else if a.N >= 1 {
		a.LowLoadAtMin = a.CPU <= cfg.ScaleInProjectedCPU+epsilon && sloComfort
	}
	return a
}

// ScaleOutStep is min(maxStep, Max-N, max(1, ceil(N*CPU/55) - N)), where
// maxStep is +2 for a CPU trigger and +1 for a latency/error trigger.
func ScaleOutStep(n int, cpu float64, trigger Trigger, cfg PolicyConfig) int {
	needed := int(math.Ceil(float64(n)*cpu/cfg.StepTargetCPU-epsilon)) - n
	limit := cfg.MaxScaleOutStep
	if trigger != TriggerCPU {
		limit = cfg.MaxSLOScaleOutStep
	}
	return max(0, min(limit, cfg.MaxInstances-n, max(1, needed)))
}
