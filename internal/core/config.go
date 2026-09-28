package core

import (
	"errors"
	"fmt"
	"time"
)

// PolicyConfig holds every parameter the decision core needs
// (docs/spec/configuration.md §1, §2 and the blind-cycle threshold of §4).
// Units: CPU and error rate in percent, latency in milliseconds.
type PolicyConfig struct {
	Profile Profile

	EvaluationInterval time.Duration
	AggregationPeriod  time.Duration
	MetricLag          time.Duration

	ScaleOutM int // breaching cycles required ...
	ScaleOutN int // ... within the last N fresh cycles
	ScaleInN  int // comfortable cycles required, all of the last N

	MinRequestsPerPeriod float64

	ScaleOutCPU         float64 // CPU >= this overloads on its own (70)
	ScaleOutCPUWithSLO  float64 // CPU >= this overloads with an SLO breach (55)
	ScaleInProjectedCPU float64 // projected CPU on N-1 instances must be <= this (55)
	StepTargetCPU       float64 // divisor in ceil(N*CPU/target) - N (55)

	LatencySLOMs     float64 // p95 > this is an SLO breach (500)
	ErrorSLOPct      float64 // error rate > this is an SLO breach (1)
	LatencyComfortMs float64 // p95 <= this for scale-in (350)
	ErrorComfortPct  float64 // error rate <= this for scale-in (0.5)

	MinInstances       int
	MaxInstances       int
	MaxScaleOutStep    int // +2
	MaxSLOScaleOutStep int // +1 when only latency/errors triggered

	BlindAlertThreshold int
}

// RealisticConfig returns the realistic profile.
func RealisticConfig() PolicyConfig {
	c := baseConfig()
	c.Profile = ProfileRealistic
	c.EvaluationInterval = 60 * time.Second
	c.AggregationPeriod = 60 * time.Second
	c.MetricLag = 60 * time.Second
	c.ScaleInN = 5
	c.BlindAlertThreshold = 5
	return c
}

// DemoConfig returns the demo profile (compressed timings).
func DemoConfig() PolicyConfig {
	c := baseConfig()
	c.Profile = ProfileDemo
	c.EvaluationInterval = 10 * time.Second
	c.AggregationPeriod = 10 * time.Second
	c.MetricLag = 0
	c.ScaleInN = 3
	c.BlindAlertThreshold = 3
	return c
}

func baseConfig() PolicyConfig {
	return PolicyConfig{
		ScaleOutM:            2,
		ScaleOutN:            3,
		MinRequestsPerPeriod: 20,
		ScaleOutCPU:          70,
		ScaleOutCPUWithSLO:   55,
		ScaleInProjectedCPU:  55,
		StepTargetCPU:        55,
		LatencySLOMs:         500,
		ErrorSLOPct:          1,
		LatencyComfortMs:     350,
		ErrorComfortPct:      0.5,
		MinInstances:         1,
		MaxInstances:         5,
		MaxScaleOutStep:      2,
		MaxSLOScaleOutStep:   1,
	}
}

// Validate reports every inconsistent parameter.
func (c PolicyConfig) Validate() error {
	var errs []error
	check := func(ok bool, format string, args ...any) {
		if !ok {
			errs = append(errs, fmt.Errorf(format, args...))
		}
	}

	check(c.Profile == ProfileDemo || c.Profile == ProfileRealistic, "unknown profile %q", c.Profile)
	check(c.EvaluationInterval > 0, "evaluation interval must be positive")
	check(c.AggregationPeriod > 0, "aggregation period must be positive")
	check(c.MetricLag >= 0, "metric lag must not be negative")
	check(c.ScaleOutM >= 1 && c.ScaleOutM <= c.ScaleOutN, "scale-out window needs 1 <= M <= N, got %d of %d", c.ScaleOutM, c.ScaleOutN)
	check(c.ScaleInN >= 1, "scale-in window must be at least 1, got %d", c.ScaleInN)
	check(c.MinRequestsPerPeriod >= 0, "min requests per period must not be negative")
	check(c.StepTargetCPU > 0, "step target CPU must be positive")
	check(c.ScaleOutCPUWithSLO <= c.ScaleOutCPU, "SLO-confirmed CPU floor must not exceed the CPU trigger")
	check(c.ScaleInProjectedCPU < c.ScaleOutCPU, "scale-in bound must be below the scale-out trigger (dead-band)")
	check(c.LatencyComfortMs <= c.LatencySLOMs, "latency comfort must not exceed the latency SLO")
	check(c.ErrorComfortPct <= c.ErrorSLOPct, "error comfort must not exceed the error SLO")
	check(c.MinInstances >= 1, "min instances must be at least 1, got %d", c.MinInstances)
	check(c.MaxInstances >= c.MinInstances, "max instances %d below min %d", c.MaxInstances, c.MinInstances)
	check(c.MaxScaleOutStep >= 1, "max scale-out step must be at least 1")
	check(c.MaxSLOScaleOutStep >= 1 && c.MaxSLOScaleOutStep <= c.MaxScaleOutStep, "SLO scale-out step must be in [1, max step]")
	check(c.BlindAlertThreshold >= 1, "blind alert threshold must be at least 1")

	return errors.Join(errs...)
}

// staleAfter is the age beyond which a datapoint is STALE:
// 2 × expected period + metric lag.
func (c PolicyConfig) staleAfter() time.Duration {
	return 2*c.AggregationPeriod + c.MetricLag
}

// windowCapacity is how many counted cycles Memory must retain.
func (c PolicyConfig) windowCapacity() int {
	return max(c.ScaleOutN, c.ScaleInN)
}
