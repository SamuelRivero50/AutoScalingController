package jsonllog

import (
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

// Wire types mirror docs/spec/decision-log.schema.json field by field.
// Pointer fields without omitempty encode as null when absent, as the
// schema expects.

type cycleDTO struct {
	SchemaVersion string            `json:"schema_version"`
	Type          string            `json:"type"`
	RunID         string            `json:"run_id"`
	CycleID       int64             `json:"cycle_id"`
	Mode          core.Mode         `json:"mode"`
	Profile       core.Profile      `json:"profile"`
	TS            string            `json:"ts"`
	ConfigHash    string            `json:"config_hash"`
	Observation   observationDTO    `json:"observation"`
	Windows       windowsDTO        `json:"windows"`
	Capacity      capacityDTO       `json:"capacity"`
	Breaker       breakerDTO        `json:"breaker"`
	Decision      core.DecisionKind `json:"decision"`
	ReasonCode    core.ReasonCode   `json:"reason_code"`
	Justification justificationDTO  `json:"justification"`
	Action        actionDTO         `json:"action"`
}

type observationDTO struct {
	Signals []signalDTO `json:"signals"`
}

type signalDTO struct {
	Name        string       `json:"name"`
	Value       *float64     `json:"value"`
	Unit        string       `json:"unit"`
	Quality     core.Quality `json:"quality"`
	DatapointTS *string      `json:"datapoint_ts"`
	PeriodStart *string      `json:"period_start"`
	PeriodEnd   *string      `json:"period_end"`
}

type windowsDTO struct {
	ScaleOut scaleOutDTO `json:"scale_out"`
	ScaleIn  scaleInDTO  `json:"scale_in"`
}

type scaleOutDTO struct {
	M         int     `json:"m"`
	N         int     `json:"n"`
	Breaching []int64 `json:"breaching"`
}

type scaleInDTO struct {
	N           int     `json:"n"`
	Comfortable []int64 `json:"comfortable"`
}

type capacityDTO struct {
	Desired   int           `json:"desired"`
	InService int           `json:"in_service"`
	Pending   int           `json:"pending"`
	Draining  int           `json:"draining"`
	Min       int           `json:"min"`
	Max       int           `json:"max"`
	Instances []instanceDTO `json:"instances"`
}

type instanceDTO struct {
	ID         string             `json:"id"`
	AZ         string             `json:"az"`
	State      core.InstanceState `json:"state"`
	LaunchedAt *string            `json:"launched_at"`
}

type breakerDTO struct {
	State               core.BreakerState `json:"state"`
	ConsecutiveFailures int               `json:"consecutive_failures"`
}

type justificationDTO struct {
	Conditions []conditionDTO `json:"conditions"`
}

type conditionDTO struct {
	Name      string   `json:"name"`
	Value     *float64 `json:"value"`
	Threshold *float64 `json:"threshold"`
	Met       bool     `json:"met"`
}

type actionDTO struct {
	Type         core.ActionType    `json:"type"`
	Params       map[string]any     `json:"params"`
	Status       ports.ActionStatus `json:"status"`
	SkipReason   *string            `json:"skip_reason"`
	APIRequestID *string            `json:"api_request_id"`
	DurationMS   *int64             `json:"duration_ms"`
	Error        *string            `json:"error"`
}

type eventDTO struct {
	SchemaVersion string          `json:"schema_version"`
	Type          string          `json:"type"`
	RunID         string          `json:"run_id"`
	TS            string          `json:"ts"`
	EventType     ports.EventType `json:"event_type"`
	Details       map[string]any  `json:"details"`
}

func timestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func optTimestamp(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := timestamp(t)
	return &s
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	s = redact(s)
	return &s
}

func toCycleDTO(r ports.CycleRecord) cycleDTO {
	return cycleDTO{
		SchemaVersion: ports.SchemaVersion,
		Type:          "cycle",
		RunID:         redact(r.Run.RunID),
		CycleID:       r.CycleID,
		Mode:          r.Run.Mode,
		Profile:       r.Run.Profile,
		TS:            timestamp(r.TS),
		ConfigHash:    r.Run.ConfigHash,
		Observation:   observationDTO{Signals: toSignals(r.Signals.List())},
		Windows: windowsDTO{
			ScaleOut: scaleOutDTO{M: r.ScaleOut.M, N: r.ScaleOut.N, Breaching: nonNil(r.ScaleOut.Breaching)},
			ScaleIn:  scaleInDTO{N: r.ScaleIn.N, Comfortable: nonNil(r.ScaleIn.Comfortable)},
		},
		Capacity:      toCapacity(r.Capacity),
		Breaker:       breakerDTO{State: r.Breaker.State, ConsecutiveFailures: r.Breaker.ConsecutiveFailures},
		Decision:      r.Decision.Decision,
		ReasonCode:    r.Decision.Reason,
		Justification: justificationDTO{Conditions: toConditions(r.Decision.Conditions)},
		Action:        toAction(r.Action),
	}
}

func nonNil(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}

func toSignals(list []core.Signal) []signalDTO {
	out := make([]signalDTO, 0, len(list))
	for _, s := range list {
		d := signalDTO{Name: s.Name, Unit: s.Unit, Quality: s.Quality}
		if s.HasValue {
			v := s.Value
			d.Value = &v
		}
		d.DatapointTS = optTimestamp(s.DatapointTS)
		d.PeriodStart = optTimestamp(s.PeriodStart)
		d.PeriodEnd = optTimestamp(s.PeriodEnd)
		out = append(out, d)
	}
	return out
}

func toCapacity(c core.CapacitySnapshot) capacityDTO {
	d := capacityDTO{
		Desired:   c.Desired,
		InService: c.InService(),
		Pending:   c.Pending(),
		Draining:  c.Draining(),
		Min:       c.Min,
		Max:       c.Max,
		Instances: make([]instanceDTO, 0, len(c.Instances)),
	}
	for _, in := range c.Instances {
		d.Instances = append(d.Instances, instanceDTO{
			ID:         redact(in.ID),
			AZ:         in.AZ,
			State:      in.State,
			LaunchedAt: optTimestamp(in.LaunchedAt),
		})
	}
	return d
}

func toConditions(cs []core.Condition) []conditionDTO {
	out := make([]conditionDTO, 0, len(cs))
	for _, c := range cs {
		out = append(out, conditionDTO{Name: c.Name, Value: c.Value, Threshold: c.Threshold, Met: c.Met})
	}
	return out
}

func toAction(a ports.ActionOutcome) actionDTO {
	d := actionDTO{
		Type:         a.Type,
		Params:       map[string]any{},
		Status:       a.Status,
		SkipReason:   optString(a.SkipReason),
		APIRequestID: optString(a.RequestID),
		Error:        optString(a.Error),
	}
	if a.TargetDesired != nil {
		d.Params["desired_capacity"] = *a.TargetDesired
	}
	if a.InstanceID != "" {
		d.Params["instance_id"] = redact(a.InstanceID)
	}
	if a.Duration != nil {
		ms := a.Duration.Milliseconds()
		d.DurationMS = &ms
	}
	return d
}

func toEventDTO(r ports.EventRecord) eventDTO {
	details, _ := redactValue(r.Details).(map[string]any)
	if details == nil {
		details = map[string]any{}
	}
	return eventDTO{
		SchemaVersion: ports.SchemaVersion,
		Type:          "event",
		RunID:         redact(r.Run.RunID),
		TS:            timestamp(r.TS),
		EventType:     r.Type,
		Details:       details,
	}
}
