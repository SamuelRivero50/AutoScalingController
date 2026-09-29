package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
)

// Config is the resolved controller configuration: the decision policy plus
// the shell timeouts of docs/spec/configuration.md §3.
type Config struct {
	Policy  core.PolicyConfig
	Breaker core.BreakerConfig

	// Warmup is the estimated launch-to-healthy time.
	Warmup time.Duration
	// PendingTimeout bounds how long an instance may stay PENDING.
	PendingTimeout time.Duration
	// DeregistrationDelay and DrainTimeout describe draining (the drain
	// timeout is enforced with the real adapters, Milestone 3).
	DeregistrationDelay time.Duration
	DrainTimeout        time.Duration
	// CycleBudget bounds a whole cycle; every port call is bounded only by
	// the remaining budget (docs/spec/lifecycle-and-failures.md §3).
	CycleBudget time.Duration
	// AWS request retry policy, applied by the real AWS adapters: each
	// attempt times out after AttemptTimeout, and up to MaxRetries retries
	// follow with exponential backoff 1s, 2s, 4s ... capped at
	// RetryMaxBackoff.
	AttemptTimeout  time.Duration
	MaxRetries      int
	RetryMaxBackoff time.Duration
	// ActivityLookback bounds how far back scaling activities are read.
	ActivityLookback time.Duration
}

// RealisticConfig returns the realistic profile.
func RealisticConfig() Config {
	return Config{
		Policy:              core.RealisticConfig(),
		Breaker:             core.DefaultBreakerConfig(),
		Warmup:              180 * time.Second,
		PendingTimeout:      360 * time.Second,
		DeregistrationDelay: 60 * time.Second,
		DrainTimeout:        90 * time.Second,
		CycleBudget:         40 * time.Second,
		AttemptTimeout:      10 * time.Second,
		MaxRetries:          3,
		RetryMaxBackoff:     30 * time.Second,
		ActivityLookback:    30 * time.Minute,
	}
}

// DemoConfig returns the demo profile.
func DemoConfig() Config {
	return Config{
		Policy:              core.DemoConfig(),
		Breaker:             core.DefaultBreakerConfig(),
		Warmup:              20 * time.Second,
		PendingTimeout:      45 * time.Second,
		DeregistrationDelay: 10 * time.Second,
		DrainTimeout:        40 * time.Second,
		CycleBudget:         10 * time.Second,
		AttemptTimeout:      10 * time.Second,
		MaxRetries:          3,
		RetryMaxBackoff:     30 * time.Second,
		ActivityLookback:    30 * time.Minute,
	}
}

// ConfigForProfile returns the named profile.
func ConfigForProfile(p core.Profile) (Config, error) {
	switch p {
	case core.ProfileRealistic:
		return RealisticConfig(), nil
	case core.ProfileDemo:
		return DemoConfig(), nil
	default:
		return Config{}, fmt.Errorf("unknown profile %q", p)
	}
}

// Validate reports every inconsistent parameter.
func (c Config) Validate() error {
	errs := []error{c.Policy.Validate()}
	check := func(ok bool, msg string) {
		if !ok {
			errs = append(errs, errors.New(msg))
		}
	}
	check(c.Breaker.Threshold >= 1, "breaker threshold must be at least 1")
	check(c.Breaker.CoolOff > 0, "breaker cool-off must be positive")
	check(c.Warmup > 0, "warmup must be positive")
	check(c.PendingTimeout > c.Warmup, "pending timeout must exceed warmup")
	check(c.AttemptTimeout > 0, "attempt timeout must be positive")
	check(c.MaxRetries >= 0, "max retries must not be negative")
	check(c.RetryMaxBackoff > 0, "retry max backoff must be positive")
	check(c.CycleBudget > 0 && c.CycleBudget <= c.Policy.EvaluationInterval, "cycle budget must be positive and fit in the evaluation interval")
	check(c.ActivityLookback > 0, "activity lookback must be positive")
	return errors.Join(errs...)
}

// hashDTO is the canonical, tagged encoding of the configuration used for
// config_hash (docs/spec/configuration.md §5). Durations are milliseconds.
type hashDTO struct {
	Profile              core.Profile `json:"profile"`
	EvaluationIntervalMS int64        `json:"evaluation_interval_ms"`
	AggregationPeriodMS  int64        `json:"aggregation_period_ms"`
	MetricLagMS          int64        `json:"metric_lag_ms"`
	ScaleOutM            int          `json:"scale_out_m"`
	ScaleOutN            int          `json:"scale_out_n"`
	ScaleInN             int          `json:"scale_in_n"`
	MinRequestsPerPeriod float64      `json:"min_requests_per_period"`
	ScaleOutCPU          float64      `json:"scale_out_cpu"`
	ScaleOutCPUWithSLO   float64      `json:"scale_out_cpu_with_slo"`
	ScaleInProjectedCPU  float64      `json:"scale_in_projected_cpu"`
	StepTargetCPU        float64      `json:"step_target_cpu"`
	LatencySLOMs         float64      `json:"latency_slo_ms"`
	ErrorSLOPct          float64      `json:"error_slo_pct"`
	LatencyComfortMs     float64      `json:"latency_comfort_ms"`
	ErrorComfortPct      float64      `json:"error_comfort_pct"`
	MinInstances         int          `json:"min_instances"`
	MaxInstances         int          `json:"max_instances"`
	MaxScaleOutStep      int          `json:"max_scale_out_step"`
	MaxSLOScaleOutStep   int          `json:"max_slo_scale_out_step"`
	BlindAlertThreshold  int          `json:"blind_alert_threshold"`
	BreakerThreshold     int          `json:"breaker_threshold"`
	BreakerCoolOffMS     int64        `json:"breaker_cool_off_ms"`
	WarmupMS             int64        `json:"warmup_ms"`
	PendingTimeoutMS     int64        `json:"pending_timeout_ms"`
	DeregistrationMS     int64        `json:"deregistration_delay_ms"`
	DrainTimeoutMS       int64        `json:"drain_timeout_ms"`
	CycleBudgetMS        int64        `json:"cycle_budget_ms"`
	AttemptTimeoutMS     int64        `json:"attempt_timeout_ms"`
	MaxRetries           int          `json:"max_retries"`
	RetryMaxBackoffMS    int64        `json:"retry_max_backoff_ms"`
	ActivityLookbackMS   int64        `json:"activity_lookback_ms"`
}

// Hash returns the SHA-256 (hex) of the canonical encoding of the resolved
// configuration: equal parameters always give the same hash, and any
// parameter change gives a different one.
func (c Config) Hash() string {
	p := c.Policy
	d := hashDTO{
		Profile:              p.Profile,
		EvaluationIntervalMS: p.EvaluationInterval.Milliseconds(),
		AggregationPeriodMS:  p.AggregationPeriod.Milliseconds(),
		MetricLagMS:          p.MetricLag.Milliseconds(),
		ScaleOutM:            p.ScaleOutM,
		ScaleOutN:            p.ScaleOutN,
		ScaleInN:             p.ScaleInN,
		MinRequestsPerPeriod: p.MinRequestsPerPeriod,
		ScaleOutCPU:          p.ScaleOutCPU,
		ScaleOutCPUWithSLO:   p.ScaleOutCPUWithSLO,
		ScaleInProjectedCPU:  p.ScaleInProjectedCPU,
		StepTargetCPU:        p.StepTargetCPU,
		LatencySLOMs:         p.LatencySLOMs,
		ErrorSLOPct:          p.ErrorSLOPct,
		LatencyComfortMs:     p.LatencyComfortMs,
		ErrorComfortPct:      p.ErrorComfortPct,
		MinInstances:         p.MinInstances,
		MaxInstances:         p.MaxInstances,
		MaxScaleOutStep:      p.MaxScaleOutStep,
		MaxSLOScaleOutStep:   p.MaxSLOScaleOutStep,
		BlindAlertThreshold:  p.BlindAlertThreshold,
		BreakerThreshold:     c.Breaker.Threshold,
		BreakerCoolOffMS:     c.Breaker.CoolOff.Milliseconds(),
		WarmupMS:             c.Warmup.Milliseconds(),
		PendingTimeoutMS:     c.PendingTimeout.Milliseconds(),
		DeregistrationMS:     c.DeregistrationDelay.Milliseconds(),
		DrainTimeoutMS:       c.DrainTimeout.Milliseconds(),
		CycleBudgetMS:        c.CycleBudget.Milliseconds(),
		AttemptTimeoutMS:     c.AttemptTimeout.Milliseconds(),
		MaxRetries:           c.MaxRetries,
		RetryMaxBackoffMS:    c.RetryMaxBackoff.Milliseconds(),
		ActivityLookbackMS:   c.ActivityLookback.Milliseconds(),
	}
	data, err := json.Marshal(d)
	if err != nil {
		// hashDTO holds only numbers and strings; Marshal cannot fail.
		panic(fmt.Sprintf("app: encode config hash: %v", err))
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
