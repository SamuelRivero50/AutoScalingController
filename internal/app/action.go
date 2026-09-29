package app

import (
	"context"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

type planKind int

const (
	planNone planKind = iota + 1
	planTerminateStuck
	planBreakerReset
	planDecision
	planSkip
)

// plan is the single action chosen for a cycle.
type plan struct {
	kind       planKind
	actionType core.ActionType
	target     int
	instanceID string
	skipReason string
}

// plan picks at most one action, first match wins
// (docs/spec/architecture.md §4.1, docs/spec/decision-log.md §5):
//  1. terminate the oldest stuck-pending instance
//  2. reset desired capacity when the breaker is open
//  3. the decision's own action (INCREASE subject to the breaker)
//
// An exhausted budget turns any intended action into SKIPPED.
func (cy *cycle) plan(snap core.CapacitySnapshot, d core.Decision, br core.Breaker) plan {
	p := cy.intended(snap, d, br)
	if cy.exhausted && p.kind != planNone {
		p.kind = planSkip
		p.skipReason = ports.SkipBudgetExhausted
	}
	return p
}

func (cy *cycle) intended(snap core.CapacitySnapshot, d core.Decision, br core.Breaker) plan {
	if snap.Known {
		if stuck, ok := cy.oldestStuck(snap); ok {
			return plan{kind: planTerminateStuck, actionType: core.ActionTerminateInstance, instanceID: stuck.ID}
		}
		if br.Status().State == core.BreakerOpen {
			floor := min(max(snap.Min, cy.c.cfg.Policy.MinInstances, snap.HealthyTargets), max(snap.Max, cy.c.cfg.Policy.MinInstances))
			if snap.Desired > floor {
				return plan{kind: planBreakerReset, actionType: core.ActionSetDesiredCapacity, target: floor}
			}
		}
	}

	switch d.Decision {
	case core.IncreaseCapacity:
		p := plan{kind: planDecision, actionType: core.ActionSetDesiredCapacity, target: d.Action.TargetDesired}
		if !br.AllowsScaleOut() {
			p.kind = planSkip
			p.skipReason = ports.SkipBreakerOpen
			if br.Status().State == core.BreakerHalfOpen {
				p.skipReason = ports.SkipBreakerProbeInFlight
			}
		}
		return p
	case core.ReduceCapacity:
		return plan{kind: planDecision, actionType: core.ActionSetDesiredCapacity, target: d.Action.TargetDesired}
	default:
		return plan{kind: planNone, actionType: core.ActionNone}
	}
}

// oldestStuck returns the PENDING instance launched longest ago beyond the
// pending timeout.
func (cy *cycle) oldestStuck(snap core.CapacitySnapshot) (core.Instance, bool) {
	var (
		oldest core.Instance
		found  bool
	)
	for _, in := range snap.Instances {
		if in.State != core.InstancePending || in.LaunchedAt.IsZero() {
			continue
		}
		if cy.now.Sub(in.LaunchedAt) <= cy.c.cfg.PendingTimeout {
			continue
		}
		if !found || in.LaunchedAt.Before(oldest.LaunchedAt) {
			oldest, found = in, true
		}
	}
	return oldest, found
}

// execute runs the planned action and describes its outcome.
func (cy *cycle) execute(ctx context.Context, p plan) ports.ActionOutcome {
	out := ports.ActionOutcome{Type: p.actionType, InstanceID: p.instanceID}
	if p.actionType == core.ActionSetDesiredCapacity {
		target := p.target
		out.TargetDesired = &target
	}
	switch p.kind {
	case planNone:
		out.Status = ports.ActionOK
		return out
	case planSkip:
		out.Status = ports.ActionSkipped
		out.SkipReason = p.skipReason
		return out
	}

	began := cy.c.deps.Clock.Now()
	var (
		res ports.ActionResult
		err error
	)
	if p.actionType == core.ActionTerminateInstance {
		res, err = cy.c.deps.Provisioner.TerminateInstance(ctx, p.instanceID)
	} else {
		res, err = cy.c.deps.Provisioner.SetDesiredCapacity(ctx, p.target)
	}
	elapsed := cy.c.deps.Clock.Now().Sub(began)
	out.Duration = &elapsed
	if err != nil {
		out.Status = ports.ActionError
		out.Error = err.Error()
		return out
	}
	out.Status = ports.ActionOK
	out.RequestID = res.RequestID
	return out
}
