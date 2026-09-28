package core

import (
	"math"
	"testing"
	"time"
)

func TestClassifyRequestCountAndSLOEvaluability(t *testing.T) {
	cfg := RealisticConfig()
	capacity := fleet(2)
	now := t0
	ts := now.Add(-time.Minute)

	tests := []struct {
		name    string
		mutate  func(*Observation)
		rc      Quality
		latency Quality
		errRate Quality
		errVal  float64
	}{
		{
			name:    "absent RequestCount is zero traffic, not missing",
			mutate:  func(_ *Observation) {},
			rc:      QualityNotEvaluable,
			latency: QualityNotEvaluable,
			errRate: QualityNotEvaluable,
		},
		{
			name: "below 20 requests latency and errors not evaluable",
			mutate: func(o *Observation) {
				o.RequestCount = present(19, ts)
				o.TargetResponseTimeP95 = present(900, ts)
			},
			rc:      QualityValid,
			latency: QualityNotEvaluable,
			errRate: QualityNotEvaluable,
		},
		{
			name: "exactly 20 requests is evaluable; absent 5xx counters mean zero errors",
			mutate: func(o *Observation) {
				o.RequestCount = present(20, ts)
				o.TargetResponseTimeP95 = present(120, ts)
			},
			rc:      QualityValid,
			latency: QualityValid,
			errRate: QualityValid,
			errVal:  0,
		},
		{
			name: "combined error rate",
			mutate: func(o *Observation) {
				o.RequestCount = present(200, ts)
				o.TargetResponseTimeP95 = present(120, ts)
				o.Target5xxCount = present(3, ts)
				o.ELB5xxCount = present(1, ts)
			},
			rc:      QualityValid,
			latency: QualityValid,
			errRate: QualityValid,
			errVal:  2,
		},
		{
			name: "latency absent with traffic is a real gap",
			mutate: func(o *Observation) {
				o.RequestCount = present(100, ts)
			},
			rc:      QualityValid,
			latency: QualityMissing,
			errRate: QualityValid,
		},
		{
			name: "RequestCount fetch failure makes SLO signals missing",
			mutate: func(o *Observation) {
				o.RequestCount = Reading{FetchFailed: true}
				o.TargetResponseTimeP95 = present(120, ts)
			},
			rc:      QualityMissing,
			latency: QualityMissing,
			errRate: QualityMissing,
		},
		{
			name: "5xx counter fetch failure makes error rate missing",
			mutate: func(o *Observation) {
				o.RequestCount = present(100, ts)
				o.TargetResponseTimeP95 = present(120, ts)
				o.ELB5xxCount = Reading{FetchFailed: true}
			},
			rc:      QualityValid,
			latency: QualityValid,
			errRate: QualityMissing,
		},
		{
			name: "more errors than requests is anomalous",
			mutate: func(o *Observation) {
				o.RequestCount = present(50, ts)
				o.TargetResponseTimeP95 = present(120, ts)
				o.Target5xxCount = present(60, ts)
			},
			rc:      QualityValid,
			latency: QualityValid,
			errRate: QualityAnomalous,
			errVal:  120,
		},
		{
			name: "negative latency is anomalous",
			mutate: func(o *Observation) {
				o.RequestCount = present(100, ts)
				o.TargetResponseTimeP95 = present(-5, ts)
			},
			rc:      QualityValid,
			latency: QualityAnomalous,
			errRate: QualityValid,
		},
		{
			name: "stale RequestCount makes SLO signals missing",
			mutate: func(o *Observation) {
				o.RequestCount = present(100, now.Add(-10*time.Minute))
			},
			rc:      QualityStale,
			latency: QualityMissing,
			errRate: QualityMissing,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs := Observation{Now: now, CPU: []InstanceCPU{
				{InstanceID: "i-0", Reading: present(40, ts)},
				{InstanceID: "i-1", Reading: present(40, ts)},
			}}
			tt.mutate(&obs)
			sig := Classify(obs, capacity, time.Time{}, cfg)
			if sig.RequestCount.Quality != tt.rc {
				t.Errorf("RequestCount = %s, want %s", sig.RequestCount.Quality, tt.rc)
			}
			if sig.LatencyP95.Quality != tt.latency {
				t.Errorf("latency = %s, want %s", sig.LatencyP95.Quality, tt.latency)
			}
			if sig.ErrorRate.Quality != tt.errRate {
				t.Errorf("error rate = %s, want %s", sig.ErrorRate.Quality, tt.errRate)
			}
			if tt.errRate == QualityValid || tt.errRate == QualityAnomalous {
				if math.Abs(sig.ErrorRate.Value-tt.errVal) > 1e-9 {
					t.Errorf("error rate value = %v, want %v", sig.ErrorRate.Value, tt.errVal)
				}
			}
		})
	}
}

func TestClassifyRequestCountAbsentHasZeroValue(t *testing.T) {
	sig := Classify(Observation{Now: t0}, fleet(1), time.Time{}, RealisticConfig())
	if sig.RequestCount.Quality != QualityNotEvaluable || !sig.RequestCount.HasValue || sig.RequestCount.Value != 0 {
		t.Fatalf("absent RequestCount = %+v, want NOT_EVALUABLE with value 0", sig.RequestCount)
	}
	if sig.RequestCountPerTarget.Quality != QualityNotEvaluable {
		t.Fatalf("absent RequestCountPerTarget = %s, want NOT_EVALUABLE", sig.RequestCountPerTarget.Quality)
	}
	if sig.HealthyHostCount.Quality != QualityMissing {
		t.Fatalf("absent HealthyHostCount = %s, want MISSING", sig.HealthyHostCount.Quality)
	}
}

func TestClassifyCPU(t *testing.T) {
	cfg := RealisticConfig() // stale after 2*60s + 60s = 180s
	now := t0
	fresh := now.Add(-2 * time.Minute)
	old := now.Add(-181 * time.Second)
	edge := now.Add(-180 * time.Second)

	tests := []struct {
		name         string
		n            int
		readings     []Reading // index i -> instance i-i; shorter = missing
		lastConsumed time.Time
		want         Quality
		value        float64
	}{
		{"mean across instances", 3, []Reading{present(30, fresh), present(60, fresh), present(90, fresh)}, time.Time{}, QualityValid, 60},
		{"exactly 50% reporting is enough", 4, []Reading{present(40, fresh), present(60, fresh)}, time.Time{}, QualityValid, 50},
		{"fewer than 50% reporting is missing", 3, []Reading{present(40, fresh)}, time.Time{}, QualityMissing, 0},
		{"no datapoints at all", 2, nil, time.Time{}, QualityMissing, 0},
		{"fetch failure", 1, []Reading{{FetchFailed: true}}, time.Time{}, QualityMissing, 0},
		{"age exactly at the limit is fresh", 1, []Reading{present(50, edge)}, time.Time{}, QualityValid, 50},
		{"one second beyond the limit is stale", 1, []Reading{present(50, old)}, time.Time{}, QualityStale, 0},
		{"negative CPU is anomalous", 1, []Reading{present(-1, fresh)}, time.Time{}, QualityAnomalous, 0},
		{"CPU above 100 is anomalous", 1, []Reading{present(100.5, fresh)}, time.Time{}, QualityAnomalous, 0},
		{"NaN CPU is anomalous", 1, []Reading{present(math.NaN(), fresh)}, time.Time{}, QualityAnomalous, 0},
		{"CPU 0 and 100 are in range", 2, []Reading{present(0, fresh), present(100, fresh)}, time.Time{}, QualityValid, 50},
		{"anomalous instance excluded from mean", 3, []Reading{present(40, fresh), present(60, fresh), present(-3, fresh)}, time.Time{}, QualityValid, 50},
		{"mixed stale and missing is missing", 2, []Reading{present(50, old)}, time.Time{}, QualityMissing, 0},
		{"same datapoint already consumed", 1, []Reading{present(50, fresh)}, fresh, QualityNoNewData, 50},
		{"older datapoint than consumed", 1, []Reading{present(50, fresh)}, fresh.Add(time.Second), QualityNoNewData, 50},
		{"newer datapoint than consumed", 1, []Reading{present(50, fresh)}, fresh.Add(-time.Minute), QualityValid, 50},
		{"no in-service instances", 0, nil, time.Time{}, QualityMissing, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capacity := fleet(tt.n)
			obs := Observation{Now: now}
			for i, r := range tt.readings {
				obs.CPU = append(obs.CPU, InstanceCPU{InstanceID: capacity.Instances[i].ID, Reading: r})
			}
			sig := Classify(obs, capacity, tt.lastConsumed, cfg)
			if sig.CPU.Quality != tt.want {
				t.Fatalf("CPU quality = %s, want %s", sig.CPU.Quality, tt.want)
			}
			if tt.want == QualityValid || tt.want == QualityNoNewData {
				if math.Abs(sig.CPU.Value-tt.value) > 1e-9 {
					t.Fatalf("CPU value = %v, want %v", sig.CPU.Value, tt.value)
				}
			} else if sig.CPU.HasValue {
				t.Fatalf("unusable CPU must not carry a value, got %v", sig.CPU.Value)
			}
		})
	}
}

func TestClassifyCPUIgnoresNonInServiceInstances(t *testing.T) {
	capacity := fleet(1, InstancePending, InstanceDraining)
	ts := t0.Add(-time.Minute)
	obs := Observation{Now: t0, CPU: []InstanceCPU{
		{InstanceID: "i-0", Reading: present(40, ts)},
		{InstanceID: "x-0", Reading: present(99, ts)},
		{InstanceID: "x-1", Reading: present(99, ts)},
	}}
	sig := Classify(obs, capacity, time.Time{}, RealisticConfig())
	if !sig.CPU.Valid() || sig.CPU.Value != 40 {
		t.Fatalf("CPU = %+v, want VALID 40 from the in-service instance only", sig.CPU)
	}
}

func TestClassifyDemoProfileStaleness(t *testing.T) {
	cfg := DemoConfig() // stale after 2*10s + 0s = 20s
	capacity := fleet(1)
	mk := func(age time.Duration) Quality {
		obs := Observation{Now: t0, CPU: []InstanceCPU{{InstanceID: "i-0", Reading: present(50, t0.Add(-age))}}}
		return Classify(obs, capacity, time.Time{}, cfg).CPU.Quality
	}
	if q := mk(20 * time.Second); q != QualityValid {
		t.Fatalf("20s old = %s, want VALID", q)
	}
	if q := mk(21 * time.Second); q != QualityStale {
		t.Fatalf("21s old = %s, want STALE", q)
	}
}

func TestBlindDefinition(t *testing.T) {
	valid := Signal{Quality: QualityValid}
	tests := []struct {
		name string
		sig  Signals
		want bool
	}{
		{"cpu missing and slo not evaluable", Signals{CPU: Signal{Quality: QualityMissing}, LatencyP95: Signal{Quality: QualityNotEvaluable}, ErrorRate: Signal{Quality: QualityNotEvaluable}}, true},
		{"cpu stale and slo missing", Signals{CPU: Signal{Quality: QualityStale}, LatencyP95: Signal{Quality: QualityMissing}, ErrorRate: Signal{Quality: QualityMissing}}, true},
		{"cpu anomalous", Signals{CPU: Signal{Quality: QualityAnomalous}, LatencyP95: Signal{Quality: QualityNotEvaluable}, ErrorRate: Signal{Quality: QualityNotEvaluable}}, true},
		{"cpu missing with valid latency and errors is still blind", Signals{CPU: Signal{Quality: QualityMissing}, LatencyP95: valid, ErrorRate: valid}, true},
		{"cpu no new data is not blind", Signals{CPU: Signal{Quality: QualityNoNewData}, LatencyP95: Signal{Quality: QualityNotEvaluable}, ErrorRate: Signal{Quality: QualityNotEvaluable}}, false},
		{"cpu valid", Signals{CPU: valid}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sig.Blind(); got != tt.want {
				t.Fatalf("Blind() = %v, want %v", got, tt.want)
			}
		})
	}
}
