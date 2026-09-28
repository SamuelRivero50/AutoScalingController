package core

// DecisionKind is one of the three decision literals (REQ-VOCAB-1).
type DecisionKind string

// Decision literals, used verbatim in the decision log.
const (
	MaintainCapacity DecisionKind = "MAINTAIN_CAPACITY"
	IncreaseCapacity DecisionKind = "INCREASE_CAPACITY"
	ReduceCapacity   DecisionKind = "REDUCE_CAPACITY"
)

// DecisionKinds returns every decision literal.
func DecisionKinds() []DecisionKind {
	return []DecisionKind{MaintainCapacity, IncreaseCapacity, ReduceCapacity}
}

// ReasonCode explains why a decision was reached. The catalog is closed:
// see docs/spec/decision-log.md §3.
type ReasonCode string

// Reason code catalog.
const (
	ReasonIncreaseCPUHigh           ReasonCode = "INCREASE_CPU_HIGH"
	ReasonIncreaseLatencySLO        ReasonCode = "INCREASE_LATENCY_SLO"
	ReasonIncreaseCapacityErrors    ReasonCode = "INCREASE_CAPACITY_ERRORS"
	ReasonIncreaseAppErrorsWithLoad ReasonCode = "INCREASE_APP_ERRORS_WITH_LOAD"
	ReasonReduceProjectionOK        ReasonCode = "REDUCE_PROJECTION_OK"
	ReasonMaintainStable            ReasonCode = "MAINTAIN_STABLE"
	ReasonMaintainWindowPending     ReasonCode = "MAINTAIN_WINDOW_PENDING"
	ReasonMaintainPendingCapacity   ReasonCode = "MAINTAIN_PENDING_CAPACITY"
	ReasonMaintainScaleInBlocked    ReasonCode = "MAINTAIN_SCALE_IN_BLOCKED"
	ReasonMaintainAtMax             ReasonCode = "MAINTAIN_AT_MAX"
	ReasonMaintainAtMin             ReasonCode = "MAINTAIN_AT_MIN"
	ReasonMaintainBlind             ReasonCode = "MAINTAIN_BLIND"
	ReasonMaintainStateUnknown      ReasonCode = "MAINTAIN_STATE_UNKNOWN"
	ReasonMaintainNoHealthyTargets  ReasonCode = "MAINTAIN_NO_HEALTHY_TARGETS"
	ReasonMaintainSLOBreachLowCPU   ReasonCode = "MAINTAIN_SLO_BREACH_LOW_CPU"
)

// ReasonCodes returns the full closed catalog in documentation order.
func ReasonCodes() []ReasonCode {
	return []ReasonCode{
		ReasonIncreaseCPUHigh,
		ReasonIncreaseLatencySLO,
		ReasonIncreaseCapacityErrors,
		ReasonIncreaseAppErrorsWithLoad,
		ReasonReduceProjectionOK,
		ReasonMaintainStable,
		ReasonMaintainWindowPending,
		ReasonMaintainPendingCapacity,
		ReasonMaintainScaleInBlocked,
		ReasonMaintainAtMax,
		ReasonMaintainAtMin,
		ReasonMaintainBlind,
		ReasonMaintainStateUnknown,
		ReasonMaintainNoHealthyTargets,
		ReasonMaintainSLOBreachLowCPU,
	}
}

// Decision returns the decision literal a reason code belongs to, and false
// if the code is not in the catalog.
func (r ReasonCode) Decision() (DecisionKind, bool) {
	switch r {
	case ReasonIncreaseCPUHigh, ReasonIncreaseLatencySLO,
		ReasonIncreaseCapacityErrors, ReasonIncreaseAppErrorsWithLoad:
		return IncreaseCapacity, true
	case ReasonReduceProjectionOK:
		return ReduceCapacity, true
	case ReasonMaintainStable, ReasonMaintainWindowPending,
		ReasonMaintainPendingCapacity, ReasonMaintainScaleInBlocked,
		ReasonMaintainAtMax, ReasonMaintainAtMin, ReasonMaintainBlind,
		ReasonMaintainStateUnknown, ReasonMaintainNoHealthyTargets,
		ReasonMaintainSLOBreachLowCPU:
		return MaintainCapacity, true
	default:
		return "", false
	}
}

// Quality is the per-cycle classification of an observed signal
// (docs/spec/lifecycle-and-failures.md §1).
type Quality string

// Signal quality states.
const (
	QualityValid        Quality = "VALID"
	QualityNotEvaluable Quality = "NOT_EVALUABLE"
	QualityNoNewData    Quality = "NO_NEW_DATA"
	QualityMissing      Quality = "MISSING"
	QualityStale        Quality = "STALE"
	QualityAnomalous    Quality = "ANOMALOUS"
)

// Qualities returns every signal quality state.
func Qualities() []Quality {
	return []Quality{
		QualityValid, QualityNotEvaluable, QualityNoNewData,
		QualityMissing, QualityStale, QualityAnomalous,
	}
}

// Unusable reports whether the quality means the value must be discarded
// (MISSING, STALE or ANOMALOUS).
func (q Quality) Unusable() bool {
	return q == QualityMissing || q == QualityStale || q == QualityAnomalous
}

// InstanceState is the controller's view of an instance lifecycle state.
type InstanceState string

// Instance states.
const (
	InstancePending    InstanceState = "PENDING"
	InstanceInService  InstanceState = "IN_SERVICE"
	InstanceDraining   InstanceState = "DRAINING"
	InstanceFailed     InstanceState = "FAILED"
	InstanceTerminated InstanceState = "TERMINATED"
)

// InstanceStates returns every instance state.
func InstanceStates() []InstanceState {
	return []InstanceState{
		InstancePending, InstanceInService, InstanceDraining,
		InstanceFailed, InstanceTerminated,
	}
}

// BreakerState is the provisioning circuit-breaker state.
type BreakerState string

// Circuit-breaker states.
const (
	BreakerClosed   BreakerState = "CLOSED"
	BreakerOpen     BreakerState = "OPEN"
	BreakerHalfOpen BreakerState = "HALF_OPEN"
)

// BreakerStates returns every breaker state.
func BreakerStates() []BreakerState {
	return []BreakerState{BreakerClosed, BreakerOpen, BreakerHalfOpen}
}

// ActionType is the kind of action requested by a decision.
type ActionType string

// Action types.
const (
	ActionSetDesiredCapacity ActionType = "SET_DESIRED_CAPACITY"
	ActionTerminateInstance  ActionType = "TERMINATE_INSTANCE"
	ActionNone               ActionType = "NONE"
)

// ActionTypes returns every action type.
func ActionTypes() []ActionType {
	return []ActionType{ActionSetDesiredCapacity, ActionTerminateInstance, ActionNone}
}

// Profile is a named configuration profile.
type Profile string

// Configuration profiles.
const (
	ProfileDemo      Profile = "demo"
	ProfileRealistic Profile = "realistic"
)

// Profiles returns every profile.
func Profiles() []Profile {
	return []Profile{ProfileDemo, ProfileRealistic}
}

// Mode tells whether the controller runs against the simulator or real AWS.
type Mode string

// Run modes.
const (
	ModeSim  Mode = "sim"
	ModeReal Mode = "real"
)

// Modes returns every run mode.
func Modes() []Mode {
	return []Mode{ModeSim, ModeReal}
}

// Alert is an out-of-band condition a human should know about.
type Alert string

// Alerts raised by the decision core.
const (
	// AlertBlind is raised once when the consecutive blind-cycle streak
	// reaches the configured threshold.
	AlertBlind Alert = "BLIND"
	// AlertZeroHealthyTargets is raised when no target is healthy and no
	// capacity change is in flight.
	AlertZeroHealthyTargets Alert = "ZERO_HEALTHY_TARGETS"
)
