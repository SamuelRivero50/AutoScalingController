package core

import "time"

// Instance is one application instance as seen by the controller.
type Instance struct {
	ID         string
	AZ         string
	State      InstanceState
	LaunchedAt time.Time // zero if unknown
}

// CapacitySnapshot is the capacity ground truth for one cycle, read from the
// ASG and target-health APIs (never from CloudWatch).
type CapacitySnapshot struct {
	// Known is false when capacity could not be read this cycle.
	Known          bool
	Desired        int
	Min            int
	Max            int
	HealthyTargets int
	Instances      []Instance
}

func (c CapacitySnapshot) count(state InstanceState) int {
	n := 0
	for _, in := range c.Instances {
		if in.State == state {
			n++
		}
	}
	return n
}

// InService is N, the in-service instance count used by the policy.
func (c CapacitySnapshot) InService() int { return c.count(InstanceInService) }

// Pending is the number of instances still launching.
func (c CapacitySnapshot) Pending() int { return c.count(InstancePending) }

// Draining is the number of instances being deregistered.
func (c CapacitySnapshot) Draining() int { return c.count(InstanceDraining) }

// ScaleOutInFlight reports whether added capacity is not yet in service:
// an instance is PENDING, or desired is above in-service and nothing has
// launched yet.
func (c CapacitySnapshot) ScaleOutInFlight() bool {
	return c.Pending() > 0 || c.Desired > c.InService()
}

// ScaleInInFlight reports whether removed capacity is still draining, or a
// lower desired capacity has not been applied yet.
func (c CapacitySnapshot) ScaleInInFlight() bool {
	return c.Draining() > 0 || c.Desired < c.InService()
}

// inServiceIDs returns the in-service instance IDs in snapshot order.
func (c CapacitySnapshot) inServiceIDs() []string {
	ids := make([]string, 0, len(c.Instances))
	for _, in := range c.Instances {
		if in.State == InstanceInService {
			ids = append(ids, in.ID)
		}
	}
	return ids
}

// BreakerStatus is the provisioning circuit-breaker status. The core does
// not change its decision based on it; the shell skips actions while the
// breaker is open (docs/spec/decision-log.md §2).
type BreakerStatus struct {
	State               BreakerState
	ConsecutiveFailures int
}
