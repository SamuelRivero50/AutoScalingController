package fakeasg

import (
	"errors"
	"testing"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/clock"
	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

var t0 = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

func newGroup(desired int) (*ASG, *clock.Fake) {
	c := clock.NewFake(t0)
	return New(c, Config{
		Min: 1, Max: 5, InitialDesired: desired,
		Warmup: 3 * time.Minute, DeregistrationDelay: time.Minute, LaunchRetryInterval: 30 * time.Second,
	}), c
}

func describe(t *testing.T, a *ASG) core.CapacitySnapshot {
	t.Helper()
	snap, err := a.DescribeCapacity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func requireCounts(t *testing.T, snap core.CapacitySnapshot, desired, inService, pending, draining int) {
	t.Helper()
	got := [4]int{snap.Desired, snap.InService(), snap.Pending(), snap.Draining()}
	want := [4]int{desired, inService, pending, draining}
	if got != want {
		t.Fatalf("desired/in-service/pending/draining = %v, want %v", got, want)
	}
}

func TestNew(t *testing.T) {
	a, _ := newGroup(2)
	snap := describe(t, a)
	requireCounts(t, snap, 2, 2, 0, 0)
	if snap.HealthyTargets != 2 || snap.Min != 1 || snap.Max != 5 || !snap.Known {
		t.Fatalf("snapshot = %+v", snap)
	}
	if snap.Instances[0].AZ == snap.Instances[1].AZ {
		t.Fatal("instances must be spread across zones")
	}
}

func TestASG_ScaleOutWarmup(t *testing.T) {
	a, c := newGroup(1)
	if _, err := a.SetDesiredCapacity(t.Context(), 3); err != nil {
		t.Fatal(err)
	}
	requireCounts(t, describe(t, a), 3, 1, 2, 0)
	c.Advance(3*time.Minute - time.Second)
	requireCounts(t, describe(t, a), 3, 1, 2, 0)
	c.Advance(time.Second)
	snap := describe(t, a)
	requireCounts(t, snap, 3, 3, 0, 0)
	if snap.HealthyTargets != 3 {
		t.Fatalf("healthy = %d, want 3", snap.HealthyTargets)
	}
}

func TestASG_SetDesiredIsAbsoluteAndIdempotent(t *testing.T) {
	a, c := newGroup(1)
	for range 3 {
		if _, err := a.SetDesiredCapacity(t.Context(), 2); err != nil {
			t.Fatal(err)
		}
	}
	c.Advance(5 * time.Minute)
	requireCounts(t, describe(t, a), 2, 2, 0, 0)
	if n := a.LaunchAttempts(); n != 1 {
		t.Fatalf("launch attempts = %d, want 1 (repeating an absolute value must not add instances)", n)
	}
}

func TestASG_SetDesiredOutOfBounds(t *testing.T) {
	a, _ := newGroup(1)
	for _, d := range []int{0, 6, -1} {
		if _, err := a.SetDesiredCapacity(t.Context(), d); !errors.Is(err, ErrValidation) {
			t.Fatalf("SetDesiredCapacity(%d) = %v, want ErrValidation", d, err)
		}
	}
	requireCounts(t, describe(t, a), 1, 1, 0, 0)
}

func TestASG_ScaleInDrains(t *testing.T) {
	a, c := newGroup(3)
	if _, err := a.SetDesiredCapacity(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	requireCounts(t, describe(t, a), 2, 2, 0, 1)
	c.Advance(time.Minute)
	requireCounts(t, describe(t, a), 2, 2, 0, 0)
}

func TestASG_ScaleInRemovesLaunchingFirst(t *testing.T) {
	a, _ := newGroup(1)
	if _, err := a.SetDesiredCapacity(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetDesiredCapacity(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	requireCounts(t, describe(t, a), 1, 1, 0, 0)
}

func TestASG_FailNextLaunchesAndRetry(t *testing.T) {
	a, c := newGroup(1)
	a.FailNextLaunches(2)
	if _, err := a.SetDesiredCapacity(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	requireCounts(t, describe(t, a), 2, 1, 0, 0)

	c.Advance(10 * time.Second) // before the retry interval: no new attempt
	describe(t, a)
	if n := a.LaunchAttempts(); n != 1 {
		t.Fatalf("attempts = %d before retry interval, want 1", n)
	}
	c.Advance(20 * time.Second) // retry #2 fails
	describe(t, a)
	c.Advance(30 * time.Second) // retry #3 succeeds
	requireCounts(t, describe(t, a), 2, 1, 1, 0)

	acts, err := a.ScalingActivities(t.Context(), t0)
	if err != nil {
		t.Fatal(err)
	}
	var failed, ok int
	for _, act := range acts {
		switch {
		case act.Launch && act.Status == ports.ActivityFailed:
			failed++
		case act.Launch && act.Status == ports.ActivitySuccessful:
			ok++
		}
	}
	if failed != 2 || ok != 1 {
		t.Fatalf("failed/successful launches = %d/%d, want 2/1", failed, ok)
	}
}

func TestASG_FailLaunchesUntil(t *testing.T) {
	a, c := newGroup(1)
	a.FailLaunchesUntil(t0.Add(2 * time.Minute))
	if _, err := a.SetDesiredCapacity(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		c.Advance(30 * time.Second)
		describe(t, a)
	}
	requireCounts(t, describe(t, a), 2, 1, 1, 0)
}

func TestASG_StuckLaunchStaysPending(t *testing.T) {
	a, c := newGroup(1)
	a.StickNextLaunches(1)
	if _, err := a.SetDesiredCapacity(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	c.Advance(time.Hour)
	snap := describe(t, a)
	requireCounts(t, snap, 2, 1, 1, 0)

	var stuck core.Instance
	for _, in := range snap.Instances {
		if in.State == core.InstancePending {
			stuck = in
		}
	}
	if !stuck.LaunchedAt.Equal(t0) {
		t.Fatalf("stuck LaunchedAt = %v, want %v", stuck.LaunchedAt, t0)
	}
	if _, err := a.TerminateInstance(t.Context(), stuck.ID); err != nil {
		t.Fatal(err)
	}
	requireCounts(t, describe(t, a), 1, 1, 0, 0)
}

func TestASG_TerminateInstance(t *testing.T) {
	a, _ := newGroup(1)
	snap := describe(t, a)
	if _, err := a.TerminateInstance(t.Context(), snap.Instances[0].ID); !errors.Is(err, ErrValidation) {
		t.Fatalf("terminating below min = %v, want ErrValidation", err)
	}
	if _, err := a.TerminateInstance(t.Context(), "i-missing"); !errors.Is(err, ErrValidation) {
		t.Fatalf("unknown instance = %v, want ErrValidation", err)
	}
}

func TestASG_FailNextAPICalls(t *testing.T) {
	a, _ := newGroup(1)
	a.FailNextAPICalls(1)
	if _, err := a.SetDesiredCapacity(t.Context(), 2); !errors.Is(err, ErrInjected) {
		t.Fatalf("SetDesiredCapacity = %v, want ErrInjected", err)
	}
	requireCounts(t, describe(t, a), 1, 1, 0, 0)
	if _, err := a.SetDesiredCapacity(t.Context(), 2); err != nil {
		t.Fatalf("second call = %v, want success", err)
	}
}

func TestASG_ScalingActivitiesSince(t *testing.T) {
	a, c := newGroup(1)
	if _, err := a.SetDesiredCapacity(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	c.Advance(time.Hour)
	if _, err := a.SetDesiredCapacity(t.Context(), 3); err != nil {
		t.Fatal(err)
	}
	acts, err := a.ScalingActivities(t.Context(), t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 1 || !acts[0].StartedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("activities since = %+v, want only the later launch", acts)
	}
}

func TestASG_CancelledContext(t *testing.T) {
	a, _ := newGroup(1)
	ctx, cancel := cancelled(t)
	defer cancel()
	if _, err := a.DescribeCapacity(ctx); err == nil {
		t.Fatal("DescribeCapacity must honor ctx")
	}
	if _, err := a.SetDesiredCapacity(ctx, 2); err == nil {
		t.Fatal("SetDesiredCapacity must honor ctx")
	}
	if _, err := a.TerminateInstance(ctx, "x"); err == nil {
		t.Fatal("TerminateInstance must honor ctx")
	}
	if _, err := a.ScalingActivities(ctx, t0); err == nil {
		t.Fatal("ScalingActivities must honor ctx")
	}
}

func TestASG_InServiceCount(t *testing.T) {
	a, c := newGroup(2)
	if _, err := a.SetDesiredCapacity(t.Context(), 3); err != nil {
		t.Fatal(err)
	}
	if n := a.InServiceCount(); n != 2 {
		t.Fatalf("in service = %d, want 2 while launching", n)
	}
	c.Advance(3 * time.Minute)
	if n := a.InServiceCount(); n != 3 {
		t.Fatalf("in service = %d, want 3 after warmup", n)
	}
}
