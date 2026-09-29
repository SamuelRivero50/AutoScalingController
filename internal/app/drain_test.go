package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/fakeasg"
	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

func TestApplyDrainTimeout(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	const timeout = 90 * time.Second
	inService := core.Instance{ID: "i-a", State: core.InstanceInService}
	draining := core.Instance{ID: "i-b", State: core.InstanceDraining}
	snap := func(ins ...core.Instance) core.CapacitySnapshot {
		return core.CapacitySnapshot{Known: true, Desired: 1, Instances: ins}
	}
	tests := []struct {
		name          string
		prev          []ports.Drain
		snap          core.CapacitySnapshot
		wantInstances []core.Instance
		wantDrains    []ports.Drain
		wantExpired   []ports.Drain
	}{
		{
			name:          "new drain starts tracking",
			snap:          snap(inService, draining),
			wantInstances: []core.Instance{inService, draining},
			wantDrains:    []ports.Drain{{InstanceID: "i-b", Since: now}},
		},
		{
			name:          "drain at the timeout still counts",
			prev:          []ports.Drain{{InstanceID: "i-b", Since: now.Add(-timeout)}},
			snap:          snap(inService, draining),
			wantInstances: []core.Instance{inService, draining},
			wantDrains:    []ports.Drain{{InstanceID: "i-b", Since: now.Add(-timeout)}},
		},
		{
			name:          "drain past the timeout expires once",
			prev:          []ports.Drain{{InstanceID: "i-b", Since: now.Add(-timeout - time.Second)}},
			snap:          snap(inService, draining),
			wantInstances: []core.Instance{inService},
			wantDrains:    []ports.Drain{{InstanceID: "i-b", Since: now.Add(-timeout - time.Second), Expired: true}},
			wantExpired:   []ports.Drain{{InstanceID: "i-b", Since: now.Add(-timeout - time.Second), Expired: true}},
		},
		{
			name:          "expired drain is not reported again",
			prev:          []ports.Drain{{InstanceID: "i-b", Since: now.Add(-time.Hour), Expired: true}},
			snap:          snap(inService, draining),
			wantInstances: []core.Instance{inService},
			wantDrains:    []ports.Drain{{InstanceID: "i-b", Since: now.Add(-time.Hour), Expired: true}},
		},
		{
			name:          "finished drain stops tracking",
			prev:          []ports.Drain{{InstanceID: "i-b", Since: now.Add(-time.Minute)}},
			snap:          snap(inService),
			wantInstances: []core.Instance{inService},
		},
		{
			name:          "unknown snapshot keeps tracking",
			prev:          []ports.Drain{{InstanceID: "i-b", Since: now.Add(-time.Minute)}},
			snap:          core.CapacitySnapshot{},
			wantInstances: nil,
			wantDrains:    []ports.Drain{{InstanceID: "i-b", Since: now.Add(-time.Minute)}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := applyDrainTimeout(tt.prev, tt.snap, now, timeout)
			if len(got.effective.Instances) != len(tt.wantInstances) || (len(tt.wantInstances) > 0 && !reflect.DeepEqual(got.effective.Instances, tt.wantInstances)) {
				t.Errorf("effective instances = %+v, want %+v", got.effective.Instances, tt.wantInstances)
			}
			if len(got.drains) != len(tt.wantDrains) || (len(tt.wantDrains) > 0 && !reflect.DeepEqual(got.drains, tt.wantDrains)) {
				t.Errorf("drains = %+v, want %+v", got.drains, tt.wantDrains)
			}
			if len(got.expired) != len(tt.wantExpired) || (len(tt.wantExpired) > 0 && !reflect.DeepEqual(got.expired, tt.wantExpired)) {
				t.Errorf("expired = %+v, want %+v", got.expired, tt.wantExpired)
			}
			if got.effective.Desired != tt.snap.Desired || got.effective.Known != tt.snap.Known {
				t.Errorf("effective header changed: %+v", got.effective)
			}
		})
	}
}

func TestController_DrainTimeout(t *testing.T) {
	t.Parallel()
	r := newRig(t, withLoad(0.2), withProvisioner(func(a *fakeasg.ASG) ports.InstanceProvisioner { return hungDrain{a} }))
	r.start(t)
	r.cycles(t, 5) // realistic: 60s interval, 90s drain timeout

	r.log.mu.Lock()
	var draining []int
	for _, c := range r.log.cycles {
		draining = append(draining, c.Capacity.Draining())
	}
	r.log.mu.Unlock()
	if want := []int{1, 1, 0, 0, 0}; !reflect.DeepEqual(draining, want) {
		t.Errorf("recorded draining per cycle = %v, want %v", draining, want)
	}

	var expired []ports.EventRecord
	for _, e := range r.log.eventsOf(ports.EventInstanceStateChange) {
		if e.Details["drain_timeout_expired"] == true {
			expired = append(expired, e)
		}
	}
	if len(expired) != 1 {
		t.Fatalf("drain expiry events = %d, want 1", len(expired))
	}
	if expired[0].Details["instance_id"] != "i-hung" || expired[0].Details["az"] != "az-a" {
		t.Errorf("expiry event details = %v", expired[0].Details)
	}
	if st := r.ctrl.State(); len(st.Drains) != 1 || !st.Drains[0].Expired {
		t.Errorf("persisted drains = %+v, want one expired", st.Drains)
	}
}
