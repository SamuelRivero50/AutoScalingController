package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/app"
	"github.com/SamuelRivero50/AutoScalingController/internal/config"
	"github.com/SamuelRivero50/AutoScalingController/internal/core"
)

const realFile = `{
  "version": 1,
  "mode": "real",
  "profile": "realistic",
  "aws": {
    "region": "us-east-1",
    "asg_name": "asc-app",
    "target_group_arn": "arn:aws:elasticloadbalancing:us-east-1:000000000000:targetgroup/asc-tg/0123456789abcdef",
    "load_balancer_dimension": "app/asc-alb/0123456789abcdef",
    "target_group_dimension": "targetgroup/asc-tg/0123456789abcdef"
  },
  "paths": {"log_dir": "/var/log/asc", "state_file": "/var/lib/asc/state.json"}
}`

func TestDuration_UnmarshalJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    time.Duration
		wantErr bool
	}{
		{name: "seconds", in: `"90s"`, want: 90 * time.Second},
		{name: "minutes", in: `"10m"`, want: 10 * time.Minute},
		{name: "number rejected", in: `90`, wantErr: true},
		{name: "invalid string", in: `"soon"`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var d config.Duration
			err := d.UnmarshalJSON([]byte(tt.in))
			if (err != nil) != tt.wantErr {
				t.Fatalf("UnmarshalJSON(%s) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if !tt.wantErr && time.Duration(d) != tt.want {
				t.Errorf("UnmarshalJSON(%s) = %v, want %v", tt.in, time.Duration(d), tt.want)
			}
		})
	}
}

func TestDecode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		wantErr string
	}{
		{name: "valid", in: realFile},
		{name: "unknown field", in: `{"version":1,"surprise":true}`, wantErr: "unknown field"},
		{name: "trailing data", in: `{"version":1} {"version":1}`, wantErr: "trailing data"},
		{name: "not json", in: `version: 1`, wantErr: "decode config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := config.Decode(strings.NewReader(tt.in))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Decode() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Decode() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func decode(t *testing.T, s string) config.File {
	t.Helper()
	f, err := config.Decode(strings.NewReader(s))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	return f
}

func TestResolve_RealProfileDefaults(t *testing.T) {
	t.Parallel()
	r, err := config.Resolve(decode(t, realFile))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if r.Mode != core.ModeReal || r.AWS.GroupName != "asc-app" || r.Paths.LogDir != "/var/log/asc" {
		t.Errorf("resolved = %+v", r)
	}
	if r.MetricsWaitTimeout != config.DefaultMetricsWaitTimeout {
		t.Errorf("metrics wait timeout = %v, want default", r.MetricsWaitTimeout)
	}
	if got, want := r.App.Hash(), app.RealisticConfig().Hash(); got != want {
		t.Errorf("config hash = %s, want the realistic profile hash %s (no overrides)", got, want)
	}
}

func TestResolve_FormattingDoesNotChangeHash(t *testing.T) {
	t.Parallel()
	compact := strings.Join(strings.Fields(realFile), "")
	a, err := config.Resolve(decode(t, realFile))
	if err != nil {
		t.Fatal(err)
	}
	b, err := config.Resolve(decode(t, compact))
	if err != nil {
		t.Fatal(err)
	}
	if a.App.Hash() != b.App.Hash() {
		t.Error("reformatting the file changed config_hash")
	}
}

func TestResolve_Overrides(t *testing.T) {
	t.Parallel()
	in := strings.Replace(realFile, `"paths"`, `"overrides": {
    "latency_slo_ms": 300, "latency_comfort_ms": 210, "error_slo_pct": 2, "error_comfort_pct": 1,
    "scale_out_cpu": 75, "scale_in_projected_cpu": 50,
    "warmup": "150s", "pending_timeout": "300s", "deregistration_delay": "30s", "drain_timeout": "60s"
  },
  "paths"`, 1)
	r, err := config.Resolve(decode(t, in))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	p := r.App.Policy
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"latency slo", p.LatencySLOMs, 300.0},
		{"latency comfort", p.LatencyComfortMs, 210.0},
		{"error slo", p.ErrorSLOPct, 2.0},
		{"error comfort", p.ErrorComfortPct, 1.0},
		{"scale-out cpu", p.ScaleOutCPU, 75.0},
		{"scale-in projected cpu", p.ScaleInProjectedCPU, 50.0},
		{"warmup", r.App.Warmup, 150 * time.Second},
		{"pending timeout", r.App.PendingTimeout, 300 * time.Second},
		{"deregistration delay", r.App.DeregistrationDelay, 30 * time.Second},
		{"drain timeout", r.App.DrainTimeout, 60 * time.Second},
		{"untouched interval", p.EvaluationInterval, 60 * time.Second},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if r.App.Hash() == app.RealisticConfig().Hash() {
		t.Error("overrides did not change config_hash")
	}
}

func TestResolve_Sim(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want config.Sim
	}{
		{
			name: "defaults",
			in:   `{"version":1,"mode":"sim","profile":"demo","paths":{"log_dir":"l","state_file":"s"}}`,
			want: config.Sim{Load: 0.5, InitialDesired: 1, Seed: 1},
		},
		{
			name: "explicit",
			in:   `{"version":1,"mode":"sim","profile":"demo","sim":{"load":2.5,"initial_desired":3,"seed":7},"paths":{"log_dir":"l","state_file":"s"}}`,
			want: config.Sim{Load: 2.5, InitialDesired: 3, Seed: 7},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, err := config.Resolve(decode(t, tt.in))
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if r.Mode != core.ModeSim || r.Sim != tt.want || r.App.Policy.Profile != core.ProfileDemo {
				t.Errorf("resolved = %+v, want sim %+v on demo", r, tt.want)
			}
		})
	}
}

func TestResolve_Errors(t *testing.T) {
	t.Parallel()
	paths := `"paths":{"log_dir":"l","state_file":"s"}`
	tests := []struct {
		name    string
		in      string
		wantErr string
	}{
		{name: "wrong version", in: `{"version":2,"mode":"sim","profile":"demo",` + paths + `}`, wantErr: "unsupported config version"},
		{name: "unknown profile", in: `{"version":1,"mode":"sim","profile":"fast",` + paths + `}`, wantErr: "unknown profile"},
		{name: "unknown mode", in: `{"version":1,"mode":"live","profile":"demo",` + paths + `}`, wantErr: "mode must be"},
		{name: "missing paths", in: `{"version":1,"mode":"sim","profile":"demo","paths":{}}`, wantErr: "paths.log_dir is required"},
		{name: "real without aws", in: `{"version":1,"mode":"real","profile":"realistic",` + paths + `}`, wantErr: "aws section is required"},
		{name: "real with sim", in: `{"version":1,"mode":"real","profile":"realistic","aws":{},"sim":{},` + paths + `}`, wantErr: "sim section is not allowed"},
		{name: "real missing fields", in: `{"version":1,"mode":"real","profile":"realistic","aws":{"region":"us-east-1"},` + paths + `}`, wantErr: "aws.asg_name is required"},
		{name: "sim with aws", in: `{"version":1,"mode":"sim","profile":"demo","aws":{},` + paths + `}`, wantErr: "aws section is not allowed"},
		{name: "sim negative load", in: `{"version":1,"mode":"sim","profile":"demo","sim":{"load":-1,"initial_desired":1},` + paths + `}`, wantErr: "sim.load"},
		{name: "sim desired above max", in: `{"version":1,"mode":"sim","profile":"demo","sim":{"load":1,"initial_desired":9},` + paths + `}`, wantErr: "sim.initial_desired"},
		{name: "override breaks validation", in: `{"version":1,"mode":"sim","profile":"demo","overrides":{"warmup":"1h"},` + paths + `}`, wantErr: "pending timeout must exceed warmup"},
		{
			name:    "non-positive metrics wait",
			in:      strings.Replace(realFile, `"region"`, `"metrics_wait_timeout": "0s", "region"`, 1),
			wantErr: "metrics_wait_timeout must be positive",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := config.Resolve(decode(t, tt.in))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Resolve() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestResolve_MetricsWaitTimeout(t *testing.T) {
	t.Parallel()
	in := strings.Replace(realFile, `"region"`, `"metrics_wait_timeout": "2m", "region"`, 1)
	r, err := config.Resolve(decode(t, in))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if r.MetricsWaitTimeout != 2*time.Minute {
		t.Errorf("metrics wait timeout = %v, want 2m", r.MetricsWaitTimeout)
	}
}

func TestLoadFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	good := filepath.Join(dir, "controller.json")
	if err := os.WriteFile(good, []byte(realFile), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"version":1,"oops":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadFile(good); err != nil {
		t.Errorf("LoadFile(valid) error = %v", err)
	}
	if _, err := config.LoadFile(bad); err == nil {
		t.Error("LoadFile(invalid) error = nil")
	}
	if _, err := config.LoadFile(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("LoadFile(missing) error = nil")
	}
}
