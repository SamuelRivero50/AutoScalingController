package simulator

import (
	"reflect"
	"testing"

	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
	"github.com/SamuelRivero50/AutoScalingController/internal/testutil/logschema"
)

const seed = 1

// TestScenarios runs S1-S10, checks each acceptance criterion and validates
// every JSONL line the run wrote against the decision-log schema.
func TestScenarios(t *testing.T) {
	for _, sc := range Scenarios() {
		t.Run(sc.ID, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			r, err := Run(t.Context(), sc, Options{Seed: seed, LogDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			if err := sc.Check(r); err != nil {
				t.Fatalf("%s (%s): %v\ncriterion: %s", sc.ID, sc.Name, err, sc.Criterion)
			}
			wantCycles := int(sc.Duration / r.Config.Policy.EvaluationInterval)
			if len(r.Cycles) != wantCycles {
				t.Fatalf("cycles = %d, want %d", len(r.Cycles), wantCycles)
			}
			lines := logschema.ValidateDir(t, dir)
			if want := len(r.Cycles) + len(r.Events); lines != want {
				t.Fatalf("log lines = %d, want %d (every record written)", lines, want)
			}
		})
	}
}

func TestRun_Deterministic(t *testing.T) {
	sc, _ := ByID("S5")
	decisions := func(seed uint64) []string {
		r, err := Run(t.Context(), sc, Options{Seed: seed, LogDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(r.Cycles))
		for _, c := range r.Cycles {
			out = append(out, string(c.Decision.Reason))
		}
		return out
	}
	if a, b := decisions(7), decisions(7); !reflect.DeepEqual(a, b) {
		t.Fatal("the same seed must reproduce the same decisions")
	}
}

func TestScenarioCatalog(t *testing.T) {
	seen := map[string]bool{}
	for _, sc := range Scenarios() {
		if seen[sc.ID] || sc.Check == nil || sc.Criterion == "" || sc.Metrics.Load == nil {
			t.Fatalf("scenario %s is incomplete or duplicated", sc.ID)
		}
		seen[sc.ID] = true
	}
	if len(seen) != 10 {
		t.Fatalf("scenarios = %d, want S1-S10", len(seen))
	}
	if _, ok := ByID("S11"); ok {
		t.Fatal("unknown id must not resolve")
	}
}

func TestTee(t *testing.T) {
	a, b := &recorder{}, &recorder{}
	l := tee{a, b}
	if err := l.LogEvent(t.Context(), ports.EventRecord{Type: ports.EventControllerStarted}); err != nil {
		t.Fatal(err)
	}
	if len(a.events) != 1 || len(b.events) != 1 {
		t.Fatal("tee must write to every logger")
	}
}
