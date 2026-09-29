package asg_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	astypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/smithy-go/middleware"

	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/asg"
	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

const (
	groupName = "asc-app"
	tgARN     = "arn:aws:elasticloadbalancing:us-east-1:000000000000:targetgroup/asc-tg/0123456789abcdef"
)

var t0 = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

type fakeAS struct {
	groupPages    []*autoscaling.DescribeAutoScalingGroupsOutput
	groupErr      error
	groupCalls    int
	activityPages []*autoscaling.DescribeScalingActivitiesOutput
	activityErr   error
	activityCalls int
	setErr        error
	setInputs     []*autoscaling.SetDesiredCapacityInput
	termErr       error
	termInputs    []*autoscaling.TerminateInstanceInAutoScalingGroupInput
}

func (f *fakeAS) DescribeAutoScalingGroups(_ context.Context, in *autoscaling.DescribeAutoScalingGroupsInput, _ ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error) {
	if f.groupErr != nil {
		return nil, f.groupErr
	}
	if len(in.AutoScalingGroupNames) != 1 || in.AutoScalingGroupNames[0] != groupName {
		return nil, errors.New("unexpected group filter")
	}
	i := f.groupCalls
	f.groupCalls++
	if i >= len(f.groupPages) {
		return &autoscaling.DescribeAutoScalingGroupsOutput{}, nil
	}
	return f.groupPages[i], nil
}

func (f *fakeAS) DescribeScalingActivities(_ context.Context, in *autoscaling.DescribeScalingActivitiesInput, _ ...func(*autoscaling.Options)) (*autoscaling.DescribeScalingActivitiesOutput, error) {
	if f.activityErr != nil {
		return nil, f.activityErr
	}
	if aws.ToString(in.AutoScalingGroupName) != groupName {
		return nil, errors.New("unexpected group")
	}
	i := f.activityCalls
	f.activityCalls++
	if i >= len(f.activityPages) {
		return &autoscaling.DescribeScalingActivitiesOutput{}, nil
	}
	return f.activityPages[i], nil
}

func withRequestID(id string) middleware.Metadata {
	var md middleware.Metadata
	awsmiddleware.SetRequestIDMetadata(&md, id)
	return md
}

func (f *fakeAS) SetDesiredCapacity(_ context.Context, in *autoscaling.SetDesiredCapacityInput, _ ...func(*autoscaling.Options)) (*autoscaling.SetDesiredCapacityOutput, error) {
	f.setInputs = append(f.setInputs, in)
	if f.setErr != nil {
		return nil, f.setErr
	}
	return &autoscaling.SetDesiredCapacityOutput{ResultMetadata: withRequestID("req-set")}, nil
}

func (f *fakeAS) TerminateInstanceInAutoScalingGroup(_ context.Context, in *autoscaling.TerminateInstanceInAutoScalingGroupInput, _ ...func(*autoscaling.Options)) (*autoscaling.TerminateInstanceInAutoScalingGroupOutput, error) {
	f.termInputs = append(f.termInputs, in)
	if f.termErr != nil {
		return nil, f.termErr
	}
	return &autoscaling.TerminateInstanceInAutoScalingGroupOutput{ResultMetadata: withRequestID("req-term")}, nil
}

type fakeELB struct {
	targets []elbtypes.TargetHealthDescription
	err     error
}

func (f *fakeELB) DescribeTargetHealth(_ context.Context, in *elbv2.DescribeTargetHealthInput, _ ...func(*elbv2.Options)) (*elbv2.DescribeTargetHealthOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	if aws.ToString(in.TargetGroupArn) != tgARN {
		return nil, errors.New("unexpected target group")
	}
	return &elbv2.DescribeTargetHealthOutput{TargetHealthDescriptions: f.targets}, nil
}

func target(id string, st elbtypes.TargetHealthStateEnum) elbtypes.TargetHealthDescription {
	return elbtypes.TargetHealthDescription{
		Target:       &elbtypes.TargetDescription{Id: aws.String(id)},
		TargetHealth: &elbtypes.TargetHealth{State: st},
	}
}

func instance(id string, lc astypes.LifecycleState) astypes.Instance {
	return astypes.Instance{InstanceId: aws.String(id), AvailabilityZone: aws.String("us-east-1a"), LifecycleState: lc}
}

func group(desired int32, instances ...astypes.Instance) *autoscaling.DescribeAutoScalingGroupsOutput {
	return &autoscaling.DescribeAutoScalingGroupsOutput{AutoScalingGroups: []astypes.AutoScalingGroup{{
		AutoScalingGroupName: aws.String(groupName),
		DesiredCapacity:      aws.Int32(desired),
		MinSize:              aws.Int32(1),
		MaxSize:              aws.Int32(5),
		Instances:            instances,
	}}}
}

func activity(id, desc string, status astypes.ScalingActivityStatusCode, started time.Time) astypes.Activity {
	return astypes.Activity{
		ActivityId: aws.String(id), Description: aws.String(desc), StatusCode: status, StartTime: aws.Time(started),
	}
}

func newProvisioner(t *testing.T, as *fakeAS, elb *fakeELB, now time.Time) *asg.Provisioner {
	t.Helper()
	p, err := asg.New(as, elb, asg.Config{GroupName: groupName, TargetGroupARN: tgARN, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return p
}

func TestNew(t *testing.T) {
	t.Parallel()
	now := func() time.Time { return t0 }
	valid := asg.Config{GroupName: groupName, TargetGroupARN: tgARN, Now: now}
	tests := []struct {
		name    string
		as      asg.AutoScalingAPI
		elb     asg.ELBAPI
		cfg     asg.Config
		wantErr bool
	}{
		{name: "valid", as: &fakeAS{}, elb: &fakeELB{}, cfg: valid},
		{name: "nil auto scaling api", elb: &fakeELB{}, cfg: valid, wantErr: true},
		{name: "nil elb api", as: &fakeAS{}, cfg: valid, wantErr: true},
		{name: "missing group", as: &fakeAS{}, elb: &fakeELB{}, cfg: asg.Config{TargetGroupARN: tgARN, Now: now}, wantErr: true},
		{name: "missing target group", as: &fakeAS{}, elb: &fakeELB{}, cfg: asg.Config{GroupName: groupName, Now: now}, wantErr: true},
		{name: "missing now", as: &fakeAS{}, elb: &fakeELB{}, cfg: asg.Config{GroupName: groupName, TargetGroupARN: tgARN}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := asg.New(tt.as, tt.elb, tt.cfg); (err != nil) != tt.wantErr {
				t.Fatalf("New() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestProvisioner_DescribeCapacity_StateMapping(t *testing.T) {
	t.Parallel()
	as := &fakeAS{
		groupPages: []*autoscaling.DescribeAutoScalingGroupsOutput{group(6,
			instance("i-00000001", astypes.LifecycleStateInService),
			instance("i-00000002", astypes.LifecycleStateInService),
			instance("i-00000003", astypes.LifecycleStatePending),
			instance("i-00000004", astypes.LifecycleStateTerminatingWait),
			instance("i-00000005", astypes.LifecycleStateInService),
			instance("i-00000006", astypes.LifecycleStateTerminated),
			instance("i-00000007", astypes.LifecycleStateStandby),
		)},
		activityPages: []*autoscaling.DescribeScalingActivitiesOutput{{Activities: []astypes.Activity{
			activity("a1", "Launching a new EC2 instance: i-00000001", astypes.ScalingActivityStatusCodeSuccessful, t0.Add(-10*time.Minute)),
			activity("a0", "Launching a new EC2 instance: i-00000002", astypes.ScalingActivityStatusCodeSuccessful, t0.Add(-3*time.Hour)),
		}}},
	}
	elb := &fakeELB{targets: []elbtypes.TargetHealthDescription{
		target("i-00000001", elbtypes.TargetHealthStateEnumHealthy),
		target("i-00000002", elbtypes.TargetHealthStateEnumUnhealthy),
		target("i-00000003", elbtypes.TargetHealthStateEnumInitial),
		target("i-00000004", elbtypes.TargetHealthStateEnumHealthy),
		target("i-00000005", elbtypes.TargetHealthStateEnumDraining),
		target("i-00000008", elbtypes.TargetHealthStateEnumHealthy),
		{},
	}}
	p := newProvisioner(t, as, elb, t0)

	snap, err := p.DescribeCapacity(t.Context())
	if err != nil {
		t.Fatalf("DescribeCapacity() error = %v", err)
	}
	if !snap.Known || snap.Desired != 6 || snap.Min != 1 || snap.Max != 5 {
		t.Errorf("snapshot header = %+v", snap)
	}
	if snap.HealthyTargets != 3 {
		t.Errorf("healthy targets = %d, want 3", snap.HealthyTargets)
	}
	want := []core.Instance{
		{ID: "i-00000001", AZ: "us-east-1a", State: core.InstanceInService, LaunchedAt: t0.Add(-10 * time.Minute)},
		{ID: "i-00000002", AZ: "us-east-1a", State: core.InstancePending, LaunchedAt: t0}, // launch activity outside lookback
		{ID: "i-00000003", AZ: "us-east-1a", State: core.InstancePending, LaunchedAt: t0},
		{ID: "i-00000004", AZ: "us-east-1a", State: core.InstanceDraining, LaunchedAt: t0},
		{ID: "i-00000005", AZ: "us-east-1a", State: core.InstanceDraining, LaunchedAt: t0},
		{ID: "i-00000007", AZ: "us-east-1a", State: core.InstancePending, LaunchedAt: t0},
	}
	if len(snap.Instances) != len(want) {
		t.Fatalf("instances = %+v, want %+v", snap.Instances, want)
	}
	for i := range want {
		if snap.Instances[i] != want[i] {
			t.Errorf("instance %d = %+v, want %+v", i, snap.Instances[i], want[i])
		}
	}
}

func TestProvisioner_DescribeCapacity_CachesLaunchTimes(t *testing.T) {
	t.Parallel()
	page := group(1, instance("i-00000001", astypes.LifecycleStateInService))
	as := &fakeAS{groupPages: []*autoscaling.DescribeAutoScalingGroupsOutput{page, page}}
	elb := &fakeELB{targets: []elbtypes.TargetHealthDescription{target("i-00000001", elbtypes.TargetHealthStateEnumHealthy)}}
	p := newProvisioner(t, as, elb, t0)

	first, err := p.DescribeCapacity(t.Context())
	if err != nil {
		t.Fatalf("first DescribeCapacity() error = %v", err)
	}
	second, err := p.DescribeCapacity(t.Context())
	if err != nil {
		t.Fatalf("second DescribeCapacity() error = %v", err)
	}
	if as.activityCalls != 1 {
		t.Errorf("activity reads = %d, want 1 (launch time cached)", as.activityCalls)
	}
	if !first.Instances[0].LaunchedAt.Equal(second.Instances[0].LaunchedAt) {
		t.Errorf("launch time changed: %v then %v", first.Instances[0].LaunchedAt, second.Instances[0].LaunchedAt)
	}
}

func TestProvisioner_DescribeCapacity_Errors(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	ok := []*autoscaling.DescribeAutoScalingGroupsOutput{group(1, instance("i-00000001", astypes.LifecycleStatePending))}
	tests := []struct {
		name string
		as   *fakeAS
		elb  *fakeELB
		want error
	}{
		{name: "group api error", as: &fakeAS{groupErr: boom}, elb: &fakeELB{}, want: boom},
		{name: "group not found", as: &fakeAS{}, elb: &fakeELB{}, want: asg.ErrGroupNotFound},
		{name: "target health error", as: &fakeAS{groupPages: ok}, elb: &fakeELB{err: boom}, want: boom},
		{name: "activity error", as: &fakeAS{groupPages: ok, activityErr: boom}, elb: &fakeELB{}, want: boom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			snap, err := newProvisioner(t, tt.as, tt.elb, t0).DescribeCapacity(t.Context())
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if snap.Known {
				t.Error("snapshot Known = true on error")
			}
		})
	}
}

func TestProvisioner_SetDesiredCapacity(t *testing.T) {
	t.Parallel()
	as := &fakeAS{}
	p := newProvisioner(t, as, &fakeELB{}, t0)

	res, err := p.SetDesiredCapacity(t.Context(), 3)
	if err != nil {
		t.Fatalf("SetDesiredCapacity() error = %v", err)
	}
	if res.RequestID != "req-set" {
		t.Errorf("request id = %q, want req-set", res.RequestID)
	}
	in := as.setInputs[0]
	if aws.ToString(in.AutoScalingGroupName) != groupName || aws.ToInt32(in.DesiredCapacity) != 3 {
		t.Errorf("input = %+v, want absolute desired 3 for %s", in, groupName)
	}
	if in.HonorCooldown == nil || *in.HonorCooldown {
		t.Error("HonorCooldown must be explicitly false")
	}
}

func TestProvisioner_SetDesiredCapacity_Errors(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	if _, err := newProvisioner(t, &fakeAS{setErr: boom}, &fakeELB{}, t0).SetDesiredCapacity(t.Context(), 2); !errors.Is(err, boom) {
		t.Errorf("api error = %v, want %v", err, boom)
	}
	as := &fakeAS{}
	if _, err := newProvisioner(t, as, &fakeELB{}, t0).SetDesiredCapacity(t.Context(), -1); err == nil || len(as.setInputs) != 0 {
		t.Errorf("negative desired: error = %v, calls = %d; want error, no call", err, len(as.setInputs))
	}
}

func TestProvisioner_TerminateInstance(t *testing.T) {
	t.Parallel()
	as := &fakeAS{}
	p := newProvisioner(t, as, &fakeELB{}, t0)

	res, err := p.TerminateInstance(t.Context(), "i-00000001")
	if err != nil {
		t.Fatalf("TerminateInstance() error = %v", err)
	}
	if res.RequestID != "req-term" {
		t.Errorf("request id = %q, want req-term", res.RequestID)
	}
	in := as.termInputs[0]
	if aws.ToString(in.InstanceId) != "i-00000001" || !aws.ToBool(in.ShouldDecrementDesiredCapacity) {
		t.Errorf("input = %+v, want i-00000001 with decrement", in)
	}
}

func TestProvisioner_TerminateInstance_Errors(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	if _, err := newProvisioner(t, &fakeAS{termErr: boom}, &fakeELB{}, t0).TerminateInstance(t.Context(), "i-00000001"); !errors.Is(err, boom) {
		t.Errorf("api error = %v, want %v", err, boom)
	}
	if _, err := newProvisioner(t, &fakeAS{}, &fakeELB{}, t0).TerminateInstance(t.Context(), ""); err == nil {
		t.Error("empty instance id: want error")
	}
}

func TestProvisioner_ScalingActivities(t *testing.T) {
	t.Parallel()
	as := &fakeAS{activityPages: []*autoscaling.DescribeScalingActivitiesOutput{
		{NextToken: aws.String("p2"), Activities: []astypes.Activity{
			activity("a4", "Launching a new EC2 instance: i-0000000a", astypes.ScalingActivityStatusCodeInProgress, t0.Add(4*time.Minute)),
			activity("a3", "Launching a new EC2 instance.  Status Reason: capacity", astypes.ScalingActivityStatusCodeFailed, t0.Add(3*time.Minute)),
		}},
		{NextToken: aws.String("p3"), Activities: []astypes.Activity{
			activity("a2", "Terminating EC2 instance: i-0000000b", astypes.ScalingActivityStatusCodeSuccessful, t0.Add(2*time.Minute)),
			activity("a1", "Launching a new EC2 instance: i-0000000c", astypes.ScalingActivityStatusCodeCancelled, t0),
			activity("a0", "Launching a new EC2 instance: i-0000000d", astypes.ScalingActivityStatusCodeSuccessful, t0.Add(-time.Second)),
		}},
		{Activities: []astypes.Activity{
			activity("old", "Launching a new EC2 instance: i-0000000e", astypes.ScalingActivityStatusCodeSuccessful, t0.Add(-time.Hour)),
		}},
	}}
	p := newProvisioner(t, as, &fakeELB{}, t0)

	got, err := p.ScalingActivities(t.Context(), t0)
	if err != nil {
		t.Fatalf("ScalingActivities() error = %v", err)
	}
	if as.activityCalls != 2 {
		t.Errorf("pages read = %d, want 2 (stop at the first older activity)", as.activityCalls)
	}
	want := []ports.ScalingActivity{
		{ID: "a4", InstanceID: "i-0000000a", Launch: true, Status: ports.ActivityInProgress, StartedAt: t0.Add(4 * time.Minute)},
		{ID: "a3", Launch: true, Status: ports.ActivityFailed, StartedAt: t0.Add(3 * time.Minute)},
		{ID: "a2", InstanceID: "i-0000000b", Launch: false, Status: ports.ActivitySuccessful, StartedAt: t0.Add(2 * time.Minute)},
		{ID: "a1", InstanceID: "i-0000000c", Launch: true, Status: ports.ActivityFailed, StartedAt: t0},
	}
	if len(got) != len(want) {
		t.Fatalf("activities = %+v, want %d", got, len(want))
	}
	for i := range want {
		g := got[i]
		g.Description = ""
		if g != want[i] {
			t.Errorf("activity %d = %+v, want %+v", i, g, want[i])
		}
	}
}

func TestProvisioner_ScalingActivities_Error(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	if _, err := newProvisioner(t, &fakeAS{activityErr: boom}, &fakeELB{}, t0).ScalingActivities(t.Context(), t0); !errors.Is(err, boom) {
		t.Fatalf("error = %v, want %v", err, boom)
	}
}

func TestNewFromConfig(t *testing.T) {
	t.Parallel()
	cfg := asg.Config{GroupName: groupName, TargetGroupARN: tgARN, Now: func() time.Time { return t0 }}
	if _, err := asg.NewFromConfig(aws.Config{Region: "us-east-1"}, cfg); err != nil {
		t.Fatalf("NewFromConfig() error = %v", err)
	}
}
