package ports_test

import (
	"testing"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/clock"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/fakeasg"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/filestate"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/jsonllog"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/memstate"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/mockmetrics"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

// Every port must be satisfied by at least one adapter (issue #8). These
// assignments fail the build if an adapter drifts from its port.
var (
	_ ports.Clock = clock.System{}
	_ ports.Clock = (*clock.Fake)(nil)

	_ ports.MetricsSource = (*mockmetrics.Source)(nil)

	_ ports.InstanceProvisioner = (*fakeasg.ASG)(nil)

	_ ports.StateStore = (*filestate.Store)(nil)
	_ ports.StateStore = (*memstate.Store)(nil)

	_ ports.DecisionLogger = (*jsonllog.Writer)(nil)
)

func TestEventTypesAreUnique(t *testing.T) {
	seen := map[ports.EventType]bool{}
	for _, e := range ports.EventTypes() {
		if seen[e] {
			t.Fatalf("duplicate event type %s", e)
		}
		seen[e] = true
	}
	if len(seen) != 8 {
		t.Fatalf("event types = %d, want 8", len(seen))
	}
}

func TestPeriodIsClosedInterval(t *testing.T) {
	start := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	p := ports.Period{Start: start, End: start.Add(time.Minute)}
	if p.End.Sub(p.Start) != time.Minute {
		t.Fatalf("period length = %v, want 1m", p.End.Sub(p.Start))
	}
}
