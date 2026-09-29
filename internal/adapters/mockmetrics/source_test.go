package mockmetrics

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

var t0 = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

type fixedFleet int

func (f fixedFleet) InServiceCount() int                         { return int(f) }
func (f fixedFleet) InServiceCountAt(_, _ time.Time) int        { return int(f) }

func constant(v float64) func(time.Duration) float64 {
	return func(time.Duration) float64 { return v }
}

func period(offset time.Duration) ports.Period {
	return ports.Period{Start: t0.Add(offset), End: t0.Add(offset + time.Minute)}
}

func TestSource_ClosedLoopCPU(t *testing.T) {
	tests := []struct {
		name string
		load float64
		ids  []string
		want float64
	}{
		{"one instance half load", 0.5, []string{"a"}, 52},
		{"load spread over two", 1.0, []string{"a", "b"}, 52},
		{"saturation is capped at 100", 3.0, []string{"a"}, 100},
		{"idle floor at zero load", 0, []string{"a", "b"}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(1, t0, Profile{Load: constant(tt.load)}, fixedFleet(len(tt.ids)))
			cpu, err := s.InstanceCPU(t.Context(), period(0), tt.ids)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range cpu {
				if math.Abs(c.Reading.Value-tt.want) > 1e-9 || !c.Reading.Present || !c.Reading.Timestamp.Equal(t0) {
					t.Fatalf("reading = %+v, want %v at %v", c.Reading, tt.want, t0)
				}
			}
		})
	}
}

func TestSource_DeterministicAndOrderIndependent(t *testing.T) {
	p := Profile{Load: constant(1), NoiseStdDev: 8}
	ids := []string{"i-1", "i-2", "i-3"}
	a := New(42, t0, p, fixedFleet(3))
	b := New(42, t0, p, fixedFleet(3))

	later, _ := b.InstanceCPU(t.Context(), period(5*time.Minute), ids) // read out of order first
	first, _ := a.InstanceCPU(t.Context(), period(0), ids)
	again, _ := b.InstanceCPU(t.Context(), period(0), ids)
	if !reflect.DeepEqual(first, again) {
		t.Fatal("same seed and period must give the same readings regardless of call order")
	}
	laterA, _ := a.InstanceCPU(t.Context(), period(5*time.Minute), ids)
	if !reflect.DeepEqual(later, laterA) {
		t.Fatal("readings must not depend on previous calls")
	}

	other := New(43, t0, p, fixedFleet(3))
	diff, _ := other.InstanceCPU(t.Context(), period(0), ids)
	if reflect.DeepEqual(first, diff) {
		t.Fatal("a different seed must change the noise")
	}
	if first[0].Reading.Value == first[1].Reading.Value {
		t.Fatal("instances must get independent noise")
	}
}

func TestSource_NoiseIsBounded(t *testing.T) {
	s := New(7, t0, Profile{Load: constant(0.1), NoiseStdDev: 50}, fixedFleet(1))
	for i := range 200 {
		cpu, _ := s.InstanceCPU(t.Context(), period(time.Duration(i)*time.Minute), []string{"x"})
		if v := cpu[0].Reading.Value; v < 0 || v > 100 {
			t.Fatalf("CPU %v out of [0,100]", v)
		}
	}
}

func TestSource_LoadBalancer(t *testing.T) {
	s := New(1, t0, Profile{
		Load:       constant(2),
		ErrorRates: func(time.Duration) (float64, float64) { return 0.01, 0.005 },
	}, fixedFleet(4))
	r, err := s.LoadBalancer(t.Context(), period(0))
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"requests", r.RequestCount.Value, 2000},
		{"per target", r.RequestCountPerTarget.Value, 500},
		{"target 5xx", r.Target5xxCount.Value, 20},
		{"elb 5xx", r.ELB5xxCount.Value, 10},
		{"healthy", r.HealthyHostCount.Value, 4},
		{"latency at 52% cpu", r.TargetResponseTimeP95.Value, 80},
	}
	for _, c := range checks {
		if math.Abs(c.got-c.want) > 1e-9 {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestSource_LatencyGrowsWithCPU(t *testing.T) {
	s := New(1, t0, Profile{Load: constant(1)}, fixedFleet(1)) // CPU 100
	r, _ := s.LoadBalancer(t.Context(), period(0))
	if got := r.TargetResponseTimeP95.Value; got != 680 {
		t.Fatalf("p95 at saturation = %v, want 680", got)
	}
}

func TestSource_NoTrafficPublishesNoRequestMetrics(t *testing.T) {
	for name, p := range map[string]Profile{
		"traffic off": {Load: constant(1), Traffic: func(time.Duration) bool { return false }},
		"zero load":   {Load: constant(0)},
	} {
		t.Run(name, func(t *testing.T) {
			r, err := New(1, t0, p, fixedFleet(2)).LoadBalancer(t.Context(), period(0))
			if err != nil {
				t.Fatal(err)
			}
			if r.RequestCount.Present || r.TargetResponseTimeP95.Present || r.Target5xxCount.Present {
				t.Fatalf("readings = %+v, want no request metrics", r)
			}
			if !r.HealthyHostCount.Present {
				t.Fatal("HealthyHostCount must still be published")
			}
		})
	}
}

func TestSource_LatencyOverlay(t *testing.T) {
	s := New(1, t0, Profile{Load: constant(0.4), LatencyOverlayMs: func(e time.Duration) float64 {
		if e >= time.Minute {
			return 700
		}
		return 0
	}}, fixedFleet(2))
	before, _ := s.LoadBalancer(t.Context(), period(0))
	after, _ := s.LoadBalancer(t.Context(), period(time.Minute))
	if before.TargetResponseTimeP95.Value != 80 || after.TargetResponseTimeP95.Value != 780 {
		t.Fatalf("p95 before/after = %v/%v, want 80/780", before.TargetResponseTimeP95.Value, after.TargetResponseTimeP95.Value)
	}
}

func TestSource_Outage(t *testing.T) {
	s := New(1, t0, Profile{Load: constant(1), Outage: func(e time.Duration) bool { return e < time.Minute }}, fixedFleet(1))
	if _, err := s.InstanceCPU(t.Context(), period(0), []string{"a"}); !errors.Is(err, ErrOutage) {
		t.Fatalf("CPU during outage = %v, want ErrOutage", err)
	}
	if _, err := s.LoadBalancer(t.Context(), period(0)); !errors.Is(err, ErrOutage) {
		t.Fatalf("LB during outage = %v, want ErrOutage", err)
	}
	if _, err := s.InstanceCPU(t.Context(), period(time.Minute), []string{"a"}); err != nil {
		t.Fatalf("CPU after outage = %v", err)
	}
}

func TestSource_CancelledContext(t *testing.T) {
	s := New(1, t0, Profile{Load: constant(1)}, fixedFleet(1))
	ctx, cancel := cancelled(t)
	defer cancel()
	if _, err := s.InstanceCPU(ctx, period(0), []string{"a"}); err == nil {
		t.Fatal("InstanceCPU must honor ctx")
	}
	if _, err := s.LoadBalancer(ctx, period(0)); err == nil {
		t.Fatal("LoadBalancer must honor ctx")
	}
}
