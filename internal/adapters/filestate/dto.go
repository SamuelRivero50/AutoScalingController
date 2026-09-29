package filestate

import (
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

// formatVersion changes whenever the on-disk layout changes; a mismatch is
// treated as corrupt state (the controller then rebuilds from AWS).
const formatVersion = 1

type stateDTO struct {
	Version           int           `json:"version"`
	RunID             string        `json:"run_id"`
	NextCycle         int64         `json:"next_cycle"`
	Memory            memoryDTO     `json:"memory"`
	Breaker           breakerDTO    `json:"breaker"`
	ActivityWatermark time.Time     `json:"activity_watermark"`
	SeenActivities    []string      `json:"seen_activities"`
	LastInstances     []instanceDTO `json:"last_instances"`
	// Drains was added without a version bump: older files simply have
	// no tracked drains.
	Drains []drainDTO `json:"drains,omitempty"`
}

type drainDTO struct {
	InstanceID string    `json:"instance_id"`
	Since      time.Time `json:"since"`
	Expired    bool      `json:"expired"`
}

type memoryDTO struct {
	Entries         []entryDTO `json:"entries"`
	HasLastCapacity bool       `json:"has_last_capacity"`
	LastDesired     int        `json:"last_desired"`
	LastInService   int        `json:"last_in_service"`
	LastConsumedCPU time.Time  `json:"last_consumed_cpu"`
	BlindStreak     int        `json:"blind_streak"`
}

type entryDTO struct {
	CycleID     int64        `json:"cycle_id"`
	CPUValid    bool         `json:"cpu_valid"`
	CPU         float64      `json:"cpu"`
	Overloaded  bool         `json:"overloaded"`
	Comfortable bool         `json:"comfortable"`
	Trigger     core.Trigger `json:"trigger"`
}

type breakerDTO struct {
	State               core.BreakerState `json:"state"`
	ConsecutiveFailures int               `json:"consecutive_failures"`
	OpenedAt            time.Time         `json:"opened_at"`
	ProbeInFlight       bool              `json:"probe_in_flight"`
}

type instanceDTO struct {
	ID         string             `json:"id"`
	AZ         string             `json:"az"`
	State      core.InstanceState `json:"state"`
	LaunchedAt time.Time          `json:"launched_at"`
}

func toDTO(s ports.State) stateDTO {
	d := stateDTO{
		Version:           formatVersion,
		RunID:             s.RunID,
		NextCycle:         s.NextCycle,
		ActivityWatermark: s.ActivityWatermark,
		SeenActivities:    append([]string{}, s.SeenActivities...),
		Memory: memoryDTO{
			Entries:         make([]entryDTO, 0, len(s.Memory.Entries)),
			HasLastCapacity: s.Memory.HasLastCapacity,
			LastDesired:     s.Memory.LastDesired,
			LastInService:   s.Memory.LastInService,
			LastConsumedCPU: s.Memory.LastConsumedCPU,
			BlindStreak:     s.Memory.BlindStreak,
		},
		Breaker: breakerDTO{
			State:               s.Breaker.State,
			ConsecutiveFailures: s.Breaker.ConsecutiveFailures,
			OpenedAt:            s.Breaker.OpenedAt,
			ProbeInFlight:       s.Breaker.ProbeInFlight,
		},
		LastInstances: make([]instanceDTO, 0, len(s.LastInstances)),
	}
	for _, e := range s.Memory.Entries {
		d.Memory.Entries = append(d.Memory.Entries, entryDTO(e))
	}
	for _, in := range s.LastInstances {
		d.LastInstances = append(d.LastInstances, instanceDTO(in))
	}
	for _, dr := range s.Drains {
		d.Drains = append(d.Drains, drainDTO(dr))
	}
	return d
}

func fromDTO(d stateDTO) ports.State {
	s := ports.State{
		RunID:             d.RunID,
		NextCycle:         d.NextCycle,
		ActivityWatermark: d.ActivityWatermark,
		SeenActivities:    append([]string{}, d.SeenActivities...),
		Memory: core.Memory{
			HasLastCapacity: d.Memory.HasLastCapacity,
			LastDesired:     d.Memory.LastDesired,
			LastInService:   d.Memory.LastInService,
			LastConsumedCPU: d.Memory.LastConsumedCPU,
			BlindStreak:     d.Memory.BlindStreak,
		},
		Breaker: core.Breaker{
			State:               d.Breaker.State,
			ConsecutiveFailures: d.Breaker.ConsecutiveFailures,
			OpenedAt:            d.Breaker.OpenedAt,
			ProbeInFlight:       d.Breaker.ProbeInFlight,
		},
	}
	for _, e := range d.Memory.Entries {
		s.Memory.Entries = append(s.Memory.Entries, core.WindowEntry(e))
	}
	for _, in := range d.LastInstances {
		s.LastInstances = append(s.LastInstances, core.Instance(in))
	}
	for _, dr := range d.Drains {
		s.Drains = append(s.Drains, ports.Drain(dr))
	}
	return s
}
