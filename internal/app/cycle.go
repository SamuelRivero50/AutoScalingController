package app

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

// cycle holds the working data of one evaluation cycle.
type cycle struct {
	c      *Controller
	id     int64
	now    time.Time
	events []ports.EventRecord
	// exhausted: the cycle budget ran out; no action may be executed.
	exhausted bool
}

func (cy *cycle) event(t ports.EventType, details map[string]any) {
	cy.events = append(cy.events, ports.EventRecord{Run: cy.c.run, TS: cy.now, Type: t, Details: details})
}

// budgetLeft reports whether the cycle may still call a port under ctx.
func (cy *cycle) budgetLeft(ctx context.Context) bool {
	if ctx.Err() != nil {
		cy.exhausted = true
	}
	return !cy.exhausted
}

func (cy *cycle) call(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, cy.c.cfg.CallTimeout)
}

// fetchFailed records a FETCH_FAILURE event, noting when the failure came
// from the exhausted budget.
func (cy *cycle) fetchFailed(ctx context.Context, source string, err error) {
	cy.budgetLeft(ctx)
	cy.event(ports.EventFetchFailure, map[string]any{
		"source":           source,
		"error":            err.Error(),
		"budget_exhausted": cy.exhausted,
	})
}

// Cycle runs one observe -> analyze -> decide -> act -> log cycle and always
// logs exactly one cycle record (docs/spec/lifecycle-and-failures.md §3).
// It returns an error only when the decision log could not be written.
func (c *Controller) Cycle(ctx context.Context) error {
	budget, cancel := context.WithTimeout(ctx, c.cfg.CycleBudget)
	defer cancel()
	cy := &cycle{c: c, id: c.state.NextCycle, now: c.deps.Clock.Now()}
	policy := c.cfg.Policy

	// Observe: capacity ground truth, provisioning outcomes, metrics.
	snap := cy.describeCapacity(budget)
	failed := cy.readActivities(budget)
	recovered := c.becameInService(snap)
	obs := cy.observe(budget, snap)
	if !cy.budgetLeft(budget) {
		cy.event(ports.EventFetchFailure, map[string]any{"source": "cycle_budget", "error": "cycle budget exhausted", "budget_exhausted": true})
	}

	// Analyze and decide (pure core).
	sig := core.Classify(obs, snap, c.state.Memory.LastConsumedCPU, policy)
	mem := core.Advance(c.state.Memory, cy.id, sig, snap, policy)
	prevBreaker := c.state.Breaker
	br := prevBreaker.Step(core.BreakerInput{Now: cy.now, Failed: failed, Recovered: recovered}, c.cfg.Breaker)
	decision := core.Decide(core.PolicyInput{
		CycleID: cy.id, Config: policy, Signals: sig, Capacity: snap, Memory: mem, Breaker: br.Status(),
	})

	// Act: at most one action.
	p := cy.plan(snap, decision, br)
	outcome := cy.execute(budget, p)
	if outcome.Status == ports.ActionOK {
		switch {
		case p.kind == planTerminateStuck && !failed && !recovered:
			// A stuck-pending termination is a provisioning failure, counted
			// at most once per cycle (docs/spec/lifecycle-and-failures.md §4).
			br = br.Step(core.BreakerInput{Now: cy.now, Failed: true}, c.cfg.Breaker)
		case p.kind == planDecision && decision.Decision == core.IncreaseCapacity:
			br = br.StartProbe()
		}
	}

	// Events.
	if br.Status().State != prevBreaker.Status().State {
		cy.event(ports.EventBreakerStateChange, map[string]any{
			"from":                 string(prevBreaker.Status().State),
			"to":                   string(br.Status().State),
			"consecutive_failures": br.ConsecutiveFailures,
		})
	}
	cy.instanceChanges(snap)
	for _, a := range decision.Alerts {
		switch a {
		case core.AlertBlind:
			cy.event(ports.EventBlindAlert, map[string]any{"consecutive_blind_cycles": mem.BlindStreak})
		case core.AlertZeroHealthyTargets:
			cy.event(ports.EventNoHealthyTargetsAlert, map[string]any{"in_service": snap.InService(), "desired": snap.Desired})
		}
	}

	// Persist memory, then log. Both run even if ctx was cancelled.
	c.state.Memory = mem
	c.state.Breaker = br
	c.state.NextCycle = cy.id + 1
	if snap.Known {
		c.state.LastInstances = slices.Clone(snap.Instances)
	}
	persist := context.WithoutCancel(ctx)
	sctx, scancel := context.WithTimeout(persist, c.cfg.CallTimeout)
	if err := c.deps.State.Save(sctx, c.State()); err != nil {
		c.logger.WarnContext(ctx, "state save failed; memory kept in process", "cycle", cy.id, "err", err)
	}
	scancel()

	return c.writeRecords(persist, cy, ports.CycleRecord{
		Run: c.run, CycleID: cy.id, TS: cy.now, Signals: sig,
		ScaleOut: mem.ScaleOut(policy), ScaleIn: mem.ScaleIn(policy),
		Capacity: recordedCapacity(snap, policy), Breaker: br.Status(),
		Decision: decision, Action: outcome,
	})
}

func (c *Controller) writeRecords(ctx context.Context, cy *cycle, rec ports.CycleRecord) error {
	var errs []error
	for _, e := range cy.events {
		if err := c.deps.Log.LogEvent(ctx, e); err != nil {
			errs = append(errs, err)
		}
	}
	if err := c.deps.Log.LogCycle(ctx, rec); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// recordedCapacity fills the bounds from the policy when capacity could not
// be read, so the record still shows the configured limits.
func recordedCapacity(snap core.CapacitySnapshot, policy core.PolicyConfig) core.CapacitySnapshot {
	if !snap.Known {
		snap.Min, snap.Max = policy.MinInstances, policy.MaxInstances
	}
	return snap
}

func (cy *cycle) describeCapacity(ctx context.Context) core.CapacitySnapshot {
	if !cy.budgetLeft(ctx) {
		return core.CapacitySnapshot{}
	}
	cctx, cancel := cy.call(ctx)
	defer cancel()
	snap, err := cy.c.deps.Provisioner.DescribeCapacity(cctx)
	if err != nil {
		cy.fetchFailed(ctx, "capacity", err)
		return core.CapacitySnapshot{}
	}
	return snap
}

// readActivities accounts new scaling activities and reports whether at
// least one new failed launch was seen. Each activity counts once
// (deduplicated by ID); in-progress activities are revisited later.
func (cy *cycle) readActivities(ctx context.Context) bool {
	if !cy.budgetLeft(ctx) {
		return false
	}
	st := &cy.c.state
	since := st.ActivityWatermark
	if floor := cy.now.Add(-cy.c.cfg.ActivityLookback); floor.After(since) {
		since = floor
	}
	cctx, cancel := cy.call(ctx)
	defer cancel()
	acts, err := cy.c.deps.Provisioner.ScalingActivities(cctx, since)
	if err != nil {
		cy.fetchFailed(ctx, "scaling_activities", err)
		return false
	}
	failed := false
	for _, act := range acts {
		if act.Status == ports.ActivityInProgress || slices.Contains(st.SeenActivities, act.ID) {
			continue
		}
		st.SeenActivities = append(st.SeenActivities, act.ID)
		if act.Launch && act.Status == ports.ActivityFailed {
			failed = true
		}
	}
	if n := len(st.SeenActivities); n > maxSeenActivities {
		st.SeenActivities = slices.Clone(st.SeenActivities[n-maxSeenActivities:])
	}
	return failed
}

// becameInService reports whether any instance transitioned to IN_SERVICE
// since the previous cycle.
func (c *Controller) becameInService(snap core.CapacitySnapshot) bool {
	if !snap.Known {
		return false
	}
	prev := make(map[string]core.InstanceState, len(c.state.LastInstances))
	for _, in := range c.state.LastInstances {
		prev[in.ID] = in.State
	}
	for _, in := range snap.Instances {
		if in.State == core.InstanceInService && prev[in.ID] != core.InstanceInService {
			return true
		}
	}
	return false
}

// observe fetches the metrics of the last closed period. Readings that were
// not read (fetch error or exhausted budget) are marked FetchFailed, so they
// classify as MISSING.
func (cy *cycle) observe(ctx context.Context, snap core.CapacitySnapshot) core.Observation {
	policy := cy.c.cfg.Policy
	end := cy.now.Add(-policy.MetricLag)
	period := ports.Period{Start: end.Add(-policy.AggregationPeriod), End: end}
	obs := core.Observation{Now: cy.now}

	var ids []string
	for _, in := range snap.Instances {
		if in.State == core.InstanceInService {
			ids = append(ids, in.ID)
		}
	}
	failedCPU := func() {
		obs.CPU = make([]core.InstanceCPU, 0, len(ids))
		for _, id := range ids {
			obs.CPU = append(obs.CPU, core.InstanceCPU{InstanceID: id, Reading: core.Reading{FetchFailed: true}})
		}
	}
	if cy.budgetLeft(ctx) {
		cctx, cancel := cy.call(ctx)
		cpu, err := cy.c.deps.Metrics.InstanceCPU(cctx, period, ids)
		cancel()
		if err != nil {
			cy.fetchFailed(ctx, "cpu", err)
			failedCPU()
		} else {
			obs.CPU = cpu
		}
	} else {
		failedCPU()
	}

	lb := ports.LoadBalancerReadings{}
	fetched := false
	if cy.budgetLeft(ctx) {
		cctx, cancel := cy.call(ctx)
		r, err := cy.c.deps.Metrics.LoadBalancer(cctx, period)
		cancel()
		if err != nil {
			cy.fetchFailed(ctx, "load_balancer", err)
		} else {
			lb, fetched = r, true
		}
	}
	if !fetched {
		f := core.Reading{FetchFailed: true}
		lb = ports.LoadBalancerReadings{
			RequestCount: f, TargetResponseTimeP95: f, Target5xxCount: f,
			ELB5xxCount: f, RequestCountPerTarget: f, HealthyHostCount: f,
		}
	}
	obs.RequestCount = lb.RequestCount
	obs.TargetResponseTimeP95 = lb.TargetResponseTimeP95
	obs.Target5xxCount = lb.Target5xxCount
	obs.ELB5xxCount = lb.ELB5xxCount
	obs.RequestCountPerTarget = lb.RequestCountPerTarget
	obs.HealthyHostCount = lb.HealthyHostCount
	return obs
}

// instanceChanges emits INSTANCE_STATE_CHANGE for every instance whose state
// differs from the previous cycle, including instances that disappeared.
func (cy *cycle) instanceChanges(snap core.CapacitySnapshot) {
	if !snap.Known {
		return
	}
	prev := make(map[string]core.InstanceState, len(cy.c.state.LastInstances))
	for _, in := range cy.c.state.LastInstances {
		prev[in.ID] = in.State
	}
	current := make(map[string]bool, len(snap.Instances))
	for _, in := range snap.Instances {
		current[in.ID] = true
		if from, ok := prev[in.ID]; !ok || from != in.State {
			fromState := any(nil)
			if ok {
				fromState = string(from)
			}
			cy.event(ports.EventInstanceStateChange, map[string]any{
				"instance_id": in.ID, "az": in.AZ, "from": fromState, "to": string(in.State),
			})
		}
	}
	for _, in := range cy.c.state.LastInstances {
		if !current[in.ID] {
			cy.event(ports.EventInstanceStateChange, map[string]any{
				"instance_id": in.ID, "az": in.AZ, "from": string(in.State), "to": string(core.InstanceTerminated),
			})
		}
	}
}
