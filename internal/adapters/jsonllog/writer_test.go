package jsonllog

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
	"github.com/SamuelRivero50/AutoScalingController/internal/testutil/logschema"
)

var t0 = time.Date(2026, 9, 28, 23, 59, 30, 0, time.UTC)

var run = ports.RunInfo{RunID: "run-1", Mode: core.ModeSim, Profile: core.ProfileRealistic, ConfigHash: "abc"}

// sampleCycle builds a realistic cycle record through the real core pipeline.
func sampleCycle(ts time.Time, cycleID int64) ports.CycleRecord {
	cfg := core.RealisticConfig()
	capacity := core.CapacitySnapshot{
		Known: true, Desired: 2, Min: 1, Max: 5, HealthyTargets: 2,
		Instances: []core.Instance{
			{ID: "i-0a1", AZ: "us-east-1a", State: core.InstanceInService, LaunchedAt: ts.Add(-time.Hour)},
			{ID: "i-0b2", AZ: "us-east-1b", State: core.InstanceInService},
		},
	}
	dp := ts.Add(-2 * time.Minute)
	obs := core.Observation{
		Now: ts,
		CPU: []core.InstanceCPU{
			{InstanceID: "i-0a1", Reading: core.Reading{Present: true, Value: 81, Timestamp: dp}},
			{InstanceID: "i-0b2", Reading: core.Reading{Present: true, Value: 79, Timestamp: dp}},
		},
		RequestCount: core.Reading{Present: true, Value: 400, Timestamp: dp},
	}
	sig := core.Classify(obs, capacity, time.Time{}, cfg)
	mem := core.Advance(core.Memory{}, cycleID, sig, capacity, cfg)
	d := core.Decide(core.PolicyInput{CycleID: cycleID, Config: cfg, Signals: sig, Capacity: capacity, Memory: mem})
	return ports.CycleRecord{
		Run: run, CycleID: cycleID, TS: ts, Signals: sig,
		ScaleOut: mem.ScaleOut(cfg), ScaleIn: mem.ScaleIn(cfg),
		Capacity: capacity, Breaker: core.BreakerStatus{State: core.BreakerClosed},
		Decision: d,
		Action:   ports.ActionOutcome{Type: core.ActionNone, Status: ports.ActionOK},
	}
}

func readLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("line is not JSON: %v", err)
		}
		out = append(out, m)
	}
	return out
}

func TestWriter_RecordsValidateAgainstSchema(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	ctx := t.Context()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	desired := 3
	dur := 1500 * time.Millisecond
	must(w.LogEvent(ctx, ports.EventRecord{Run: run, TS: t0, Type: ports.EventControllerStarted, Details: map[string]any{"profile": "realistic"}}))
	must(w.LogCycle(ctx, sampleCycle(t0, 1)))
	c := sampleCycle(t0, 2)
	c.Action = ports.ActionOutcome{Type: core.ActionSetDesiredCapacity, TargetDesired: &desired, Status: ports.ActionOK, RequestID: "req-1", Duration: &dur}
	must(w.LogCycle(ctx, c))
	c.Action = ports.ActionOutcome{Type: core.ActionSetDesiredCapacity, TargetDesired: &desired, Status: ports.ActionSkipped, SkipReason: ports.SkipBreakerOpen}
	must(w.LogCycle(ctx, c))
	c.Action = ports.ActionOutcome{Type: core.ActionTerminateInstance, InstanceID: "i-0a1", Status: ports.ActionError, Error: "throttled"}
	must(w.LogCycle(ctx, c))
	unknown := sampleCycle(t0, 3)
	unknown.Capacity = core.CapacitySnapshot{}
	must(w.LogCycle(ctx, unknown))
	for _, et := range ports.EventTypes() {
		must(w.LogEvent(ctx, ports.EventRecord{Run: run, TS: t0, Type: et}))
	}

	if n := logschema.ValidateDir(t, dir); n != 6+len(ports.EventTypes()) {
		t.Fatalf("validated %d lines, want %d", n, 6+len(ports.EventTypes()))
	}
}

func TestWriter_OneFilePerUTCDay(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	ctx := t.Context()
	for i, ts := range []time.Time{t0, t0.Add(20 * time.Second), t0.Add(40 * time.Second), t0.Add(time.Minute)} {
		if err := w.LogCycle(ctx, sampleCycle(ts, int64(i+1))); err != nil {
			t.Fatal(err)
		}
	}
	day1 := readLines(t, filepath.Join(dir, "decisions-2026-09-28.jsonl"))
	day2 := readLines(t, filepath.Join(dir, "decisions-2026-09-29.jsonl"))
	if len(day1) != 2 || len(day2) != 2 {
		t.Fatalf("lines per day = %d/%d, want 2/2", len(day1), len(day2))
	}
}

func TestWriter_AppendsAcrossWriters(t *testing.T) {
	dir := t.TempDir()
	ctx := t.Context()
	for i := range 2 {
		w, err := New(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.LogCycle(ctx, sampleCycle(t0, int64(i+1))); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if lines := readLines(t, filepath.Join(dir, FileName(t0))); len(lines) != 2 {
		t.Fatalf("lines = %d, want 2 (a new writer must append, not truncate)", len(lines))
	}
}

func TestWriter_EachRecordIsOnDiskImmediately(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.LogCycle(t.Context(), sampleCycle(t0, 1)); err != nil {
		t.Fatal(err)
	}
	// Read before Close: the record must already be in the file.
	if lines := readLines(t, filepath.Join(dir, FileName(t0))); len(lines) != 1 {
		t.Fatalf("lines before close = %d, want 1", len(lines))
	}
}

func TestWriter_NullableFields(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.LogCycle(t.Context(), sampleCycle(t0, 1)); err != nil {
		t.Fatal(err)
	}
	rec := readLines(t, filepath.Join(dir, FileName(t0)))[0]

	action := rec["action"].(map[string]any)
	for _, k := range []string{"skip_reason", "api_request_id", "duration_ms", "error"} {
		if v, ok := action[k]; !ok || v != nil {
			t.Errorf("action.%s = %v (present %v), want null", k, v, ok)
		}
	}
	windows := rec["windows"].(map[string]any)
	if _, ok := windows["scale_in"].(map[string]any)["comfortable"].([]any); !ok {
		t.Error("scale_in.comfortable must be an array, not null")
	}
	instances := rec["capacity"].(map[string]any)["instances"].([]any)
	if instances[1].(map[string]any)["launched_at"] != nil {
		t.Error("unknown launched_at must be null")
	}
}

func TestWriter_RejectsCancelledContext(t *testing.T) {
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := w.LogCycle(ctx, sampleCycle(t0, 1)); err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
}

func TestWriter_RedactsSecrets(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	c := sampleCycle(t0, 1)
	c.Action = ports.ActionOutcome{
		Type: core.ActionTerminateInstance, InstanceID: "i-0a1", Status: ports.ActionError,
		Error: "AccessDenied: arn:aws:autoscaling:us-east-1:123456789012:autoScalingGroup key AKIAABCDEFGHIJKLMNOP",
	}
	if err := w.LogCycle(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	if err := w.LogEvent(t.Context(), ports.EventRecord{Run: run, TS: t0, Type: ports.EventFetchFailure,
		Details: map[string]any{"error": "account 123456789012", "nested": map[string]any{"keys": []any{"ASIAABCDEFGHIJKLMNOP"}}}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName(t0)))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"123456789012", "AKIAABCDEFGHIJKLMNOP", "ASIAABCDEFGHIJKLMNOP"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("log contains %q", secret)
		}
	}
	if !strings.Contains(string(data), "arn:aws:autoscaling:us-east-1:REDACTED:autoScalingGroup") {
		t.Fatalf("ARN not redacted in place:\n%s", data)
	}
}
