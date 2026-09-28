package core

import (
	"testing"
	"time"
)

func sigs(cpu, latencyMs, errPct float64, latOK, errOK bool) Signals {
	s := Signals{CPU: Signal{Quality: QualityValid, HasValue: true, Value: cpu}}
	s.LatencyP95 = Signal{Quality: QualityNotEvaluable}
	s.ErrorRate = Signal{Quality: QualityNotEvaluable}
	if latOK {
		s.LatencyP95 = Signal{Quality: QualityValid, HasValue: true, Value: latencyMs}
	}
	if errOK {
		s.ErrorRate = Signal{Quality: QualityValid, HasValue: true, Value: errPct}
	}
	return s
}

func TestAssessOverloadBoundaries(t *testing.T) {
	cfg := RealisticConfig()
	tests := []struct {
		name       string
		sig        Signals
		overloaded bool
		trigger    Trigger
		lowCPUSLO  bool
	}{
		{"cpu exactly 70 overloads", sigs(70, 0, 0, false, false), true, TriggerCPU, false},
		{"cpu just below 70 alone does not", sigs(69.999, 0, 0, false, false), false, TriggerNone, false},
		{"cpu 55 with latency 501 overloads", sigs(55, 501, 0, true, false), true, TriggerLatency, false},
		{"cpu 55 with latency exactly 500 does not", sigs(55, 500, 0, true, false), false, TriggerNone, false},
		{"cpu just below 55 with latency breach is a low-cpu slo breach", sigs(54.999, 800, 0, true, false), false, TriggerNone, true},
		{"cpu 60 with error 1.001% overloads", sigs(60, 0, 1.001, false, true), true, TriggerCapacityErrors, false},
		{"cpu 60 with error exactly 1% does not", sigs(60, 0, 1, false, true), false, TriggerNone, false},
		{"cpu 60 with latency not evaluable does not", sigs(60, 0, 0, false, false), false, TriggerNone, false},
		{"latency takes precedence over errors", sigs(60, 600, 5, true, true), true, TriggerLatency, false},
		{"cpu high takes precedence over slo", sigs(80, 600, 5, true, true), true, TriggerCPU, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := Assess(tt.sig, fleet(2), cfg)
			if a.Overloaded != tt.overloaded || a.Trigger != tt.trigger || a.SLOBreachLowCPU != tt.lowCPUSLO {
				t.Fatalf("overloaded=%v trigger=%q lowCPUSLO=%v, want %v %q %v",
					a.Overloaded, a.Trigger, a.SLOBreachLowCPU, tt.overloaded, tt.trigger, tt.lowCPUSLO)
			}
		})
	}
}

func TestAssessErrorAttribution(t *testing.T) {
	cfg := RealisticConfig()
	s := sigs(60, 0, 2, false, true)

	s.ELB5xx, s.Target5xx = 3, 1
	if got := Assess(s, fleet(2), cfg).Trigger; got != TriggerCapacityErrors {
		t.Fatalf("ELB-dominant errors trigger = %q, want %q", got, TriggerCapacityErrors)
	}
	s.ELB5xx, s.Target5xx = 1, 3
	if got := Assess(s, fleet(2), cfg).Trigger; got != TriggerAppErrors {
		t.Fatalf("target-dominant errors trigger = %q, want %q", got, TriggerAppErrors)
	}
}

func TestAssessComfortBoundaries(t *testing.T) {
	cfg := RealisticConfig()
	tests := []struct {
		name        string
		n           int
		sig         Signals
		comfortable bool
		atMin       bool
	}{
		{"N=2 cpu 27.5 projects to exactly 55", 2, sigs(27.5, 0, 0, false, false), true, false},
		{"N=2 cpu 27.6 projects above 55", 2, sigs(27.6, 0, 0, false, false), false, false},
		{"N=5 cpu 44 projects to exactly 55", 5, sigs(44, 0, 0, false, false), true, false},
		{"N=5 cpu 44.01 projects above 55", 5, sigs(44.01, 0, 0, false, false), false, false},
		{"latency exactly 350 is comfortable", 3, sigs(20, 350, 0, true, false), true, false},
		{"latency 350.01 is not", 3, sigs(20, 350.01, 0, true, false), false, false},
		{"error exactly 0.5% is comfortable", 3, sigs(20, 0, 0.5, false, true), true, false},
		{"error 0.51% is not", 3, sigs(20, 0, 0.51, false, true), false, false},
		{"N=1 has no projection, low load reports at-min", 1, sigs(0, 0, 0, false, false), false, true},
		{"N=1 cpu exactly 55 at-min", 1, sigs(55, 0, 0, false, false), false, true},
		{"N=1 cpu 55.01 is not low load", 1, sigs(55.01, 0, 0, false, false), false, false},
		{"N=1 latency high is not low load", 1, sigs(10, 400, 0, true, false), false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := Assess(tt.sig, fleet(tt.n), cfg)
			if a.Comfortable != tt.comfortable || a.LowLoadAtMin != tt.atMin {
				t.Fatalf("comfortable=%v atMin=%v (projected %v), want %v %v",
					a.Comfortable, a.LowLoadAtMin, a.ProjectedCPU, tt.comfortable, tt.atMin)
			}
			if tt.n <= cfg.MinInstances && a.HasProjection {
				t.Fatal("projection must not be computed at N <= min")
			}
		})
	}
}

func TestAssessInvalidCPUIsNeverOverloadedOrComfortable(t *testing.T) {
	for _, q := range []Quality{QualityMissing, QualityStale, QualityAnomalous} {
		s := sigs(99, 900, 50, true, true)
		s.CPU.Quality = q
		a := Assess(s, fleet(3), RealisticConfig())
		if a.Overloaded || a.Comfortable || a.LowLoadAtMin || a.SLOBreachLowCPU {
			t.Fatalf("CPU %s: got %+v, want no overload/comfort", q, a)
		}
	}
}

func TestScaleOutStep(t *testing.T) {
	cfg := RealisticConfig()
	tests := []struct {
		name    string
		n       int
		cpu     float64
		trigger Trigger
		want    int
	}{
		{"N=1 cpu 70 needs one", 1, 70, TriggerCPU, 1},
		{"N=1 cpu 100 needs one", 1, 100, TriggerCPU, 1},
		{"N=1 cpu 110 would need two", 1, 110.01, TriggerCPU, 2},
		{"N=2 cpu 82.5 exactly three total", 2, 82.5, TriggerCPU, 1},
		{"N=2 cpu 82.6 needs two", 2, 82.6, TriggerCPU, 2},
		{"N=2 cpu 100 capped at +2", 2, 100, TriggerCPU, 2},
		{"N=4 cpu 100 capped by max 5", 4, 100, TriggerCPU, 1},
		{"N=5 no room", 5, 100, TriggerCPU, 0},
		{"latency trigger capped at +1", 2, 69, TriggerLatency, 1},
		{"error trigger capped at +1", 3, 69, TriggerAppErrors, 1},
		{"step is at least 1", 3, 56, TriggerLatency, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ScaleOutStep(tt.n, tt.cpu, tt.trigger, cfg); got != tt.want {
				t.Fatalf("ScaleOutStep(%d, %v, %q) = %d, want %d", tt.n, tt.cpu, tt.trigger, got, tt.want)
			}
		})
	}
}

func TestStaleAfter(t *testing.T) {
	if got := RealisticConfig().staleAfter(); got != 180*time.Second {
		t.Fatalf("realistic staleAfter = %v, want 180s", got)
	}
	if got := DemoConfig().staleAfter(); got != 20*time.Second {
		t.Fatalf("demo staleAfter = %v, want 20s", got)
	}
}
