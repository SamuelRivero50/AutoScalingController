package cloudwatch_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdkcw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/cloudwatch"
	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

const (
	lbSuffix = "app/asc-alb/0123456789abcdef"
	tgSuffix = "targetgroup/asc-tg/0123456789abcdef"
)

var (
	t0     = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	period = ports.Period{Start: t0, End: t0.Add(time.Minute)}
)

// fakeAPI answers GetMetricData with pages built by respond, and
// ListMetrics with canned pages.
type fakeAPI struct {
	respond    func(in *sdkcw.GetMetricDataInput, call int) (*sdkcw.GetMetricDataOutput, error)
	calls      []*sdkcw.GetMetricDataInput
	listPages  []*sdkcw.ListMetricsOutput
	listErr    error
	listInputs []*sdkcw.ListMetricsInput
}

func (f *fakeAPI) GetMetricData(_ context.Context, in *sdkcw.GetMetricDataInput, _ ...func(*sdkcw.Options)) (*sdkcw.GetMetricDataOutput, error) {
	f.calls = append(f.calls, in)
	return f.respond(in, len(f.calls)-1)
}

func (f *fakeAPI) ListMetrics(_ context.Context, in *sdkcw.ListMetricsInput, _ ...func(*sdkcw.Options)) (*sdkcw.ListMetricsOutput, error) {
	f.listInputs = append(f.listInputs, in)
	if f.listErr != nil {
		return nil, f.listErr
	}
	i := len(f.listInputs) - 1
	if i >= len(f.listPages) {
		return &sdkcw.ListMetricsOutput{}, nil
	}
	return f.listPages[i], nil
}

func result(id string, status cwtypes.StatusCode, points map[time.Time]float64) cwtypes.MetricDataResult {
	r := cwtypes.MetricDataResult{Id: aws.String(id), StatusCode: status}
	for ts, v := range points {
		r.Timestamps = append(r.Timestamps, ts)
		r.Values = append(r.Values, v)
	}
	return r
}

func newSource(t *testing.T, api cloudwatch.API) *cloudwatch.Source {
	t.Helper()
	s, err := cloudwatch.New(api, cloudwatch.Config{LoadBalancer: lbSuffix, TargetGroup: tgSuffix})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return s
}

func TestNew(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{}
	tests := []struct {
		name    string
		api     cloudwatch.API
		cfg     cloudwatch.Config
		wantErr bool
	}{
		{name: "valid", api: api, cfg: cloudwatch.Config{LoadBalancer: lbSuffix, TargetGroup: tgSuffix}},
		{name: "nil api", cfg: cloudwatch.Config{LoadBalancer: lbSuffix, TargetGroup: tgSuffix}, wantErr: true},
		{name: "missing load balancer", api: api, cfg: cloudwatch.Config{TargetGroup: tgSuffix}, wantErr: true},
		{name: "missing target group", api: api, cfg: cloudwatch.Config{LoadBalancer: lbSuffix}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := cloudwatch.New(tt.api, tt.cfg); (err != nil) != tt.wantErr {
				t.Fatalf("New() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSource_InstanceCPU(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{respond: func(*sdkcw.GetMetricDataInput, int) (*sdkcw.GetMetricDataOutput, error) {
		return &sdkcw.GetMetricDataOutput{MetricDataResults: []cwtypes.MetricDataResult{
			result("m0", cwtypes.StatusCodeComplete, map[time.Time]float64{t0: 42.5}),
			result("m1", cwtypes.StatusCodeComplete, nil),
			result("m2", cwtypes.StatusCodeForbidden, nil),
		}}, nil
	}}
	s := newSource(t, api)

	got, err := s.InstanceCPU(t.Context(), period, []string{"i-a", "i-b", "i-c"})
	if err != nil {
		t.Fatalf("InstanceCPU() error = %v", err)
	}
	want := []core.InstanceCPU{
		{InstanceID: "i-a", Reading: core.Reading{Present: true, Value: 42.5, Timestamp: t0}},
		{InstanceID: "i-b", Reading: core.Reading{}},
		{InstanceID: "i-c", Reading: core.Reading{FetchFailed: true}},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d readings, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("reading %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	in := api.calls[0]
	if !aws.ToTime(in.StartTime).Equal(period.Start) || !aws.ToTime(in.EndTime).Equal(period.End) {
		t.Errorf("time range = [%v, %v), want the closed period", aws.ToTime(in.StartTime), aws.ToTime(in.EndTime))
	}
	q := in.MetricDataQueries[1].MetricStat
	if aws.ToString(q.Metric.Namespace) != "AWS/EC2" || aws.ToString(q.Metric.MetricName) != "CPUUtilization" ||
		aws.ToString(q.Stat) != "Average" || aws.ToInt32(q.Period) != 60 {
		t.Errorf("cpu query = %+v", q)
	}
	if d := q.Metric.Dimensions; len(d) != 1 || aws.ToString(d[0].Name) != "InstanceId" || aws.ToString(d[0].Value) != "i-b" {
		t.Errorf("cpu dimensions = %+v, want InstanceId=i-b", d)
	}
}

func TestSource_InstanceCPU_NoInstances(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{}
	got, err := newSource(t, api).InstanceCPU(t.Context(), period, nil)
	if err != nil || len(got) != 0 || len(api.calls) != 0 {
		t.Fatalf("InstanceCPU(nil) = %v, %v with %d calls; want empty, nil, no call", got, err, len(api.calls))
	}
}

func TestSource_InstanceCPU_Paginates(t *testing.T) {
	t.Parallel()
	later := t0.Add(30 * time.Second)
	api := &fakeAPI{respond: func(_ *sdkcw.GetMetricDataInput, call int) (*sdkcw.GetMetricDataOutput, error) {
		if call == 0 {
			return &sdkcw.GetMetricDataOutput{
				NextToken:         aws.String("page2"),
				MetricDataResults: []cwtypes.MetricDataResult{result("m0", cwtypes.StatusCodePartialData, map[time.Time]float64{t0: 10})},
			}, nil
		}
		return &sdkcw.GetMetricDataOutput{MetricDataResults: []cwtypes.MetricDataResult{
			result("m0", cwtypes.StatusCodeComplete, map[time.Time]float64{later: 20}),
			result("unknown", cwtypes.StatusCodeComplete, map[time.Time]float64{t0: 99}),
		}}, nil
	}}

	got, err := newSource(t, api).InstanceCPU(t.Context(), period, []string{"i-a"})
	if err != nil {
		t.Fatalf("InstanceCPU() error = %v", err)
	}
	if len(api.calls) != 2 {
		t.Fatalf("GetMetricData calls = %d, want 2", len(api.calls))
	}
	want := core.Reading{Present: true, Value: 20, Timestamp: later}
	if got[0].Reading != want {
		t.Errorf("reading = %+v, want latest datapoint %+v", got[0].Reading, want)
	}
}

func TestSource_InstanceCPU_Error(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	api := &fakeAPI{respond: func(*sdkcw.GetMetricDataInput, int) (*sdkcw.GetMetricDataOutput, error) { return nil, boom }}
	_, err := newSource(t, api).InstanceCPU(t.Context(), period, []string{"i-a"})
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want wrapped %v", err, boom)
	}
}

func TestSource_InstanceCPU_InvalidPeriod(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{}
	_, err := newSource(t, api).InstanceCPU(t.Context(), ports.Period{Start: t0, End: t0}, []string{"i-a"})
	if err == nil || len(api.calls) != 0 {
		t.Fatalf("error = %v with %d calls, want error and no call", err, len(api.calls))
	}
}

func TestSource_LoadBalancer(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{respond: func(*sdkcw.GetMetricDataInput, int) (*sdkcw.GetMetricDataOutput, error) {
		return &sdkcw.GetMetricDataOutput{MetricDataResults: []cwtypes.MetricDataResult{
			result("m0", cwtypes.StatusCodeComplete, map[time.Time]float64{t0: 1200}),
			result("m1", cwtypes.StatusCodeComplete, map[time.Time]float64{t0: 0.25}),
			result("m2", cwtypes.StatusCodeComplete, nil),
			result("m3", cwtypes.StatusCodeComplete, map[time.Time]float64{t0: 3}),
			result("m4", cwtypes.StatusCodeComplete, map[time.Time]float64{t0: 400}),
			result("m5", cwtypes.StatusCodeInternalError, nil),
		}}, nil
	}}

	got, err := newSource(t, api).LoadBalancer(t.Context(), period)
	if err != nil {
		t.Fatalf("LoadBalancer() error = %v", err)
	}
	want := ports.LoadBalancerReadings{
		RequestCount:          core.Reading{Present: true, Value: 1200, Timestamp: t0},
		TargetResponseTimeP95: core.Reading{Present: true, Value: 250, Timestamp: t0},
		Target5xxCount:        core.Reading{},
		ELB5xxCount:           core.Reading{Present: true, Value: 3, Timestamp: t0},
		RequestCountPerTarget: core.Reading{Present: true, Value: 400, Timestamp: t0},
		HealthyHostCount:      core.Reading{FetchFailed: true},
	}
	if got != want {
		t.Errorf("LoadBalancer() =\n%+v\nwant\n%+v", got, want)
	}

	wantQueries := []struct {
		metric, stat string
		dims         map[string]string
	}{
		{"RequestCount", "Sum", map[string]string{"LoadBalancer": lbSuffix}},
		{"TargetResponseTime", "p95", map[string]string{"LoadBalancer": lbSuffix}},
		{"HTTPCode_Target_5XX_Count", "Sum", map[string]string{"LoadBalancer": lbSuffix}},
		{"HTTPCode_ELB_5XX_Count", "Sum", map[string]string{"LoadBalancer": lbSuffix}},
		{"RequestCountPerTarget", "Sum", map[string]string{"LoadBalancer": lbSuffix, "TargetGroup": tgSuffix}},
		{"HealthyHostCount", "Minimum", map[string]string{"LoadBalancer": lbSuffix, "TargetGroup": tgSuffix}},
	}
	qs := api.calls[0].MetricDataQueries
	if len(qs) != len(wantQueries) {
		t.Fatalf("queries = %d, want %d", len(qs), len(wantQueries))
	}
	for i, w := range wantQueries {
		ms := qs[i].MetricStat
		if aws.ToString(ms.Metric.Namespace) != "AWS/ApplicationELB" || aws.ToString(ms.Metric.MetricName) != w.metric || aws.ToString(ms.Stat) != w.stat {
			t.Errorf("query %d = %s %s %s, want AWS/ApplicationELB %s %s", i,
				aws.ToString(ms.Metric.Namespace), aws.ToString(ms.Metric.MetricName), aws.ToString(ms.Stat), w.metric, w.stat)
		}
		gotDims := map[string]string{}
		for _, d := range ms.Metric.Dimensions {
			gotDims[aws.ToString(d.Name)] = aws.ToString(d.Value)
		}
		if len(gotDims) != len(w.dims) {
			t.Errorf("query %d dimensions = %v, want %v", i, gotDims, w.dims)
			continue
		}
		for k, v := range w.dims {
			if gotDims[k] != v {
				t.Errorf("query %d dimension %s = %q, want %q", i, k, gotDims[k], v)
			}
		}
	}
}

func TestSource_LoadBalancer_Error(t *testing.T) {
	t.Parallel()
	boom := errors.New("throttled")
	api := &fakeAPI{respond: func(*sdkcw.GetMetricDataInput, int) (*sdkcw.GetMetricDataOutput, error) { return nil, boom }}
	if _, err := newSource(t, api).LoadBalancer(t.Context(), period); !errors.Is(err, boom) {
		t.Fatalf("error = %v, want wrapped %v", err, boom)
	}
}

func TestSource_Validate(t *testing.T) {
	t.Parallel()
	boom := errors.New("denied")
	found := &sdkcw.ListMetricsOutput{Metrics: []cwtypes.Metric{{MetricName: aws.String("HealthyHostCount")}}}
	tests := []struct {
		name    string
		api     *fakeAPI
		wantErr error
	}{
		{name: "found on first page", api: &fakeAPI{listPages: []*sdkcw.ListMetricsOutput{found}}},
		{name: "found on second page", api: &fakeAPI{listPages: []*sdkcw.ListMetricsOutput{{NextToken: aws.String("p2")}, found}}},
		{name: "not found", api: &fakeAPI{}, wantErr: cloudwatch.ErrMetricNotFound},
		{name: "api error", api: &fakeAPI{listErr: boom}, wantErr: boom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := newSource(t, tt.api).Validate(t.Context())
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
			} else if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.wantErr)
			}
			in := tt.api.listInputs[0]
			if aws.ToString(in.Namespace) != "AWS/ApplicationELB" || aws.ToString(in.MetricName) != "HealthyHostCount" || len(in.Dimensions) != 2 {
				t.Errorf("ListMetrics input = %+v", in)
			}
		})
	}
}

func TestNewFromConfig(t *testing.T) {
	t.Parallel()
	if _, err := cloudwatch.NewFromConfig(aws.Config{Region: "us-east-1"}, cloudwatch.Config{LoadBalancer: lbSuffix, TargetGroup: tgSuffix}); err != nil {
		t.Fatalf("NewFromConfig() error = %v", err)
	}
}
