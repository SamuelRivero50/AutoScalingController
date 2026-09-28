package ports

import (
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
)

// SchemaVersion is the decision-log schema version written in every record.
const SchemaVersion = "1.0"

// RunInfo identifies one controller process run.
type RunInfo struct {
	RunID      string
	Mode       core.Mode
	Profile    core.Profile
	ConfigHash string
}

// ActionStatus is the outcome of the action requested by a decision.
type ActionStatus string

// Action statuses (docs/spec/decision-log.md §5).
const (
	ActionOK      ActionStatus = "OK"
	ActionSkipped ActionStatus = "SKIPPED"
	ActionError   ActionStatus = "ERROR"
)

// Skip reasons for ActionSkipped.
const (
	SkipBreakerOpen          = "BREAKER_OPEN"
	SkipBreakerProbeInFlight = "BREAKER_PROBE_IN_FLIGHT"
	SkipBudgetExhausted      = "BUDGET_EXHAUSTED"
)

// ActionOutcome is what the shell did in a cycle.
type ActionOutcome struct {
	Type          core.ActionType
	TargetDesired *int   // for SET_DESIRED_CAPACITY
	InstanceID    string // for TERMINATE_INSTANCE
	Status        ActionStatus
	SkipReason    string
	RequestID     string
	Duration      *time.Duration
	Error         string
}

// CycleRecord is one evaluation cycle (exactly one per cycle).
type CycleRecord struct {
	Run      RunInfo
	CycleID  int64
	TS       time.Time
	Signals  core.Signals
	ScaleOut core.ScaleOutWindow
	ScaleIn  core.ScaleInWindow
	Capacity core.CapacitySnapshot
	Breaker  core.BreakerStatus
	Decision core.Decision
	Action   ActionOutcome
}

// EventType is the kind of an asynchronous event record.
type EventType string

// Event types (docs/spec/decision-log.md §4).
const (
	EventInstanceStateChange   EventType = "INSTANCE_STATE_CHANGE"
	EventBreakerStateChange    EventType = "BREAKER_STATE_CHANGE"
	EventBlindAlert            EventType = "BLIND_ALERT"
	EventNoHealthyTargetsAlert EventType = "NO_HEALTHY_TARGETS_ALERT"
	EventFetchFailure          EventType = "FETCH_FAILURE"
	EventStateRebuilt          EventType = "STATE_REBUILT"
	EventControllerStarted     EventType = "CONTROLLER_STARTED"
	EventControllerStopped     EventType = "CONTROLLER_STOPPED"
)

// EventTypes returns every event type.
func EventTypes() []EventType {
	return []EventType{
		EventInstanceStateChange, EventBreakerStateChange, EventBlindAlert,
		EventNoHealthyTargetsAlert, EventFetchFailure, EventStateRebuilt,
		EventControllerStarted, EventControllerStopped,
	}
}

// EventRecord is one asynchronous occurrence between or during cycles.
// Details holds JSON-compatible values (string, number, bool, nil, and
// maps/slices of those).
type EventRecord struct {
	Run     RunInfo
	TS      time.Time
	Type    EventType
	Details map[string]any
}
