package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/clock"
	"github.com/SamuelRivero50/AutoScalingController/internal/config"
	"github.com/SamuelRivero50/AutoScalingController/internal/core"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "controller.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRun_Usage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{name: "no config flag", args: nil},
		{name: "extra argument", args: []string{"-config", "c.json", "extra"}},
		{name: "unknown flag", args: []string{"-nope"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			if code := run(t.Context(), tt.args, &stderr); code != 2 {
				t.Fatalf("run(%v) = %d, want 2", tt.args, code)
			}
		})
	}
}

func TestRun_InvalidConfig(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	path := writeConfig(t, `{"version":1,"mode":"sim","profile":"nope","paths":{"log_dir":"l","state_file":"s"}}`)
	if code := run(t.Context(), []string{"-config", path}, &stderr); code != 1 {
		t.Fatalf("run() = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unknown profile") {
		t.Errorf("stderr = %q, want the config error", stderr.String())
	}
}

func TestRun_SimModeRunsAndStops(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	path := writeConfig(t, `{"version":1,"mode":"sim","profile":"demo","run_id":"run-smoke",
		"sim":{"load":0.4,"initial_desired":2,"seed":3},
		"paths":{"log_dir":"`+filepath.ToSlash(logDir)+`","state_file":"`+filepath.ToSlash(filepath.Join(dir, "state.json"))+`"}}`)

	// The first cycle runs immediately; the context ends during the
	// following 10s sleep.
	ctx, cancel := context.WithTimeout(t.Context(), 1500*time.Millisecond)
	defer cancel()
	var stderr bytes.Buffer
	if code := run(ctx, []string{"-config", path}, &stderr); code != 0 {
		t.Fatalf("run() = %d, stderr:\n%s", code, stderr.String())
	}

	files, err := filepath.Glob(filepath.Join(logDir, "decisions-*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("decision log files = %v, %v; want one", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	for _, want := range []string{`"CONTROLLER_STARTED"`, `"type":"cycle"`, `"CONTROLLER_STOPPED"`, `"run_id":"run-smoke"`, `"mode":"sim"`} {
		if !strings.Contains(log, want) {
			t.Errorf("decision log lacks %s:\n%s", want, log)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); err != nil {
		t.Errorf("state file not written: %v", err)
	}
}

func TestRealDeps_BuildsWithoutCallingAWS(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "none"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "none"))
	f, err := config.Decode(strings.NewReader(`{"version":1,"mode":"real","profile":"realistic",
		"aws":{"region":"us-east-1","asg_name":"asc-app",
		"target_group_arn":"arn:aws:elasticloadbalancing:us-east-1:000000000000:targetgroup/asc-tg/0123456789abcdef",
		"load_balancer_dimension":"app/asc-alb/0123456789abcdef","target_group_dimension":"targetgroup/asc-tg/0123456789abcdef"},
		"paths":{"log_dir":"l","state_file":"s"}}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Resolve(f)
	if err != nil {
		t.Fatal(err)
	}
	deps, src, err := realDeps(t.Context(), cfg)
	if err != nil {
		t.Fatalf("realDeps() error = %v", err)
	}
	if deps.Metrics == nil || deps.Provisioner == nil || src == nil {
		t.Fatalf("realDeps() = %+v, %v; want metrics, provisioner and validator", deps, src)
	}
	if cfg.Mode != core.ModeReal {
		t.Errorf("mode = %s", cfg.Mode)
	}
}

type scriptedValidator struct {
	errs  []error
	calls int
}

func (v *scriptedValidator) Validate(context.Context) error {
	i := v.calls
	v.calls++
	if i < len(v.errs) {
		return v.errs[i]
	}
	return nil
}

func TestWaitForMetrics(t *testing.T) {
	t.Parallel()
	notYet := errors.New("not found")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name      string
		errs      []error
		timeout   time.Duration
		wantErr   bool
		wantCalls int
	}{
		{name: "available immediately", timeout: time.Minute, wantCalls: 1},
		{name: "available after retries", errs: []error{notYet, notYet}, timeout: 10 * time.Minute, wantCalls: 3},
		{name: "times out", errs: []error{notYet, notYet, notYet, notYet, notYet}, timeout: 70 * time.Second, wantErr: true, wantCalls: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := &scriptedValidator{errs: tt.errs}
			c := clock.NewFake(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC))
			err := waitForMetrics(t.Context(), v, c, tt.timeout, 30*time.Second, logger)
			if (err != nil) != tt.wantErr {
				t.Fatalf("waitForMetrics() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, notYet) {
				t.Errorf("error = %v, want wrapped validator error", err)
			}
			if v.calls != tt.wantCalls {
				t.Errorf("Validate calls = %d, want %d", v.calls, tt.wantCalls)
			}
		})
	}
}

func TestWaitForMetrics_Cancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	v := &scriptedValidator{errs: []error{errors.New("not found")}}
	err := waitForMetrics(ctx, v, clock.NewFake(time.Now()), time.Hour, time.Second, slog.New(slog.DiscardHandler))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestNewRunID(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 27, 12, 30, 5, 0, time.UTC)
	a, b := newRunID(now), newRunID(now)
	if !regexp.MustCompile(`^run-20260927T123005Z-[a-z2-7]{8}$`).MatchString(a) {
		t.Errorf("newRunID() = %q", a)
	}
	if a == b {
		t.Errorf("two run IDs are equal: %q", a)
	}
}
