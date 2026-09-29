package core

import (
	"slices"
	"time"
)

// WindowEntry is one counted (fresh) cycle in the evaluation windows.
type WindowEntry struct {
	CycleID     int64
	CPUValid    bool
	CPU         float64
	Overloaded  bool
	Comfortable bool
	Trigger     Trigger
}

// Memory is the controller's own transient memory, persisted by the
// StateStore between cycles: evaluation windows, the last capacity seen
// (for window reset), the CPU dedup marker and the blind streak.
type Memory struct {
	Entries []WindowEntry // oldest first, at most max(ScaleOutN, ScaleInN)

	HasLastCapacity      bool
	LastDesired          int
	LastInService        int
	HadScaleOutInFlight  bool // true if the previous cycle had pending instances

	// LastCapacityChangedAt records the cycle time of the most recent
	// in_service increase. Used with HadScaleOutInFlight for the
	// stale-period guard (docs/spec/decision-policy.md §3).
	LastCapacityChangedAt time.Time

	LastConsumedCPU time.Time
	BlindStreak     int
}

// Advance returns the memory after accounting for this cycle. It never
// mutates prev.
//
//   - Any change of (desired, in-service) since the previous cycle resets both
//     windows and records the cycle time as LastCapacityChangedAt.
//   - The cycle is counted only if capacity is known, no scale-out is in
//     flight, CPU is not NO_NEW_DATA, and the CPU datapoint's period_start is
//     not before LastCapacityChangedAt (stale-period rule,
//     docs/spec/decision-policy.md §3).
func Advance(prev Memory, cycleID int64, now time.Time, sig Signals, capacity CapacitySnapshot, cfg PolicyConfig) Memory {
	next := prev
	next.Entries = slices.Clone(prev.Entries)

	if sig.Blind() {
		next.BlindStreak = prev.BlindStreak + 1
	} else {
		next.BlindStreak = 0
	}

	if !capacity.Known {
		return next
	}

	inService := capacity.InService()
	if prev.HasLastCapacity && (prev.LastDesired != capacity.Desired || prev.LastInService != inService) {
		next.Entries = nil
		if inService > prev.LastInService {
			next.LastCapacityChangedAt = now
		}
	}
	next.HasLastCapacity = true
	next.LastDesired = capacity.Desired
	next.LastInService = inService

	inFlight := capacity.ScaleOutInFlight()
	if inFlight || sig.CPU.Quality == QualityNoNewData {
		next.HadScaleOutInFlight = true
		return next
	}

	// Stale-period guard: the cycle right after scale-out in flight completes
	// (HadScaleOutInFlight=true, now ScaleOutInFlight=false) may carry a CPU
	// datapoint whose period_start precedes the new instances' arrival time.
	// That reading used the smaller fleet's CPU and must not count.
	// Keep discarding until we see a non-stale datapoint.
	if prev.HadScaleOutInFlight &&
		!next.LastCapacityChangedAt.IsZero() &&
		sig.CPU.PeriodStart.Before(next.LastCapacityChangedAt) {
		next.HadScaleOutInFlight = true // still stale; keep guard armed
		return next
	}
	next.HadScaleOutInFlight = false
	// Clear the timestamp once we've accepted a fresh post-scaleout datapoint.
	next.LastCapacityChangedAt = time.Time{}

	a := Assess(sig, capacity, cfg)
	next.Entries = append(next.Entries, WindowEntry{
		CycleID:     cycleID,
		CPUValid:    a.CPUValid,
		CPU:         a.CPU,
		Overloaded:  a.Overloaded,
		Comfortable: a.Comfortable,
		Trigger:     a.Trigger,
	})
	if keep := cfg.windowCapacity(); len(next.Entries) > keep {
		next.Entries = slices.Clone(next.Entries[len(next.Entries)-keep:])
	}

	if sig.CPU.Valid() {
		next.LastConsumedCPU = sig.CPU.DatapointTS
	}
	return next
}

// ScaleOutWindow is the state of the M-of-N scale-out window.
type ScaleOutWindow struct {
	M, N      int
	Breaching []int64 // cycle IDs of overloaded entries among the last N
	Satisfied bool
	// Latest is the most recent overloaded entry (valid if Satisfied).
	Latest WindowEntry
}

// ScaleInWindow is the state of the N-of-N scale-in window.
type ScaleInWindow struct {
	N           int
	Comfortable []int64 // cycle IDs of comfortable entries among the last N
	Satisfied   bool
	// Blocked: a non-VALID CPU reading is inside the window.
	Blocked bool
}

func lastN(entries []WindowEntry, n int) []WindowEntry {
	if len(entries) <= n {
		return entries
	}
	return entries[len(entries)-n:]
}

// ScaleOut evaluates the scale-out window. Breaching cycles need not be
// consecutive; right after a reset M overloaded entries are enough.
func (m Memory) ScaleOut(cfg PolicyConfig) ScaleOutWindow {
	w := ScaleOutWindow{M: cfg.ScaleOutM, N: cfg.ScaleOutN, Breaching: []int64{}}
	for _, e := range lastN(m.Entries, cfg.ScaleOutN) {
		if e.Overloaded {
			w.Breaching = append(w.Breaching, e.CycleID)
			w.Latest = e
		}
	}
	w.Satisfied = len(w.Breaching) >= cfg.ScaleOutM
	return w
}

// ScaleIn evaluates the scale-in window: the last N counted cycles must all
// be comfortable with VALID CPU.
func (m Memory) ScaleIn(cfg PolicyConfig) ScaleInWindow {
	w := ScaleInWindow{N: cfg.ScaleInN, Comfortable: []int64{}}
	window := lastN(m.Entries, cfg.ScaleInN)
	for _, e := range window {
		if e.Comfortable {
			w.Comfortable = append(w.Comfortable, e.CycleID)
		}
		if !e.CPUValid {
			w.Blocked = true
		}
	}
	w.Satisfied = len(window) == cfg.ScaleInN && len(w.Comfortable) == cfg.ScaleInN
	return w
}
