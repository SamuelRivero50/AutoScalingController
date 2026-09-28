// Package fakeasg implements the InstanceProvisioner port as an in-memory
// Auto Scaling Group for the simulator. It models launch warmup, draining,
// the ASG's own launch-retry loop, and injected launch failures and stuck
// launches (docs/spec/simulator.md, ADR-0011).
//
// Instances are reported IN_SERVICE only once their warmup has elapsed,
// matching the real adapter's "InService and healthy" mapping
// (docs/spec/lifecycle-and-failures.md §4).
package fakeasg

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

var _ ports.InstanceProvisioner = (*ASG)(nil)

// ErrValidation mirrors the ASG ValidationError for out-of-bounds requests.
var ErrValidation = errors.New("fakeasg: validation error")

// ErrInjected is returned by API calls whose failure was injected.
var ErrInjected = errors.New("fakeasg: injected api failure")

// Config parameterizes the fake group.
type Config struct {
	Min, Max, InitialDesired int
	// Warmup is the time from launch to IN_SERVICE (healthy).
	Warmup time.Duration
	// DeregistrationDelay is how long a removed instance stays DRAINING.
	DeregistrationDelay time.Duration
	// LaunchRetryInterval is how often the group retries a failed launch.
	LaunchRetryInterval time.Duration
	// AZs are assigned round-robin; defaults to two zones.
	AZs []string
}

type phase int

const (
	phaseLaunching phase = iota + 1
	phaseInService
	phaseDraining
)

type instance struct {
	id         string
	az         string
	phase      phase
	launchedAt time.Time
	readyAt    time.Time
	drainUntil time.Time
	stuck      bool
}

// ASG is the fake group. It is safe for concurrent use.
type ASG struct {
	clock ports.Clock
	cfg   Config

	mu            sync.Mutex
	desired       int
	instances     []*instance
	activities    []ports.ScalingActivity
	nextInstance  int
	nextActivity  int
	nextRequest   int
	lastAttempt   time.Time
	failNext      int
	failUntil     time.Time
	stickNext     int
	failAPINext   int
	launchesTried int
}

// New returns a group with InitialDesired instances already in service.
func New(clock ports.Clock, cfg Config) *ASG {
	if len(cfg.AZs) == 0 {
		cfg.AZs = []string{"sim-az-a", "sim-az-b"}
	}
	a := &ASG{clock: clock, cfg: cfg, desired: cfg.InitialDesired}
	now := clock.Now()
	for range cfg.InitialDesired {
		in := a.newInstance(now)
		in.phase = phaseInService
		in.readyAt = now
	}
	return a
}

// FailNextLaunches makes the next n launch attempts fail.
func (a *ASG) FailNextLaunches(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failNext = n
}

// FailLaunchesUntil makes every launch attempt fail until t.
func (a *ASG) FailLaunchesUntil(t time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failUntil = t
}

// StickNextLaunches makes the next n launched instances never leave PENDING.
func (a *ASG) StickNextLaunches(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stickNext = n
}

// FailNextAPICalls makes the next n SetDesiredCapacity/TerminateInstance
// calls return ErrInjected without effect.
func (a *ASG) FailNextAPICalls(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failAPINext = n
}

// InServiceCount returns the number of IN_SERVICE instances; it lets the
// simulated metrics source close the loop.
func (a *ASG) InServiceCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reconcile(a.clock.Now())
	n := 0
	for _, in := range a.instances {
		if in.phase == phaseInService {
			n++
		}
	}
	return n
}

// LaunchAttempts returns how many launches the group has attempted.
func (a *ASG) LaunchAttempts() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.launchesTried
}

// DescribeCapacity returns the current capacity snapshot.
func (a *ASG) DescribeCapacity(ctx context.Context) (core.CapacitySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return core.CapacitySnapshot{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reconcile(a.clock.Now())

	snap := core.CapacitySnapshot{
		Known:     true,
		Desired:   a.desired,
		Min:       a.cfg.Min,
		Max:       a.cfg.Max,
		Instances: make([]core.Instance, 0, len(a.instances)),
	}
	for _, in := range a.instances {
		state := core.InstancePending
		switch in.phase {
		case phaseInService:
			state = core.InstanceInService
			snap.HealthyTargets++
		case phaseDraining:
			state = core.InstanceDraining
		}
		snap.Instances = append(snap.Instances, core.Instance{ID: in.id, AZ: in.az, State: state, LaunchedAt: in.launchedAt})
	}
	return snap, nil
}

// SetDesiredCapacity sets an absolute desired capacity within [Min, Max].
func (a *ASG) SetDesiredCapacity(ctx context.Context, desired int) (ports.ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return ports.ActionResult{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.injectedAPIFailure(); err != nil {
		return ports.ActionResult{}, err
	}
	if desired < a.cfg.Min || desired > a.cfg.Max {
		return ports.ActionResult{}, fmt.Errorf("%w: desired %d outside [%d, %d]", ErrValidation, desired, a.cfg.Min, a.cfg.Max)
	}
	now := a.clock.Now()
	a.reconcile(now)
	a.desired = desired
	a.lastAttempt = time.Time{} // a new desired value triggers an immediate attempt
	a.reconcile(now)
	return a.request(), nil
}

// TerminateInstance terminates one instance and decrements desired. Like
// the real API it refuses to go below Min.
func (a *ASG) TerminateInstance(ctx context.Context, instanceID string) (ports.ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return ports.ActionResult{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.injectedAPIFailure(); err != nil {
		return ports.ActionResult{}, err
	}
	now := a.clock.Now()
	a.reconcile(now)
	idx := slices.IndexFunc(a.instances, func(in *instance) bool { return in.id == instanceID })
	if idx < 0 {
		return ports.ActionResult{}, fmt.Errorf("%w: instance %s not in group", ErrValidation, instanceID)
	}
	if a.desired-1 < a.cfg.Min {
		return ports.ActionResult{}, fmt.Errorf("%w: terminating %s would violate min size %d", ErrValidation, instanceID, a.cfg.Min)
	}
	a.instances = slices.Delete(a.instances, idx, idx+1)
	a.desired--
	a.record(now, instanceID, false, ports.ActivitySuccessful, "terminate instance, desired capacity decremented")
	return a.request(), nil
}

// ScalingActivities returns activities started at or after since, oldest first.
func (a *ASG) ScalingActivities(ctx context.Context, since time.Time) ([]ports.ScalingActivity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reconcile(a.clock.Now())
	out := []ports.ScalingActivity{}
	for _, act := range a.activities {
		if !act.StartedAt.Before(since) {
			out = append(out, act)
		}
	}
	return out, nil
}

func (a *ASG) injectedAPIFailure() error {
	if a.failAPINext > 0 {
		a.failAPINext--
		return ErrInjected
	}
	return nil
}

func (a *ASG) request() ports.ActionResult {
	a.nextRequest++
	return ports.ActionResult{RequestID: fmt.Sprintf("sim-req-%04d", a.nextRequest)}
}

func (a *ASG) newInstance(now time.Time) *instance {
	a.nextInstance++
	in := &instance{
		id:         fmt.Sprintf("i-sim%04d", a.nextInstance),
		az:         a.cfg.AZs[(a.nextInstance-1)%len(a.cfg.AZs)],
		phase:      phaseLaunching,
		launchedAt: now,
		readyAt:    now.Add(a.cfg.Warmup),
	}
	a.instances = append(a.instances, in)
	return in
}

func (a *ASG) record(now time.Time, instanceID string, launch bool, status ports.ActivityStatus, desc string) {
	a.nextActivity++
	a.activities = append(a.activities, ports.ScalingActivity{
		ID:          fmt.Sprintf("sim-act-%04d", a.nextActivity),
		InstanceID:  instanceID,
		Launch:      launch,
		Status:      status,
		StartedAt:   now,
		Description: desc,
	})
}

// reconcile advances instance lifecycles to now and drives the group
// towards the desired capacity. The caller holds a.mu.
func (a *ASG) reconcile(now time.Time) {
	for _, in := range a.instances {
		if in.phase == phaseLaunching && !in.stuck && !now.Before(in.readyAt) {
			in.phase = phaseInService
		}
	}
	a.instances = slices.DeleteFunc(a.instances, func(in *instance) bool {
		return in.phase == phaseDraining && !now.Before(in.drainUntil)
	})

	live := 0
	for _, in := range a.instances {
		if in.phase != phaseDraining {
			live++
		}
	}

	for ; live > a.desired; live-- {
		a.removeOne(now)
	}
	if live < a.desired && (a.lastAttempt.IsZero() || !now.Before(a.lastAttempt.Add(a.cfg.LaunchRetryInterval))) {
		a.lastAttempt = now
		for ; live < a.desired; live++ {
			if !a.launch(now) {
				break // the group retries after LaunchRetryInterval
			}
		}
	}
}

// launch attempts one launch and reports whether it succeeded.
func (a *ASG) launch(now time.Time) bool {
	a.launchesTried++
	if a.failNext > 0 || now.Before(a.failUntil) {
		if a.failNext > 0 {
			a.failNext--
		}
		a.record(now, "", true, ports.ActivityFailed, "launching a new instance failed (injected)")
		return false
	}
	in := a.newInstance(now)
	if a.stickNext > 0 {
		a.stickNext--
		in.stuck = true
	}
	a.record(now, in.id, true, ports.ActivitySuccessful, "launching a new instance")
	return true
}

// removeOne removes a launching instance first (newest), otherwise moves the
// newest in-service instance to DRAINING.
func (a *ASG) removeOne(now time.Time) {
	for i := len(a.instances) - 1; i >= 0; i-- {
		if a.instances[i].phase == phaseLaunching {
			id := a.instances[i].id
			a.instances = slices.Delete(a.instances, i, i+1)
			a.record(now, id, false, ports.ActivitySuccessful, "cancel launching instance")
			return
		}
	}
	for i := len(a.instances) - 1; i >= 0; i-- {
		if a.instances[i].phase == phaseInService {
			a.instances[i].phase = phaseDraining
			a.instances[i].drainUntil = now.Add(a.cfg.DeregistrationDelay)
			a.record(now, a.instances[i].id, false, ports.ActivitySuccessful, "terminate instance after deregistration delay")
			return
		}
	}
}
