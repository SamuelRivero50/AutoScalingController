package core

import (
	"testing"
	"time"
)

func TestBreaker_Step(t *testing.T) {
	cfg := DefaultBreakerConfig()
	fail := func(at time.Duration) BreakerInput { return BreakerInput{Now: t0.Add(at), Failed: true} }
	ok := func(at time.Duration) BreakerInput { return BreakerInput{Now: t0.Add(at), Recovered: true} }
	idle := func(at time.Duration) BreakerInput { return BreakerInput{Now: t0.Add(at)} }

	tests := []struct {
		name     string
		steps    []BreakerInput
		probe    int // index after which StartProbe is called, -1 for never
		want     BreakerState
		failures int
	}{
		{"zero value is closed", nil, -1, BreakerClosed, 0},
		{"two failures stay closed", []BreakerInput{fail(0), fail(time.Minute)}, -1, BreakerClosed, 2},
		{"three failures open", []BreakerInput{fail(0), fail(time.Minute), fail(2 * time.Minute)}, -1, BreakerOpen, 3},
		{"recovery resets the counter", []BreakerInput{fail(0), fail(time.Minute), ok(2 * time.Minute), fail(3 * time.Minute)}, -1, BreakerClosed, 1},
		{"idle cycles keep the counter", []BreakerInput{fail(0), idle(time.Minute), fail(2 * time.Minute)}, -1, BreakerClosed, 2},
		{"open before cool-off stays open", []BreakerInput{fail(0), fail(time.Minute), fail(2 * time.Minute), idle(11*time.Minute + 59*time.Second)}, -1, BreakerOpen, 3},
		{"cool-off elapsed goes half-open", []BreakerInput{fail(0), fail(time.Minute), fail(2 * time.Minute), idle(12 * time.Minute)}, -1, BreakerHalfOpen, 3},
		{"probe success closes", []BreakerInput{fail(0), fail(time.Minute), fail(2 * time.Minute), idle(12 * time.Minute), ok(13 * time.Minute)}, 3, BreakerClosed, 0},
		{"probe failure reopens", []BreakerInput{fail(0), fail(time.Minute), fail(2 * time.Minute), idle(12 * time.Minute), fail(13 * time.Minute)}, 3, BreakerOpen, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b Breaker
			for i, in := range tt.steps {
				b = b.Step(in, cfg)
				if i == tt.probe {
					b = b.StartProbe()
				}
			}
			if got := b.Status(); got.State != tt.want || got.ConsecutiveFailures != tt.failures {
				t.Fatalf("status = %+v, want %s with %d failures", got, tt.want, tt.failures)
			}
		})
	}
}

func TestBreaker_ReopenRestartsCoolOff(t *testing.T) {
	cfg := DefaultBreakerConfig()
	var b Breaker
	for i := range 3 {
		b = b.Step(BreakerInput{Now: t0.Add(time.Duration(i) * time.Minute), Failed: true}, cfg)
	}
	b = b.Step(BreakerInput{Now: t0.Add(12 * time.Minute)}, cfg).StartProbe()
	reopenedAt := t0.Add(13 * time.Minute)
	b = b.Step(BreakerInput{Now: reopenedAt, Failed: true}, cfg)
	if !b.OpenedAt.Equal(reopenedAt) {
		t.Fatalf("OpenedAt = %v, want %v", b.OpenedAt, reopenedAt)
	}
	if b = b.Step(BreakerInput{Now: reopenedAt.Add(9 * time.Minute)}, cfg); b.State != BreakerOpen {
		t.Fatalf("state = %s 9 minutes after reopening, want OPEN", b.State)
	}
}

func TestBreaker_AllowsScaleOut(t *testing.T) {
	cfg := DefaultBreakerConfig()
	var b Breaker
	if !b.AllowsScaleOut() {
		t.Fatal("closed breaker must allow scale-out")
	}
	for i := range 3 {
		b = b.Step(BreakerInput{Now: t0.Add(time.Duration(i) * time.Minute), Failed: true}, cfg)
	}
	if b.AllowsScaleOut() {
		t.Fatal("open breaker must block scale-out")
	}
	b = b.Step(BreakerInput{Now: t0.Add(20 * time.Minute)}, cfg)
	if !b.AllowsScaleOut() {
		t.Fatal("half-open breaker must allow exactly one probe")
	}
	b = b.StartProbe()
	if b.AllowsScaleOut() {
		t.Fatal("half-open breaker must block a second scale-out while the probe is in flight")
	}
}

func TestBreaker_StartProbeOutsideHalfOpenIsNoop(t *testing.T) {
	var b Breaker
	if b.StartProbe().ProbeInFlight {
		t.Fatal("StartProbe on a closed breaker must not set a probe")
	}
}

func TestBreaker_RecoveryWinsOverFailureInSameCycle(t *testing.T) {
	cfg := DefaultBreakerConfig()
	b := Breaker{ConsecutiveFailures: 2}
	b = b.Step(BreakerInput{Now: t0, Failed: true, Recovered: true}, cfg)
	if b.State != BreakerClosed || b.ConsecutiveFailures != 0 {
		t.Fatalf("got %+v, want closed with 0 failures", b.Status())
	}
}
