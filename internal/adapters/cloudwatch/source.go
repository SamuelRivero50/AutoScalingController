// Package cloudwatch implements the MetricsSource port with Amazon
// CloudWatch GetMetricData (docs/spec/signals.md). It reads exactly one
// closed aggregation period per call and never interprets missing
// datapoints: the decision core owns the absent-metric semantics
// (docs/spec/lifecycle-and-failures.md §1).
package cloudwatch

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

// API is the subset of the CloudWatch client the adapter calls. It is also
// the exact list of CloudWatch operations the controller may perform, and
// is checked against infra/iam/controller-policy.json by a test.
type API interface {
	GetMetricData(ctx context.Context, in *cloudwatch.GetMetricDataInput, optFns ...func(*cloudwatch.Options)) (*cloudwatch.GetMetricDataOutput, error)
	ListMetrics(ctx context.Context, in *cloudwatch.ListMetricsInput, optFns ...func(*cloudwatch.Options)) (*cloudwatch.ListMetricsOutput, error)
}

var (
	_ API                 = (*cloudwatch.Client)(nil)
	_ ports.MetricsSource = (*Source)(nil)
)

// ErrMetricNotFound is returned by Validate when an expected metric does not
// exist (yet) in CloudWatch.
var ErrMetricNotFound = errors.New("cloudwatch: expected metric not found")

// CloudWatch namespaces, metric names and dimension names.
const (
	namespaceEC2 = "AWS/EC2"
	namespaceALB = "AWS/ApplicationELB"

	metricCPU                   = "CPUUtilization"
	metricRequestCount          = "RequestCount"
	metricTargetResponseTime    = "TargetResponseTime"
	metricTarget5xx             = "HTTPCode_Target_5XX_Count"
	metricELB5xx                = "HTTPCode_ELB_5XX_Count"
	metricRequestCountPerTarget = "RequestCountPerTarget"
	metricHealthyHostCount      = "HealthyHostCount"

	dimInstanceID   = "InstanceId"
	dimLoadBalancer = "LoadBalancer"
	dimTargetGroup  = "TargetGroup"

	statAverage = "Average"
	statSum     = "Sum"
	statMinimum = "Minimum"
	statP95     = "p95"
)

// Config identifies the load balancer and target group.
type Config struct {
	// LoadBalancer is the ALB ARN suffix, e.g. app/my-alb/0123456789abcdef.
	LoadBalancer string
	// TargetGroup is the target-group ARN suffix, e.g.
	// targetgroup/my-tg/0123456789abcdef.
	TargetGroup string
}

// Source reads the controller's signals from CloudWatch. It is safe for
// concurrent use.
type Source struct {
	api API
	cfg Config
}

// NewFromConfig returns a Source backed by a CloudWatch client built from
// awsCfg (see package awsclient).
func NewFromConfig(awsCfg aws.Config, cfg Config) (*Source, error) {
	return New(cloudwatch.NewFromConfig(awsCfg), cfg)
}

// New returns a Source. api is usually *cloudwatch.Client.
func New(api API, cfg Config) (*Source, error) {
	if api == nil {
		return nil, errors.New("cloudwatch: api is required")
	}
	if cfg.LoadBalancer == "" || cfg.TargetGroup == "" {
		return nil, errors.New("cloudwatch: load balancer and target group are required")
	}
	return &Source{api: api, cfg: cfg}, nil
}

// query describes one metric to read in a GetMetricData batch.
type query struct {
	namespace string
	metric    string
	stat      string
	dims      []cwtypes.Dimension
}

func dim(name, value string) cwtypes.Dimension {
	return cwtypes.Dimension{Name: aws.String(name), Value: aws.String(value)}
}

// InstanceCPU returns the Average CPUUtilization of each instance for p.
func (s *Source) InstanceCPU(ctx context.Context, p ports.Period, instanceIDs []string) ([]core.InstanceCPU, error) {
	if len(instanceIDs) == 0 {
		return []core.InstanceCPU{}, nil
	}
	qs := make([]query, 0, len(instanceIDs))
	for _, id := range instanceIDs {
		qs = append(qs, query{
			namespace: namespaceEC2, metric: metricCPU, stat: statAverage,
			dims: []cwtypes.Dimension{dim(dimInstanceID, id)},
		})
	}
	readings, err := s.fetch(ctx, p, qs)
	if err != nil {
		return nil, fmt.Errorf("fetch instance cpu: %w", err)
	}
	out := make([]core.InstanceCPU, 0, len(instanceIDs))
	for i, id := range instanceIDs {
		out = append(out, core.InstanceCPU{InstanceID: id, Reading: readings[i]})
	}
	return out, nil
}

// LoadBalancer returns the ALB and target-group readings for p.
// TargetResponseTime is converted from seconds to milliseconds.
func (s *Source) LoadBalancer(ctx context.Context, p ports.Period) (ports.LoadBalancerReadings, error) {
	lb := dim(dimLoadBalancer, s.cfg.LoadBalancer)
	tg := dim(dimTargetGroup, s.cfg.TargetGroup)
	lbOnly := []cwtypes.Dimension{lb}
	lbAndTG := []cwtypes.Dimension{tg, lb}
	qs := []query{
		{namespace: namespaceALB, metric: metricRequestCount, stat: statSum, dims: lbOnly},
		{namespace: namespaceALB, metric: metricTargetResponseTime, stat: statP95, dims: lbOnly},
		{namespace: namespaceALB, metric: metricTarget5xx, stat: statSum, dims: lbOnly},
		{namespace: namespaceALB, metric: metricELB5xx, stat: statSum, dims: lbOnly},
		{namespace: namespaceALB, metric: metricRequestCountPerTarget, stat: statSum, dims: lbAndTG},
		{namespace: namespaceALB, metric: metricHealthyHostCount, stat: statMinimum, dims: lbAndTG},
	}
	r, err := s.fetch(ctx, p, qs)
	if err != nil {
		return ports.LoadBalancerReadings{}, fmt.Errorf("fetch load balancer metrics: %w", err)
	}
	latency := r[1]
	latency.Value *= 1000 // seconds -> milliseconds
	return ports.LoadBalancerReadings{
		RequestCount:          r[0],
		TargetResponseTimeP95: latency,
		Target5xxCount:        r[2],
		ELB5xxCount:           r[3],
		RequestCountPerTarget: r[4],
		HealthyHostCount:      r[5],
	}, nil
}

// fetch runs one paginated GetMetricData call for qs over p and returns one
// reading per query, in order. A query whose result reports an internal or
// forbidden status is returned as FetchFailed.
func (s *Source) fetch(ctx context.Context, p ports.Period, qs []query) ([]core.Reading, error) {
	secs := int64(p.End.Sub(p.Start) / time.Second)
	if secs <= 0 || secs > math.MaxInt32 {
		return nil, fmt.Errorf("invalid period [%s, %s)", p.Start, p.End)
	}
	period := int32(secs)
	mdq := make([]cwtypes.MetricDataQuery, 0, len(qs))
	for i, q := range qs {
		mdq = append(mdq, cwtypes.MetricDataQuery{
			Id: aws.String(queryID(i)),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  aws.String(q.namespace),
					MetricName: aws.String(q.metric),
					Dimensions: q.dims,
				},
				Period: aws.Int32(period),
				Stat:   aws.String(q.stat),
			},
			ReturnData: aws.Bool(true),
		})
	}
	in := &cloudwatch.GetMetricDataInput{
		StartTime:         aws.Time(p.Start),
		EndTime:           aws.Time(p.End),
		MetricDataQueries: mdq,
		ScanBy:            cwtypes.ScanByTimestampDescending,
	}

	readings := make([]core.Reading, len(qs))
	pager := cloudwatch.NewGetMetricDataPaginator(s.api, in)
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("get metric data: %w", err)
		}
		for _, res := range page.MetricDataResults {
			i, ok := queryIndex(aws.ToString(res.Id), len(qs))
			if !ok {
				continue
			}
			mergeResult(&readings[i], res)
		}
	}
	return readings, nil
}

// mergeResult folds one (possibly partial) result page into r, keeping the
// most recent datapoint.
func mergeResult(r *core.Reading, res cwtypes.MetricDataResult) {
	switch res.StatusCode {
	case cwtypes.StatusCodeInternalError, cwtypes.StatusCodeForbidden:
		if !r.Present {
			r.FetchFailed = true
		}
		return
	}
	for j, ts := range res.Timestamps {
		if j >= len(res.Values) {
			break
		}
		if !r.Present || ts.After(r.Timestamp) {
			*r = core.Reading{Present: true, Value: res.Values[j], Timestamp: ts}
		}
	}
}

func queryID(i int) string { return "m" + strconv.Itoa(i) }

func queryIndex(id string, n int) (int, bool) {
	if len(id) < 2 || id[0] != 'm' {
		return 0, false
	}
	i, err := strconv.Atoi(id[1:])
	if err != nil || i < 0 || i >= n {
		return 0, false
	}
	return i, true
}

// Validate checks with ListMetrics that the target group's HealthyHostCount
// metric exists for the configured load balancer, which proves both
// dimension values are right. ALB traffic metrics only appear after the
// first request, so they are not required. It returns an error wrapping
// ErrMetricNotFound when the metric does not exist yet.
func (s *Source) Validate(ctx context.Context) error {
	in := &cloudwatch.ListMetricsInput{
		Namespace:  aws.String(namespaceALB),
		MetricName: aws.String(metricHealthyHostCount),
		Dimensions: []cwtypes.DimensionFilter{
			{Name: aws.String(dimLoadBalancer), Value: aws.String(s.cfg.LoadBalancer)},
			{Name: aws.String(dimTargetGroup), Value: aws.String(s.cfg.TargetGroup)},
		},
	}
	pager := cloudwatch.NewListMetricsPaginator(s.api, in)
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("list metrics: %w", err)
		}
		if len(page.Metrics) > 0 {
			return nil
		}
	}
	return fmt.Errorf("%w: %s/%s for %s, %s", ErrMetricNotFound, namespaceALB, metricHealthyHostCount, s.cfg.LoadBalancer, s.cfg.TargetGroup)
}
