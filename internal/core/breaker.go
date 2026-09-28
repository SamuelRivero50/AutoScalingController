package core

import "time"

// BreakerConfig parameterizes the provisioning circuit breaker
// (docs/spec/lifecycle-and-failures.md §4).
type BreakerConfig struct {
	// Threshold is the number of consecutive cycles with a provisioning
	// failure that opens the breaker.
	Threshold int
	// CoolOff is how long the breaker stays OPEN before allowing one probe.
	CoolOff time.Duration
}

// DefaultBreakerConfig returns the configured values, identical for the
// realistic and demo profiles (ADR-0004).
func DefaultBreakerConfig() BreakerConfig {
	return BreakerConfig{Threshold: 3, CoolOff: 10 * time.Minute}
}

// Breaker is the circuit-breaker state carried between cycles. Its zero
// value is a CLOSED breaker with no failures.
type Breaker struct {
	State               BreakerState
	ConsecutiveFailures int
	OpenedAt            time.Time
	// ProbeInFlight is true between the probe scale-out and its outcome.
	ProbeInFlight bool
}

// BreakerInput is what one cycle observed about provisioning.
type BreakerInput struct {
	Now time.Time
	// Failed: at least one provisioning failure this cycle (a new failed
	// launch activity, or a stuck-pending termination). Counted once per
	// cycle however many failures were seen.
	Failed bool
	// Recovered: at least one instance became IN_SERVICE this cycle.
	Recovered bool
}

// state normalizes the zero value to CLOSED.
func (b Breaker) state() BreakerState {
	if b.State == "" {
		return BreakerClosed
	}
	return b.State
}

// Status is the breaker view recorded in the decision log.
func (b Breaker) Status() BreakerStatus {
	return BreakerStatus{State: b.state(), ConsecutiveFailures: b.ConsecutiveFailures}
}

// AllowsScaleOut reports whether an INCREASE decision may be executed now:
// always when CLOSED, once (as the probe) when HALF_OPEN, never when OPEN.
func (b Breaker) AllowsScaleOut() bool {
	switch b.state() {
	case BreakerClosed:
		return true
	case BreakerHalfOpen:
		return !b.ProbeInFlight
	default:
		return false
	}
}

// StartProbe marks the HALF_OPEN probe as executed. It is a no-op in any
// other state.
func (b Breaker) StartProbe() Breaker {
	if b.state() == BreakerHalfOpen {
		b.ProbeInFlight = true
	}
	return b
}

// Step applies one cycle's provisioning outcome and the passage of time.
// Recovery wins over failure within the same cycle, since an instance that
// became healthy proves provisioning works.
func (b Breaker) Step(in BreakerInput, cfg BreakerConfig) Breaker {
	b.State = b.state()

	if b.State == BreakerOpen && !in.Now.Before(b.OpenedAt.Add(cfg.CoolOff)) {
		b.State = BreakerHalfOpen
		b.ProbeInFlight = false
	}

	switch {
	case in.Recovered:
		b.ConsecutiveFailures = 0
		if b.State == BreakerHalfOpen {
			b.State = BreakerClosed
			b.ProbeInFlight = false
		}
	case in.Failed:
		b.ConsecutiveFailures++
		switch {
		case b.State == BreakerHalfOpen:
			b = b.open(in.Now)
		case b.State == BreakerClosed && b.ConsecutiveFailures >= cfg.Threshold:
			b = b.open(in.Now)
		}
	}
	return b
}

func (b Breaker) open(now time.Time) Breaker {
	b.State = BreakerOpen
	b.OpenedAt = now
	b.ProbeInFlight = false
	return b
}
