package core

import (
	"testing"
	"time"
)

func TestRealisticAndDemoConfig(t *testing.T) {
	r, d := RealisticConfig(), DemoConfig()
	checks := []struct {
		name      string
		got, want any
	}{
		{"realistic interval", r.EvaluationInterval, 60 * time.Second},
		{"realistic period", r.AggregationPeriod, 60 * time.Second},
		{"realistic lag", r.MetricLag, 60 * time.Second},
		{"realistic scale-in N", r.ScaleInN, 5},
		{"realistic blind threshold", r.BlindAlertThreshold, 5},
		{"demo interval", d.EvaluationInterval, 10 * time.Second},
		{"demo period", d.AggregationPeriod, 10 * time.Second},
		{"demo lag", d.MetricLag, time.Duration(0)},
		{"demo scale-in N", d.ScaleInN, 3},
		{"demo blind threshold", d.BlindAlertThreshold, 3},
		{"scale-out M of N", [2]int{r.ScaleOutM, r.ScaleOutN}, [2]int{2, 3}},
		{"bounds", [2]int{r.MinInstances, r.MaxInstances}, [2]int{1, 5}},
		{"steps", [2]int{r.MaxScaleOutStep, r.MaxSLOScaleOutStep}, [2]int{2, 1}},
		{"cpu thresholds", [3]float64{r.ScaleOutCPU, r.ScaleOutCPUWithSLO, r.ScaleInProjectedCPU}, [3]float64{70, 55, 55}},
		{"slo thresholds", [4]float64{r.LatencySLOMs, r.ErrorSLOPct, r.LatencyComfortMs, r.ErrorComfortPct}, [4]float64{500, 1, 350, 0.5}},
		{"min requests", r.MinRequestsPerPeriod, 20.0},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Fatalf("got %v, want %v", c.got, c.want)
			}
		})
	}
}

func TestPolicyConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*PolicyConfig)
		wantErr bool
	}{
		{"realistic profile is valid", func(*PolicyConfig) {}, false},
		{"unknown profile", func(c *PolicyConfig) { c.Profile = "fast" }, true},
		{"no dead-band", func(c *PolicyConfig) { c.ScaleInProjectedCPU = 70 }, true},
		{"min zero", func(c *PolicyConfig) { c.MinInstances = 0 }, true},
		{"max below min", func(c *PolicyConfig) { c.MaxInstances = 0 }, true},
		{"M greater than N", func(c *PolicyConfig) { c.ScaleOutM = 4 }, true},
		{"SLO step above max step", func(c *PolicyConfig) { c.MaxSLOScaleOutStep = 3 }, true},
		{"zero period", func(c *PolicyConfig) { c.AggregationPeriod = 0 }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := RealisticConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
	if err := DemoConfig().Validate(); err != nil {
		t.Fatalf("demo profile invalid: %v", err)
	}
}
