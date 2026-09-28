package core

import (
	"fmt"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

// fleet builds a known capacity snapshot with n in-service instances and
// the given extra instance states.
func fleet(n int, extra ...InstanceState) CapacitySnapshot {
	c := CapacitySnapshot{Known: true, Desired: n, Min: 1, Max: 5, HealthyTargets: n}
	for i := range n {
		c.Instances = append(c.Instances, Instance{ID: fmt.Sprintf("i-%d", i), AZ: "az-a", State: InstanceInService})
	}
	for j, s := range extra {
		c.Instances = append(c.Instances, Instance{ID: fmt.Sprintf("x-%d", j), AZ: "az-b", State: s})
		if s == InstancePending {
			c.Desired++
		}
	}
	return c
}

func present(v float64, ts time.Time) Reading {
	return Reading{Present: true, Value: v, Timestamp: ts}
}

// obsSpec describes one cycle's load in a compact form.
type obsSpec struct {
	cpu       float64
	latencyMs float64 // 0 = no latency datapoint
	requests  float64 // 0 = RequestCount absent (zero traffic)
	target5xx float64
	elb5xx    float64
}

// observe builds an observation where every in-service instance reports cpu
// for the last closed period (one aggregation period before now), so the
// datapoint is fresh under any profile.
func observe(now time.Time, cfg PolicyConfig, capacity CapacitySnapshot, s obsSpec) Observation {
	ts := now.Add(-cfg.AggregationPeriod)
	obs := Observation{Now: now}
	for _, id := range capacity.inServiceIDs() {
		obs.CPU = append(obs.CPU, InstanceCPU{InstanceID: id, Reading: present(s.cpu, ts)})
	}
	if s.requests > 0 {
		obs.RequestCount = present(s.requests, ts)
		obs.RequestCountPerTarget = present(s.requests/float64(max(1, capacity.InService())), ts)
	}
	if s.latencyMs > 0 {
		obs.TargetResponseTimeP95 = present(s.latencyMs, ts)
	}
	if s.target5xx > 0 {
		obs.Target5xxCount = present(s.target5xx, ts)
	}
	if s.elb5xx > 0 {
		obs.ELB5xxCount = present(s.elb5xx, ts)
	}
	obs.HealthyHostCount = present(float64(capacity.HealthyTargets), ts)
	return obs
}

// harness runs the Classify -> Advance -> Decide pipeline across cycles.
type harness struct {
	cfg   PolicyConfig
	mem   Memory
	cycle int64
	now   time.Time
}

func newHarness(cfg PolicyConfig) *harness {
	return &harness{cfg: cfg, now: t0}
}

func (h *harness) step(capacity CapacitySnapshot, obs Observation) Decision {
	h.cycle++
	sig := Classify(obs, capacity, h.mem.LastConsumedCPU, h.cfg)
	h.mem = Advance(h.mem, h.cycle, sig, capacity, h.cfg)
	return Decide(PolicyInput{
		CycleID:  h.cycle,
		Config:   h.cfg,
		Signals:  sig,
		Capacity: capacity,
		Memory:   h.mem,
		Breaker:  BreakerStatus{State: BreakerClosed},
	})
}

// load advances the clock one interval and runs a cycle with fresh data.
func (h *harness) load(capacity CapacitySnapshot, s obsSpec) Decision {
	h.now = h.now.Add(h.cfg.EvaluationInterval)
	return h.step(capacity, observe(h.now, h.cfg, capacity, s))
}

func requireReason(t *testing.T, d Decision, want ReasonCode) {
	t.Helper()
	if d.Reason != want {
		t.Fatalf("reason = %s, want %s", d.Reason, want)
	}
	kind, ok := want.Decision()
	if !ok || d.Decision != kind {
		t.Fatalf("decision = %s, want %s for reason %s", d.Decision, kind, want)
	}
}
