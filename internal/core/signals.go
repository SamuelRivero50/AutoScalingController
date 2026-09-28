package core

import (
	"math"
	"time"
)

// Signal names and units, as recorded in observation.signals[].
const (
	SignalCPU                   = "CPUUtilization"
	SignalLatencyP95            = "TargetResponseTime"
	SignalErrorRate             = "ErrorRate5xx"
	SignalRequestCount          = "RequestCount"
	SignalRequestCountPerTarget = "RequestCountPerTarget"
	SignalHealthyHostCount      = "HealthyHostCount"

	UnitPercent      = "Percent"
	UnitMilliseconds = "Milliseconds"
	UnitCount        = "Count"
)

// Reading is one raw metric datapoint for one closed period, as returned by a
// MetricsSource adapter. Present=false with FetchFailed=false means the
// metric published no datapoint for the period.
type Reading struct {
	Present     bool
	FetchFailed bool
	Value       float64
	Timestamp   time.Time // start of the aggregation period
}

// InstanceCPU is the CPU reading of one instance.
type InstanceCPU struct {
	InstanceID string
	Reading    Reading
}

// Observation is everything the metrics source returned for one cycle.
type Observation struct {
	Now                   time.Time // cycle time, from the Clock port
	CPU                   []InstanceCPU
	RequestCount          Reading
	TargetResponseTimeP95 Reading // milliseconds
	Target5xxCount        Reading
	ELB5xxCount           Reading
	RequestCountPerTarget Reading
	HealthyHostCount      Reading
}

// Signal is one classified signal.
type Signal struct {
	Name        string
	Unit        string
	Quality     Quality
	HasValue    bool
	Value       float64
	DatapointTS time.Time // zero means null
	PeriodStart time.Time
	PeriodEnd   time.Time
}

// Valid reports whether the signal can be used by the policy.
func (s Signal) Valid() bool { return s.Quality == QualityValid }

// Signals is the classified view of one cycle's observation.
type Signals struct {
	CPU                   Signal
	LatencyP95            Signal
	ErrorRate             Signal
	RequestCount          Signal
	RequestCountPerTarget Signal
	HealthyHostCount      Signal

	// Error-rate components (counts), kept to attribute an error-triggered
	// scale-out to load-balancer or application errors.
	Target5xx float64
	ELB5xx    float64
}

// List returns the signals in log order.
func (s Signals) List() []Signal {
	return []Signal{s.CPU, s.LatencyP95, s.ErrorRate, s.RequestCount, s.RequestCountPerTarget, s.HealthyHostCount}
}

// Blind reports a blind cycle: CPU is unusable (MISSING, STALE or
// ANOMALOUS). Latency and errors only confirm the CPU trigger, so their
// quality does not matter (docs/spec/lifecycle-and-failures.md §2).
func (s Signals) Blind() bool {
	return s.CPU.Quality.Unusable()
}

// Classify assigns a quality to every signal of the observation. It is pure:
// the cycle time comes from obs.Now and the dedup marker from lastConsumedCPU.
func Classify(obs Observation, capacity CapacitySnapshot, lastConsumedCPU time.Time, cfg PolicyConfig) Signals {
	rc := classifyRequestCount(obs, SignalRequestCount, obs.RequestCount, cfg)
	sig := Signals{
		CPU:                   classifyCPU(obs, capacity, lastConsumedCPU, cfg),
		RequestCount:          rc,
		RequestCountPerTarget: classifyRequestCount(obs, SignalRequestCountPerTarget, obs.RequestCountPerTarget, cfg),
		HealthyHostCount:      classifyGauge(obs, SignalHealthyHostCount, UnitCount, obs.HealthyHostCount, nonNegative, cfg),
	}
	sig.LatencyP95 = classifyLatency(obs, rc, cfg)
	sig.ErrorRate, sig.Target5xx, sig.ELB5xx = classifyErrorRate(obs, rc, cfg)
	return sig
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func nonNegative(v float64) bool { return finite(v) && v >= 0 }

func percentRange(v float64) bool { return finite(v) && v >= 0 && v <= 100 }

func (c PolicyConfig) isStale(ts, now time.Time) bool {
	return now.Sub(ts) > c.staleAfter()
}

func newSignal(name, unit string, q Quality) Signal {
	return Signal{Name: name, Unit: unit, Quality: q}
}

func (s Signal) withDatapoint(v float64, ts time.Time, cfg PolicyConfig) Signal {
	s.HasValue = true
	s.Value = v
	s.DatapointTS = ts
	s.PeriodStart = ts
	s.PeriodEnd = ts.Add(cfg.AggregationPeriod)
	return s
}

// readingQuality classifies a present reading: ANOMALOUS, STALE or VALID.
func readingQuality(r Reading, now time.Time, inRange func(float64) bool, cfg PolicyConfig) Quality {
	switch {
	case !inRange(r.Value):
		return QualityAnomalous
	case cfg.isStale(r.Timestamp, now):
		return QualityStale
	default:
		return QualityValid
	}
}

// classifyGauge classifies a metric whose absence is a real gap.
func classifyGauge(obs Observation, name, unit string, r Reading, inRange func(float64) bool, cfg PolicyConfig) Signal {
	if r.FetchFailed || !r.Present {
		return newSignal(name, unit, QualityMissing)
	}
	s := newSignal(name, unit, readingQuality(r, obs.Now, inRange, cfg))
	if !finite(r.Value) {
		return s
	}
	return s.withDatapoint(r.Value, r.Timestamp, cfg)
}

// classifyRequestCount treats an absent datapoint as zero traffic:
// NOT_EVALUABLE with value 0, never MISSING.
func classifyRequestCount(obs Observation, name string, r Reading, cfg PolicyConfig) Signal {
	if !r.FetchFailed && !r.Present {
		s := newSignal(name, UnitCount, QualityNotEvaluable)
		s.HasValue = true
		return s
	}
	return classifyGauge(obs, name, UnitCount, r, nonNegative, cfg)
}

// evaluability returns the quality latency/error must take given the request
// count, and whether they can be evaluated at all.
func evaluability(rc Signal, cfg PolicyConfig) (Quality, bool) {
	switch {
	case rc.Quality.Unusable():
		return QualityMissing, false
	case rc.Quality != QualityValid || rc.Value < cfg.MinRequestsPerPeriod:
		return QualityNotEvaluable, false
	default:
		return QualityValid, true
	}
}

func classifyLatency(obs Observation, rc Signal, cfg PolicyConfig) Signal {
	q, ok := evaluability(rc, cfg)
	if !ok {
		s := newSignal(SignalLatencyP95, UnitMilliseconds, q)
		if obs.TargetResponseTimeP95.Present && finite(obs.TargetResponseTimeP95.Value) {
			s = s.withDatapoint(obs.TargetResponseTimeP95.Value, obs.TargetResponseTimeP95.Timestamp, cfg)
		}
		return s
	}
	return classifyGauge(obs, SignalLatencyP95, UnitMilliseconds, obs.TargetResponseTimeP95, nonNegative, cfg)
}

// countOrZero returns an error counter value; an absent counter while
// RequestCount is present means zero errors.
func countOrZero(r Reading, now time.Time, cfg PolicyConfig) (float64, Quality) {
	switch {
	case r.FetchFailed:
		return 0, QualityMissing
	case !r.Present:
		return 0, QualityValid
	default:
		return r.Value, readingQuality(r, now, nonNegative, cfg)
	}
}

func classifyErrorRate(obs Observation, rc Signal, cfg PolicyConfig) (Signal, float64, float64) {
	q, ok := evaluability(rc, cfg)
	if !ok {
		return newSignal(SignalErrorRate, UnitPercent, q), 0, 0
	}

	target, tq := countOrZero(obs.Target5xxCount, obs.Now, cfg)
	elb, eq := countOrZero(obs.ELB5xxCount, obs.Now, cfg)
	for _, cq := range []Quality{tq, eq} {
		if cq != QualityValid {
			return newSignal(SignalErrorRate, UnitPercent, cq), target, elb
		}
	}

	rate := (target + elb) / rc.Value * 100
	s := newSignal(SignalErrorRate, UnitPercent, QualityValid)
	if !percentRange(rate) {
		s.Quality = QualityAnomalous
	}
	return s.withDatapoint(rate, rc.DatapointTS, cfg), target, elb
}

// classifyCPU aggregates per-instance CPU across in-service instances.
func classifyCPU(obs Observation, capacity CapacitySnapshot, lastConsumed time.Time, cfg PolicyConfig) Signal {
	ids := capacity.inServiceIDs()
	if len(ids) == 0 {
		return newSignal(SignalCPU, UnitPercent, QualityMissing)
	}

	byID := make(map[string]Reading, len(obs.CPU))
	for _, c := range obs.CPU {
		byID[c.InstanceID] = c.Reading
	}

	var (
		sum                     float64
		usable                  int
		missing, stale, anomaly int
		newest                  time.Time
	)
	for _, id := range ids {
		r, ok := byID[id]
		if !ok || r.FetchFailed || !r.Present {
			missing++
			continue
		}
		switch readingQuality(r, obs.Now, percentRange, cfg) {
		case QualityAnomalous:
			anomaly++
		case QualityStale:
			stale++
		default:
			usable++
			sum += r.Value
			if r.Timestamp.After(newest) {
				newest = r.Timestamp
			}
		}
	}

	if 2*usable < len(ids) {
		q := QualityMissing
		switch {
		case missing == 0 && anomaly == 0:
			q = QualityStale
		case missing == 0 && stale == 0:
			q = QualityAnomalous
		}
		return newSignal(SignalCPU, UnitPercent, q)
	}

	s := newSignal(SignalCPU, UnitPercent, QualityValid).withDatapoint(sum/float64(usable), newest, cfg)
	if !lastConsumed.IsZero() && !newest.After(lastConsumed) {
		s.Quality = QualityNoNewData
	}
	return s
}
