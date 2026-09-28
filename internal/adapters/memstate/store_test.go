package memstate

import (
	"errors"
	"testing"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

func TestStore_Load(t *testing.T) {
	var s Store
	if _, err := s.Load(t.Context()); !errors.Is(err, ports.ErrStateNotFound) {
		t.Fatalf("empty Load = %v, want ErrStateNotFound", err)
	}
	st := ports.State{RunID: "r", SeenActivities: []string{"a"}, Memory: core.Memory{Entries: []core.WindowEntry{{CycleID: 1}}}}
	if err := s.Save(t.Context(), st); err != nil {
		t.Fatal(err)
	}
	st.SeenActivities[0] = "mutated"
	got, err := s.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.SeenActivities[0] != "a" {
		t.Fatal("Save must copy slices")
	}
	got.Memory.Entries[0].CycleID = 99
	again, _ := s.Load(t.Context())
	if again.Memory.Entries[0].CycleID != 1 {
		t.Fatal("Load must return a copy")
	}
}

func TestStore_CorruptNextLoad(t *testing.T) {
	var s Store
	if err := s.Save(t.Context(), ports.State{RunID: "r"}); err != nil {
		t.Fatal(err)
	}
	s.CorruptNextLoad()
	if _, err := s.Load(t.Context()); !errors.Is(err, ports.ErrStateCorrupt) {
		t.Fatalf("Load = %v, want ErrStateCorrupt", err)
	}
	if _, err := s.Load(t.Context()); err != nil {
		t.Fatalf("second Load = %v, want the saved state", err)
	}
}
