package app

import (
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

// drainResult is the outcome of applying the drain timeout to one snapshot.
type drainResult struct {
	// effective is the snapshot the decision is made from: instances whose
	// drain timed out no longer count as DRAINING.
	effective core.CapacitySnapshot
	// drains is the updated tracking list to persist.
	drains []ports.Drain
	// expired lists the drains that timed out in this cycle.
	expired []ports.Drain
}

// applyDrainTimeout enforces the drain timeout
// (docs/spec/lifecycle-and-failures.md §4): an instance DRAINING for longer
// than timeout stops counting as DRAINING for the scale-in block. The ASG
// stays responsible for terminating it. An unknown snapshot leaves the
// tracking unchanged.
func applyDrainTimeout(prev []ports.Drain, snap core.CapacitySnapshot, now time.Time, timeout time.Duration) drainResult {
	if !snap.Known {
		return drainResult{effective: snap, drains: prev}
	}
	known := make(map[string]ports.Drain, len(prev))
	for _, d := range prev {
		known[d.InstanceID] = d
	}

	res := drainResult{effective: snap}
	res.effective.Instances = make([]core.Instance, 0, len(snap.Instances))
	for _, in := range snap.Instances {
		if in.State != core.InstanceDraining {
			res.effective.Instances = append(res.effective.Instances, in)
			continue
		}
		d, ok := known[in.ID]
		if !ok {
			d = ports.Drain{InstanceID: in.ID, Since: now}
		}
		if !d.Expired && now.Sub(d.Since) > timeout {
			d.Expired = true
			res.expired = append(res.expired, d)
		}
		res.drains = append(res.drains, d)
		if !d.Expired {
			res.effective.Instances = append(res.effective.Instances, in)
		}
	}
	return res
}
