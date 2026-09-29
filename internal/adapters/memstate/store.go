// Package memstate implements the StateStore port in memory, for the
// simulator and tests.
package memstate

import (
	"context"
	"slices"
	"sync"

	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

var _ ports.StateStore = (*Store)(nil)

// Store keeps the last saved state. Its zero value is an empty store that
// returns ports.ErrStateNotFound. It is safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	state    ports.State
	hasState bool
	// failLoad makes the next Load report corrupt state (tests the rebuild path).
	failLoad bool
}

// Load returns a copy of the last saved state.
func (s *Store) Load(ctx context.Context) (ports.State, error) {
	if err := ctx.Err(); err != nil {
		return ports.State{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failLoad {
		s.failLoad = false
		return ports.State{}, ports.ErrStateCorrupt
	}
	if !s.hasState {
		return ports.State{}, ports.ErrStateNotFound
	}
	return clone(s.state), nil
}

// Save stores a copy of st.
func (s *Store) Save(ctx context.Context, st ports.State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = clone(st)
	s.hasState = true
	return nil
}

// CorruptNextLoad makes the next Load return ports.ErrStateCorrupt.
func (s *Store) CorruptNextLoad() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failLoad = true
}

// clone deep-copies the slices so callers cannot alias stored state.
func clone(st ports.State) ports.State {
	st.Memory.Entries = slices.Clone(st.Memory.Entries)
	st.SeenActivities = slices.Clone(st.SeenActivities)
	st.LastInstances = slices.Clone(st.LastInstances)
	st.Drains = slices.Clone(st.Drains)
	return st
}
