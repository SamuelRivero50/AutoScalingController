package evaluation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleLog = `{"schema_version":"1.0","type":"event","run_id":"r1","ts":"2026-01-01T00:00:00Z","event_type":"CONTROLLER_STARTED","details":{}}
{"schema_version":"1.0","type":"cycle","run_id":"r1","cycle_id":2,"mode":"sim","profile":"realistic","ts":"2026-01-01T00:01:00Z","config_hash":"h","observation":{"signals":[{"name":"CPUUtilization","value":80,"unit":"Percent","quality":"VALID"}]},"capacity":{"desired":2,"in_service":2,"pending":0,"draining":0,"min":1,"max":5,"instances":[]},"decision":"INCREASE_CAPACITY","reason_code":"INCREASE_CPU_HIGH","justification":{"conditions":[{"name":"cpu_high","value":80,"threshold":70,"met":true}]},"action":{"type":"SET_DESIRED_CAPACITY","params":{"desired_capacity":4},"status":"OK","skip_reason":null}}
{"schema_version":"1.0","type":"cycle","run_id":"r1","cycle_id":1,"mode":"sim","profile":"realistic","ts":"2026-01-01T00:00:00Z","config_hash":"h","observation":{"signals":[{"name":"CPUUtilization","value":null,"unit":"Percent","quality":"MISSING"}]},"capacity":{"desired":2,"in_service":2,"pending":0,"draining":0,"min":1,"max":5,"instances":[]},"decision":"MAINTAIN_CAPACITY","reason_code":"MAINTAIN_BLIND","justification":{"conditions":[]},"action":{"type":"NONE","params":{},"status":"OK","skip_reason":null}}

{"schema_version":"1.0","type":"event","run_id":"r2","ts":"2026-01-01T00:00:00Z","event_type":"CONTROLLER_STARTED","details":{}}
`

func writeLog(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCycle_Condition(t *testing.T) {
	t.Parallel()
	c := Cycle{Conditions: []Condition{{Name: "cpu_high", Met: true}}}
	if got, ok := c.Condition("cpu_high"); !ok || !got.Met {
		t.Fatalf("Condition(cpu_high) = %+v, %v", got, ok)
	}
	if _, ok := c.Condition("absent"); ok {
		t.Fatal("Condition(absent) found")
	}
}

func TestCycle_SignalValue(t *testing.T) {
	t.Parallel()
	v := 42.0
	c := Cycle{Observation: Observation{Signals: []Signal{
		{Name: "CPUUtilization", Value: &v, Quality: "VALID"},
		{Name: "TargetResponseTime", Value: &v, Quality: "STALE"},
		{Name: "ErrorRate5xx", Quality: "VALID"},
	}}}
	tests := []struct {
		name   string
		signal string
		ok     bool
	}{
		{name: "valid", signal: "CPUUtilization", ok: true},
		{name: "not valid quality", signal: "TargetResponseTime"},
		{name: "null value", signal: "ErrorRate5xx"},
		{name: "absent", signal: "RequestCount"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := c.SignalValue(tt.signal)
			if ok != tt.ok || (ok && got != v) {
				t.Fatalf("SignalValue(%s) = %v, %v; want ok=%v", tt.signal, got, ok, tt.ok)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeLog(t, dir, "a/decisions-2026-01-01.jsonl", sampleLog)
	writeLog(t, dir, "a/notes.txt", "ignored")

	runs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].ID != "r1" || runs[1].ID != "r2" {
		t.Fatalf("runs = %+v, want r1 and r2", runs)
	}
	r1 := runs[0]
	if len(r1.Cycles) != 2 || len(r1.Events) != 1 {
		t.Fatalf("r1 has %d cycles and %d events, want 2 and 1", len(r1.Cycles), len(r1.Events))
	}
	if r1.Cycles[0].CycleID != 1 || r1.Cycles[1].CycleID != 2 {
		t.Fatalf("cycles not sorted by cycle_id: %d, %d", r1.Cycles[0].CycleID, r1.Cycles[1].CycleID)
	}
	c := r1.Cycles[1]
	if cond, ok := c.Condition("cpu_high"); !ok || *cond.Threshold != 70 {
		t.Fatalf("justification conditions not decoded: %+v", c.Conditions)
	}
	if c.Action.Params["desired_capacity"] != 4.0 {
		t.Fatalf("action params = %v", c.Action.Params)
	}
}

func TestLoad_File(t *testing.T) {
	t.Parallel()
	path := writeLog(t, t.TempDir(), "x.jsonl", sampleLog)
	runs, err := Load(path)
	if err != nil || len(runs) != 2 {
		t.Fatalf("Load(file) = %d runs, %v", len(runs), err)
	}
}

func TestLoad_Errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "invalid json", content: "{not json}\n", want: "line 1"},
		{name: "unknown type", content: `{"type":"other","run_id":"r"}` + "\n", want: "unknown record type"},
		{name: "invalid cycle", content: `{"type":"cycle","run_id":"r","cycle_id":"x"}` + "\n", want: "line 1"},
		{name: "invalid event", content: `{"type":"event","run_id":"r","ts":"yesterday"}` + "\n", want: "line 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := writeLog(t, t.TempDir(), "bad.jsonl", tt.content)
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestLoad_NoFiles(t *testing.T) {
	t.Parallel()
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("Load(empty dir) succeeded")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("Load(missing path) succeeded")
	}
}
