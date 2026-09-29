// Package mockmetrics implements the MetricsSource port as a seeded,
// deterministic, closed-loop metrics generator for the simulator
// (docs/spec/simulator.md).
//
// Load is expressed in "instance units": a load of 1.0 fully saturates one
// instance. Per-instance CPU is load/N×100 (plus an idle floor and seeded
// noise), where N is the current in-service count, so adding instances
// lowers CPU exactly as in a real load-balanced fleet.
//
// Every reading is a pure function of (seed, instance ID, period start), so
// the sequence does not depend on call order or on how many times a period
// is read.
package mockmetrics

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

var _ ports.MetricsSource = (*Source)(nil)

// ErrOutage is returned while a simulated metrics outage is active.
var ErrOutage = errors.New("mockmetrics: simulated metrics outage")

// Fleet reports the current in-service instance count.
type Fleet interface {
	InServiceCount() int
	// InServiceCountAt returns the number of instances that were in service
	// at some point during [start, end]. It is used to compute CPU and
	// latency for a closed aggregation period, ensuring that a period
	// observed before new capacity arrived does not benefit from it.
	// Implementations that do not track history may return InServiceCount().
	InServiceCountAt(start, end time.Time) int
}

// Profile describes one scenario's load over time. Every function receives
// the elapsed time between the scenario start and the period start. Nil
// functions take their documented default.
type Profile struct {
	// Load in instance units (required).
	Load func(elapsed time.Duration) float64
	// NoiseStdDev is the standard deviation of per-instance CPU noise, in
	// CPU percentage points.
	NoiseStdDev float64
	// IdleCPU is the CPU floor of an idle instance (default 2%).
	IdleCPU float64
	// RequestsPerUnit is the request count per period for a load of 1.0
	// (default 1000).
	RequestsPerUnit float64
	// Traffic reports whether any request arrives (default: always).
	Traffic func(elapsed time.Duration) bool
	// LatencyOverlayMs adds latency independent of CPU, e.g. a slow
	// downstream dependency (default: none).
	LatencyOverlayMs func(elapsed time.Duration) float64
	// ErrorRates returns the target and ELB 5xx rates as fractions of the
	// requests (default: none).
	ErrorRates func(elapsed time.Duration) (target, elb float64)
	// Outage reports a metrics outage: every fetch fails (default: never).
	Outage func(elapsed time.Duration) bool
}

// Source is the generator. It is safe for concurrent use: it holds no
// mutable state.
type Source struct {
	seed    uint64
	start   time.Time
	profile Profile
	fleet   Fleet
}

// New returns a generator for one scenario run.
func New(seed uint64, start time.Time, p Profile, fleet Fleet) *Source {
	if p.IdleCPU == 0 {
		p.IdleCPU = 2
	}
	if p.RequestsPerUnit == 0 {
		p.RequestsPerUnit = 1000
	}
	return &Source{seed: seed, start: start, profile: p, fleet: fleet}
}

func (s *Source) elapsed(p ports.Period) time.Duration {
	return p.Start.Sub(s.start)
}

func (s *Source) outage(p ports.Period) bool {
	return s.profile.Outage != nil && s.profile.Outage(s.elapsed(p))
}

// load returns the scenario load for the period (never negative).
func (s *Source) load(p ports.Period) float64 {
	return max(0, s.profile.Load(s.elapsed(p)))
}

// meanCPU is the noise-free per-instance CPU for the period, using the
// number of instances in service during that period (not the current count).
func (s *Source) meanCPU(p ports.Period, n int) float64 {
	if n <= 0 {
		return 100
	}
	return min(100, s.profile.IdleCPU+s.load(p)/float64(n)*100)
}

// InstanceCPU returns one CPU reading per requested instance.
func (s *Source) InstanceCPU(ctx context.Context, p ports.Period, instanceIDs []string) ([]core.InstanceCPU, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.outage(p) {
		return nil, ErrOutage
	}
	// Use the in-service count during this period, not the current count.
	n := s.fleet.InServiceCountAt(p.Start, p.End)
	mean := s.meanCPU(p, n)
	out := make([]core.InstanceCPU, 0, len(instanceIDs))
	for _, id := range instanceIDs {
		v := mean + s.noise(id, p)*s.profile.NoiseStdDev
		out = append(out, core.InstanceCPU{
			InstanceID: id,
			Reading:    core.Reading{Present: true, Value: min(100, max(0, v)), Timestamp: p.Start},
		})
	}
	return out, nil
}

// LoadBalancer returns the ALB readings for the period.
func (s *Source) LoadBalancer(ctx context.Context, p ports.Period) (ports.LoadBalancerReadings, error) {
	if err := ctx.Err(); err != nil {
		return ports.LoadBalancerReadings{}, err
	}
	if s.outage(p) {
		return ports.LoadBalancerReadings{}, ErrOutage
	}
	// Current healthy count for the HealthyHostCount gauge.
	n := s.fleet.InServiceCount()
	// Period-based count for CPU-driven latency.
	np := s.fleet.InServiceCountAt(p.Start, p.End)
	at := func(v float64) core.Reading { return core.Reading{Present: true, Value: v, Timestamp: p.Start} }
	r := ports.LoadBalancerReadings{HealthyHostCount: at(float64(n))}

	elapsed := s.elapsed(p)
	if s.profile.Traffic != nil && !s.profile.Traffic(elapsed) {
		return r, nil // ALB publishes no request, latency or 5xx datapoints
	}
	requests := math.Round(s.load(p) * s.profile.RequestsPerUnit)
	if requests <= 0 {
		return r, nil
	}
	r.RequestCount = at(requests)
	r.RequestCountPerTarget = at(requests / float64(max(1, n)))
	r.TargetResponseTimeP95 = at(latencyMs(s.meanCPU(p, np)) + s.overlay(elapsed))

	if s.profile.ErrorRates != nil {
		target, elb := s.profile.ErrorRates(elapsed)
		if c := math.Round(target * requests); c > 0 {
			r.Target5xxCount = at(c)
		}
		if c := math.Round(elb * requests); c > 0 {
			r.ELB5xxCount = at(c)
		}
	}
	return r, nil
}

func (s *Source) overlay(elapsed time.Duration) float64 {
	if s.profile.LatencyOverlayMs == nil {
		return 0
	}
	return max(0, s.profile.LatencyOverlayMs(elapsed))
}

// latencyMs models CPU-bound p95 latency: flat at 80ms, rising
// quadratically above 60% CPU to 680ms at 100%.
func latencyMs(cpu float64) float64 {
	excess := max(0, (cpu-60)/40)
	return 80 + 600*excess*excess
}

// noise returns a standard-normal sample determined only by the seed, the
// instance ID and the period start.
func (s *Source) noise(instanceID string, p ports.Period) float64 {
	h := fnv.New64a()
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], s.seed)
	_, _ = h.Write(buf[:]) // hash.Hash.Write never returns an error
	_, _ = h.Write([]byte(instanceID))
	binary.LittleEndian.PutUint64(buf[:], uint64(p.Start.UnixNano()))
	_, _ = h.Write(buf[:])
	sum := h.Sum64()
	return rand.New(rand.NewPCG(sum, sum^0x9e3779b97f4a7c15)).NormFloat64()
}
