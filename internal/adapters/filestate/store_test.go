package filestate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

var t0 = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

func sampleState() ports.State {
	return ports.State{
		RunID:     "run-1",
		NextCycle: 42,
		Memory: core.Memory{
			Entries: []core.WindowEntry{
				{CycleID: 40, CPUValid: true, CPU: 81, Overloaded: true, Trigger: core.TriggerCPU},
				{CycleID: 41, CPUValid: true, CPU: 20, Comfortable: true},
			},
			HasLastCapacity: true, LastDesired: 3, LastInService: 3,
			LastConsumedCPU: t0.Add(-time.Minute), BlindStreak: 2,
		},
		Breaker:           core.Breaker{State: core.BreakerOpen, ConsecutiveFailures: 3, OpenedAt: t0, ProbeInFlight: false},
		ActivityWatermark: t0.Add(-time.Hour),
		SeenActivities:    []string{"act-1", "act-2"},
		LastInstances:     []core.Instance{{ID: "i-1", AZ: "az-a", State: core.InstanceInService, LaunchedAt: t0}},
		Drains:            []ports.Drain{{InstanceID: "i-2", Since: t0.Add(-2 * time.Minute), Expired: true}},
	}
}

// TestStore_LoadWithoutDrains keeps files written before drain tracking
// was added loadable.
func TestStore_LoadWithoutDrains(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"run_id":"r","next_cycle":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := New(path).Load(t.Context())
	if err != nil {
		t.Fatalf("Load = %v, want nil", err)
	}
	if got.RunID != "r" || got.NextCycle != 3 || len(got.Drains) != 0 {
		t.Fatalf("Load = %+v", got)
	}
}

func TestStore_SaveLoadRoundTrip(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "sub", "state.json"))
	want := sampleState()
	if err := s.Save(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestStore_LoadMissingFile(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "state.json"))
	if _, err := s.Load(t.Context()); !errors.Is(err, ports.ErrStateNotFound) {
		t.Fatalf("Load = %v, want ErrStateNotFound", err)
	}
}

func TestStore_LoadCorruptFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"truncated json", `{"version":1,"run_id":"r`},
		{"not json", "garbage"},
		{"empty file", ""},
		{"unknown version", `{"version":99}`},
		{"unknown field", `{"version":1,"surprise":true}`},
		{"wrong type", `{"version":1,"next_cycle":"many"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := New(path).Load(t.Context())
			if !errors.Is(err, ports.ErrStateCorrupt) {
				t.Fatalf("Load = %v, want ErrStateCorrupt", err)
			}
		})
	}
}

func TestStore_SaveReplacesAtomicallyAndLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	s := New(path)
	first := sampleState()
	if err := s.Save(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	second := sampleState()
	second.NextCycle = 99
	if err := s.Save(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.NextCycle != 99 {
		t.Fatalf("NextCycle = %d, want 99", got.NextCycle)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory holds %v, want only state.json", names)
	}
}

func TestStore_SaveFailureKeepsPreviousState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	s := New(path)
	if err := s.Save(t.Context(), sampleState()); err != nil {
		t.Fatal(err)
	}
	// Make the target a directory's child that cannot be replaced: point a
	// second store at a path whose parent is a regular file.
	blocked := New(filepath.Join(path, "nested.json"))
	if err := blocked.Save(t.Context(), sampleState()); err == nil {
		t.Fatal("expected Save to fail when the parent is a file")
	}
	if _, err := s.Load(t.Context()); err != nil {
		t.Fatalf("previous state lost after a failed save: %v", err)
	}
}

func TestStore_ContextCancelled(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "state.json"))
	ctx, cancel := contextWithCancel(t)
	cancel()
	if err := s.Save(ctx, sampleState()); err == nil {
		t.Fatal("Save with cancelled ctx must fail")
	}
	if _, err := s.Load(ctx); err == nil {
		t.Fatal("Load with cancelled ctx must fail")
	}
}
