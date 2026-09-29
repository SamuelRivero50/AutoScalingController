package app

import (
	"testing"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
)

func TestRealisticAndDemoConfig(t *testing.T) {
	r, d := RealisticConfig(), DemoConfig()
	checks := []struct {
		name      string
		got, want time.Duration
	}{
		{"realistic warmup", r.Warmup, 180 * time.Second},
		{"realistic pending timeout", r.PendingTimeout, 360 * time.Second},
		{"realistic deregistration", r.DeregistrationDelay, 60 * time.Second},
		{"realistic drain timeout", r.DrainTimeout, 90 * time.Second},
		{"realistic cycle budget", r.CycleBudget, 40 * time.Second},
		{"demo warmup", d.Warmup, 20 * time.Second},
		{"demo pending timeout", d.PendingTimeout, 45 * time.Second},
		{"demo deregistration", d.DeregistrationDelay, 10 * time.Second},
		{"demo drain timeout", d.DrainTimeout, 40 * time.Second},
		{"demo cycle budget", d.CycleBudget, 10 * time.Second},
		{"attempt timeout", r.AttemptTimeout, 10 * time.Second},
		{"retry max backoff", r.RetryMaxBackoff, 30 * time.Second},
		{"breaker cool-off", r.Breaker.CoolOff, 10 * time.Minute},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Fatalf("got %v, want %v", c.got, c.want)
			}
		})
	}
	for _, cfg := range []Config{r, d} {
		if err := cfg.Validate(); err != nil {
			t.Fatalf("%s invalid: %v", cfg.Policy.Profile, err)
		}
	}
}

func TestConfigForProfile(t *testing.T) {
	for _, p := range core.Profiles() {
		cfg, err := ConfigForProfile(p)
		if err != nil || cfg.Policy.Profile != p {
			t.Fatalf("ConfigForProfile(%s) = %v, %v", p, cfg.Policy.Profile, err)
		}
	}
	if _, err := ConfigForProfile("fast"); err == nil {
		t.Fatal("unknown profile must fail")
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"budget above interval", func(c *Config) { c.CycleBudget = 2 * time.Minute }},
		{"pending timeout below warmup", func(c *Config) { c.PendingTimeout = time.Second }},
		{"zero breaker threshold", func(c *Config) { c.Breaker.Threshold = 0 }},
		{"invalid policy", func(c *Config) { c.Policy.MinInstances = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := RealisticConfig()
			tt.mutate(&cfg)
			if cfg.Validate() == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
}

func TestConfig_Hash(t *testing.T) {
	r := RealisticConfig()
	if len(r.Hash()) != 64 {
		t.Fatalf("hash %q is not hex SHA-256", r.Hash())
	}
	if r.Hash() != RealisticConfig().Hash() {
		t.Fatal("equal configurations must hash equally")
	}
	if r.Hash() == DemoConfig().Hash() {
		t.Fatal("different profiles must hash differently")
	}
	changed := RealisticConfig()
	changed.Policy.ScaleOutCPU = 75
	if changed.Hash() == r.Hash() {
		t.Fatal("a parameter change must change the hash")
	}
	timeout := RealisticConfig()
	timeout.PendingTimeout = 400 * time.Second
	if timeout.Hash() == r.Hash() {
		t.Fatal("a timeout change must change the hash")
	}
	retries := RealisticConfig()
	retries.MaxRetries = 5
	if retries.Hash() == r.Hash() {
		t.Fatal("a retry policy change must change the hash")
	}
	if r.MaxRetries != 3 {
		t.Fatalf("max retries = %d, want 3", r.MaxRetries)
	}
}
