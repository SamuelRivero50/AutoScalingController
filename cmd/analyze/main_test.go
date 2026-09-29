package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SamuelRivero50/AutoScalingController/internal/simulator"
)

// simulate writes the logs of the given scenarios under one directory.
func simulate(t *testing.T, ids ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, id := range ids {
		sc, ok := simulator.ByID(id)
		if !ok {
			t.Fatalf("unknown scenario %s", id)
		}
		if _, err := simulator.Run(t.Context(), sc, simulator.Options{Seed: 1, LogDir: filepath.Join(root, id)}); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRun(t *testing.T) {
	t.Parallel()
	root := simulate(t, "S2", "S7", "S9")

	var out, errOut bytes.Buffer
	if code := run([]string{root}, &out, &errOut); code != 0 {
		t.Fatalf("run = %d, stderr: %s", code, errOut.String())
	}
	text := out.String()
	for _, want := range []string{
		"== sim-S2-seed1 (sim, realistic", "== sim-S7-seed1", "SLO compliance", "instance-minutes",
		"time-to-relief", "capacity-relieved", "stuck terminations", "warmup", "reason codes", "INCREASE_CPU_HIGH",
		"run", "relief median", "breaker resets",
		"SLO compliance    n/a", // S9 has no traffic, so the SLO is not evaluable
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
}

func TestRun_JSON(t *testing.T) {
	t.Parallel()
	root := simulate(t, "S1")

	var out, errOut bytes.Buffer
	if code := run([]string{"-json", root}, &out, &errOut); code != 0 {
		t.Fatalf("run = %d, stderr: %s", code, errOut.String())
	}
	var reports []struct {
		RunID string `json:"run_id"`
		SLO   struct {
			CompliancePct float64 `json:"compliance_pct"`
		} `json:"slo"`
		Interval float64 `json:"cycle_interval"`
	}
	if err := json.Unmarshal(out.Bytes(), &reports); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if len(reports) != 1 || reports[0].RunID != "sim-S1-seed1" || reports[0].SLO.CompliancePct != 100 || reports[0].Interval != 60 {
		t.Fatalf("reports = %+v", reports)
	}
}

func TestRun_Errors(t *testing.T) {
	t.Parallel()
	empty := t.TempDir()
	noCycles := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(noCycles, []byte(`{"type":"event","run_id":"r","ts":"2026-01-01T00:00:00Z","event_type":"CONTROLLER_STARTED","details":{}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{name: "bad flag", args: []string{"-nope"}, code: 2},
		{name: "no logs", args: []string{empty}, code: 1},
		{name: "missing path", args: []string{filepath.Join(empty, "missing")}, code: 1},
		{name: "run without cycles", args: []string{noCycles}, code: 0, want: "no cycle records"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out, errOut bytes.Buffer
			if code := run(tt.args, &out, &errOut); code != tt.code {
				t.Fatalf("run(%v) = %d, want %d (stderr: %s)", tt.args, code, tt.code, errOut.String())
			}
			if tt.want != "" && !strings.Contains(out.String(), tt.want) {
				t.Fatalf("output lacks %q:\n%s", tt.want, out.String())
			}
		})
	}
}

func TestShortHash(t *testing.T) {
	t.Parallel()
	if got := shortHash("0123456789abcdef"); got != "0123456789ab" {
		t.Fatalf("shortHash = %q", got)
	}
	if got := shortHash("abc"); got != "abc" {
		t.Fatalf("shortHash = %q", got)
	}
}
