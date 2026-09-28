// Package ports defines the five ports between the application layer and
// the outside world (docs/spec/architecture.md §2). Signatures use only
// project-owned and standard-library types; no AWS SDK type crosses a port.
package ports

import (
	"context"
	"errors"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
)

// Clock provides the current time and waiting. The simulator uses a fake
// clock so simulated hours run in seconds.
type Clock interface {
	Now() time.Time
	// Sleep waits for d or until ctx is done, returning ctx.Err() in the
	// latter case.
	Sleep(ctx context.Context, d time.Duration) error
}

// Period is one closed metric aggregation period [Start, End).
type Period struct {
	Start time.Time
	End   time.Time
}

// LoadBalancerReadings are the ALB metrics for one period.
type LoadBalancerReadings struct {
	RequestCount          core.Reading
	TargetResponseTimeP95 core.Reading // milliseconds
	Target5xxCount        core.Reading
	ELB5xxCount           core.Reading
	RequestCountPerTarget core.Reading
	HealthyHostCount      core.Reading
}

// MetricsSource fetches metrics for a closed period. A returned error means
// the whole fetch failed; the caller then treats every reading as MISSING.
type MetricsSource interface {
	InstanceCPU(ctx context.Context, p Period, instanceIDs []string) ([]core.InstanceCPU, error)
	LoadBalancer(ctx context.Context, p Period) (LoadBalancerReadings, error)
}

// ActivityStatus is the outcome of one provisioning activity.
type ActivityStatus string

// Scaling activity outcomes.
const (
	ActivityInProgress ActivityStatus = "IN_PROGRESS"
	ActivitySuccessful ActivityStatus = "SUCCESSFUL"
	ActivityFailed     ActivityStatus = "FAILED"
)

// ScalingActivity is one ASG scaling activity (launch or termination).
type ScalingActivity struct {
	ID          string
	InstanceID  string
	Launch      bool // true for a launch, false for a termination
	Status      ActivityStatus
	StartedAt   time.Time
	Description string
}

// ActionResult describes a successful capacity-changing call.
type ActionResult struct {
	RequestID string
}

// InstanceProvisioner reads capacity and applies absolute capacity changes.
// It is one port because docs/spec/architecture.md §2 defines it as a
// single responsibility.
type InstanceProvisioner interface {
	// DescribeCapacity returns the ASG and target-health ground truth.
	// Instances are IN_SERVICE only when InService and their target is
	// healthy; otherwise PENDING (docs/spec/lifecycle-and-failures.md §4).
	DescribeCapacity(ctx context.Context) (core.CapacitySnapshot, error)
	// SetDesiredCapacity sets an absolute desired capacity, never a delta.
	SetDesiredCapacity(ctx context.Context, desired int) (ActionResult, error)
	// TerminateInstance terminates one instance and decrements desired.
	TerminateInstance(ctx context.Context, instanceID string) (ActionResult, error)
	// ScalingActivities returns activities started at or after since.
	ScalingActivities(ctx context.Context, since time.Time) ([]ScalingActivity, error)
}

// Errors returned by StateStore.Load.
var (
	ErrStateNotFound = errors.New("ports: state not found")
	ErrStateCorrupt  = errors.New("ports: state corrupt")
)

// State is the controller's own memory persisted between cycles. Capacity
// truth always comes from the provisioner, never from here (ADR-0009).
type State struct {
	RunID     string
	NextCycle int64
	Memory    core.Memory
	Breaker   core.Breaker
	// ActivityWatermark is the start time of the oldest activity still
	// worth reading.
	ActivityWatermark time.Time
	// SeenActivities holds activity IDs already accounted for, so each
	// activity counts once.
	SeenActivities []string
	// LastInstances is the instance view of the previous cycle, used to
	// emit INSTANCE_STATE_CHANGE events.
	LastInstances []core.Instance
}

// StateStore persists State.
type StateStore interface {
	// Load returns ErrStateNotFound or an error wrapping ErrStateCorrupt
	// when no usable state exists.
	Load(ctx context.Context) (State, error)
	Save(ctx context.Context, s State) error
}

// DecisionLogger appends decision-log records (docs/spec/decision-log.md).
type DecisionLogger interface {
	LogCycle(ctx context.Context, r CycleRecord) error
	LogEvent(ctx context.Context, r EventRecord) error
}
