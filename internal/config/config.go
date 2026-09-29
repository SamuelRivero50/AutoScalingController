// Package config decodes the controller's versioned configuration file and
// resolves it on top of a named profile (docs/spec/configuration.md §5).
// Only cmd/controller uses it; the application layer and the core receive
// the resolved values through constructors.
//
// The file is JSON. Unknown fields are rejected, so a typo cannot silently
// leave a parameter at its profile default. Overrides only replace the
// parameters they name; the hash of the resolved configuration
// (config_hash) changes with any effective parameter change and never with
// formatting.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/app"
	"github.com/SamuelRivero50/AutoScalingController/internal/core"
)

// Version is the only supported file format version.
const Version = 1

// Duration is a time.Duration encoded as a Go duration string ("90s").
type Duration time.Duration

// UnmarshalJSON decodes a duration string.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string such as \"90s\": %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

// File is the on-disk configuration.
type File struct {
	Version   int        `json:"version"`
	Mode      core.Mode  `json:"mode"`
	Profile   string     `json:"profile"`
	RunID     string     `json:"run_id,omitempty"`
	AWS       *AWS       `json:"aws,omitempty"`
	Sim       *Sim       `json:"sim,omitempty"`
	Paths     Paths      `json:"paths"`
	Overrides *Overrides `json:"overrides,omitempty"`
}

// AWS identifies the real resources (Terraform outputs).
type AWS struct {
	Region         string `json:"region"`
	GroupName      string `json:"asg_name"`
	TargetGroupARN string `json:"target_group_arn"`
	// LoadBalancerDimension and TargetGroupDimension are the ARN suffixes
	// used as CloudWatch dimensions.
	LoadBalancerDimension string `json:"load_balancer_dimension"`
	TargetGroupDimension  string `json:"target_group_dimension"`
	// MetricsWaitTimeout bounds how long startup waits for the target
	// group metrics to appear in CloudWatch (default 10m).
	MetricsWaitTimeout *Duration `json:"metrics_wait_timeout,omitempty"`
}

// Sim configures the local simulated adapters (real clock, fake AWS).
type Sim struct {
	// Load in instance units (1.0 saturates one instance).
	Load           float64 `json:"load"`
	InitialDesired int     `json:"initial_desired"`
	Seed           uint64  `json:"seed"`
}

// Paths are the controller's local files.
type Paths struct {
	LogDir    string `json:"log_dir"`
	StateFile string `json:"state_file"`
}

// Overrides replace individual profile parameters. Nil fields keep the
// profile value.
type Overrides struct {
	LatencySLOMs        *float64  `json:"latency_slo_ms,omitempty"`
	LatencyComfortMs    *float64  `json:"latency_comfort_ms,omitempty"`
	ErrorSLOPct         *float64  `json:"error_slo_pct,omitempty"`
	ErrorComfortPct     *float64  `json:"error_comfort_pct,omitempty"`
	ScaleOutCPU         *float64  `json:"scale_out_cpu,omitempty"`
	ScaleInProjectedCPU *float64  `json:"scale_in_projected_cpu,omitempty"`
	Warmup              *Duration `json:"warmup,omitempty"`
	PendingTimeout      *Duration `json:"pending_timeout,omitempty"`
	DeregistrationDelay *Duration `json:"deregistration_delay,omitempty"`
	DrainTimeout        *Duration `json:"drain_timeout,omitempty"`
}

// Resolved is the validated configuration handed to cmd/controller.
type Resolved struct {
	Mode  core.Mode
	RunID string // empty: generate one
	App   app.Config
	AWS   AWS // set in real mode
	Sim   Sim // set in sim mode
	Paths Paths
	// MetricsWaitTimeout is the startup bound for the metric check.
	MetricsWaitTimeout time.Duration
}

// DefaultMetricsWaitTimeout is used when aws.metrics_wait_timeout is unset.
const DefaultMetricsWaitTimeout = 10 * time.Minute

// Decode reads a configuration file, rejecting unknown fields and trailing
// data.
func Decode(r io.Reader) (File, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return File{}, fmt.Errorf("read config: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f File
	if err := dec.Decode(&f); err != nil {
		return File{}, fmt.Errorf("decode config: %w", err)
	}
	if dec.More() {
		return File{}, errors.New("decode config: trailing data after the configuration object")
	}
	return f, nil
}

// LoadFile decodes and resolves the file at path.
func LoadFile(path string) (Resolved, error) {
	fh, err := os.Open(path) //nolint:gosec // the path is the operator's own -config flag
	if err != nil {
		return Resolved{}, fmt.Errorf("open config: %w", err)
	}
	defer func() { _ = fh.Close() }()
	f, err := Decode(fh)
	if err != nil {
		return Resolved{}, err
	}
	return Resolve(f)
}

// Resolve validates f and applies it on top of its profile.
func Resolve(f File) (Resolved, error) {
	if f.Version != Version {
		return Resolved{}, fmt.Errorf("unsupported config version %d (want %d)", f.Version, Version)
	}
	cfg, err := app.ConfigForProfile(core.Profile(f.Profile))
	if err != nil {
		return Resolved{}, fmt.Errorf("resolve profile: %w", err)
	}
	applyOverrides(&cfg, f.Overrides)
	if err := cfg.Validate(); err != nil {
		return Resolved{}, fmt.Errorf("invalid resolved configuration: %w", err)
	}

	r := Resolved{Mode: f.Mode, RunID: f.RunID, App: cfg, Paths: f.Paths, MetricsWaitTimeout: DefaultMetricsWaitTimeout}
	var errs []error
	if f.Paths.LogDir == "" {
		errs = append(errs, errors.New("paths.log_dir is required"))
	}
	if f.Paths.StateFile == "" {
		errs = append(errs, errors.New("paths.state_file is required"))
	}
	switch f.Mode {
	case core.ModeReal:
		errs = append(errs, resolveAWS(&r, f)...)
	case core.ModeSim:
		errs = append(errs, resolveSim(&r, f)...)
	default:
		errs = append(errs, fmt.Errorf("mode must be %q or %q, got %q", core.ModeReal, core.ModeSim, f.Mode))
	}
	if err := errors.Join(errs...); err != nil {
		return Resolved{}, err
	}
	return r, nil
}

func resolveAWS(r *Resolved, f File) []error {
	if f.AWS == nil {
		return []error{errors.New("aws section is required in real mode")}
	}
	if f.Sim != nil {
		return []error{errors.New("sim section is not allowed in real mode")}
	}
	a := *f.AWS
	var errs []error
	for _, req := range []struct{ name, value string }{
		{"aws.region", a.Region},
		{"aws.asg_name", a.GroupName},
		{"aws.target_group_arn", a.TargetGroupARN},
		{"aws.load_balancer_dimension", a.LoadBalancerDimension},
		{"aws.target_group_dimension", a.TargetGroupDimension},
	} {
		if req.value == "" {
			errs = append(errs, fmt.Errorf("%s is required", req.name))
		}
	}
	if a.MetricsWaitTimeout != nil {
		if *a.MetricsWaitTimeout <= 0 {
			errs = append(errs, errors.New("aws.metrics_wait_timeout must be positive"))
		}
		r.MetricsWaitTimeout = time.Duration(*a.MetricsWaitTimeout)
	}
	r.AWS = a
	return errs
}

func resolveSim(r *Resolved, f File) []error {
	if f.AWS != nil {
		return []error{errors.New("aws section is not allowed in sim mode")}
	}
	s := Sim{Load: 0.5, InitialDesired: r.App.Policy.MinInstances, Seed: 1}
	if f.Sim != nil {
		s = *f.Sim
	}
	var errs []error
	if s.Load < 0 {
		errs = append(errs, errors.New("sim.load must not be negative"))
	}
	if s.InitialDesired < r.App.Policy.MinInstances || s.InitialDesired > r.App.Policy.MaxInstances {
		errs = append(errs, fmt.Errorf("sim.initial_desired must be within [%d, %d]", r.App.Policy.MinInstances, r.App.Policy.MaxInstances))
	}
	r.Sim = s
	return errs
}

func applyOverrides(cfg *app.Config, o *Overrides) {
	if o == nil {
		return
	}
	setF := func(dst *float64, v *float64) {
		if v != nil {
			*dst = *v
		}
	}
	setD := func(dst *time.Duration, v *Duration) {
		if v != nil {
			*dst = time.Duration(*v)
		}
	}
	setF(&cfg.Policy.LatencySLOMs, o.LatencySLOMs)
	setF(&cfg.Policy.LatencyComfortMs, o.LatencyComfortMs)
	setF(&cfg.Policy.ErrorSLOPct, o.ErrorSLOPct)
	setF(&cfg.Policy.ErrorComfortPct, o.ErrorComfortPct)
	setF(&cfg.Policy.ScaleOutCPU, o.ScaleOutCPU)
	setF(&cfg.Policy.ScaleInProjectedCPU, o.ScaleInProjectedCPU)
	setD(&cfg.Warmup, o.Warmup)
	setD(&cfg.PendingTimeout, o.PendingTimeout)
	setD(&cfg.DeregistrationDelay, o.DeregistrationDelay)
	setD(&cfg.DrainTimeout, o.DrainTimeout)
}
