package awsclient_test

import (
	"testing"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/awsclient"
)

func TestSettings_Validate(t *testing.T) {
	t.Parallel()
	valid := awsclient.Settings{Region: "us-east-1", AttemptTimeout: 10 * time.Second, MaxRetries: 3, MaxBackoff: 30 * time.Second}
	tests := []struct {
		name    string
		mutate  func(*awsclient.Settings)
		wantErr bool
	}{
		{name: "valid", mutate: func(*awsclient.Settings) {}},
		{name: "zero retries is valid", mutate: func(s *awsclient.Settings) { s.MaxRetries = 0 }},
		{name: "missing region", mutate: func(s *awsclient.Settings) { s.Region = "" }, wantErr: true},
		{name: "zero attempt timeout", mutate: func(s *awsclient.Settings) { s.AttemptTimeout = 0 }, wantErr: true},
		{name: "negative retries", mutate: func(s *awsclient.Settings) { s.MaxRetries = -1 }, wantErr: true},
		{name: "zero max backoff", mutate: func(s *awsclient.Settings) { s.MaxBackoff = 0 }, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := valid
			tt.mutate(&s)
			if err := s.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoad_InvalidSettings(t *testing.T) {
	t.Parallel()
	if _, err := awsclient.Load(t.Context(), awsclient.Settings{}); err == nil {
		t.Fatal("Load() with empty settings: want error")
	}
}

func TestLoad_AppliesRetryPolicy(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_CONFIG_FILE", "/nonexistent")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/nonexistent")
	cfg, err := awsclient.Load(t.Context(), awsclient.Settings{
		Region: "us-east-1", AttemptTimeout: 10 * time.Second, MaxRetries: 3, MaxBackoff: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Region != "us-east-1" {
		t.Errorf("region = %q, want us-east-1", cfg.Region)
	}
	if got := cfg.Retryer().MaxAttempts(); got != 4 {
		t.Errorf("max attempts = %d, want 4 (1 + 3 retries)", got)
	}
}

func TestBackoff_BackoffDelay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		attempt int
		rand    float64
		max     time.Duration
		want    time.Duration
	}{
		{name: "first retry", attempt: 1, want: time.Second},
		{name: "second retry", attempt: 2, want: 2 * time.Second},
		{name: "third retry", attempt: 3, want: 4 * time.Second},
		{name: "capped", attempt: 10, want: 30 * time.Second},
		{name: "attempt below one", attempt: 0, want: time.Second},
		{name: "full jitter lowers by twenty percent", attempt: 3, rand: 1, want: 3200 * time.Millisecond},
		{name: "small cap", attempt: 3, max: 3 * time.Second, want: 3 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			maxBackoff := tt.max
			if maxBackoff == 0 {
				maxBackoff = 30 * time.Second
			}
			b := awsclient.Backoff{Max: maxBackoff, Rand: func() float64 { return tt.rand }}
			got, err := b.BackoffDelay(tt.attempt, nil)
			if err != nil {
				t.Fatalf("BackoffDelay() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("BackoffDelay(%d) = %v, want %v", tt.attempt, got, tt.want)
			}
		})
	}
}

func TestBackoff_DefaultRandStaysInRange(t *testing.T) {
	t.Parallel()
	b := awsclient.Backoff{Max: 30 * time.Second}
	for range 100 {
		got, err := b.BackoffDelay(2, nil)
		if err != nil {
			t.Fatalf("BackoffDelay() error = %v", err)
		}
		if got > 2*time.Second || got < 1600*time.Millisecond {
			t.Fatalf("BackoffDelay(2) = %v, want within [1.6s, 2s]", got)
		}
	}
}
