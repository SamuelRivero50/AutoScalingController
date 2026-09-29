// Package asg implements the InstanceProvisioner port with the EC2 Auto
// Scaling and Elastic Load Balancing v2 APIs. The Auto Scaling group is the
// actuator only (ADR-0009): the adapter reads capacity and target health,
// sets absolute desired capacities and terminates stuck instances.
package asg

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	astypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/smithy-go/middleware"

	"github.com/SamuelRivero50/AutoScalingController/internal/core"
	"github.com/SamuelRivero50/AutoScalingController/internal/ports"
)

// AutoScalingAPI is the subset of the Auto Scaling client the adapter
// calls. Together with ELBAPI it is the exact list of operations the
// controller may perform, checked against infra/iam/controller-policy.json
// by a test.
type AutoScalingAPI interface {
	DescribeAutoScalingGroups(ctx context.Context, in *autoscaling.DescribeAutoScalingGroupsInput, optFns ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error)
	DescribeScalingActivities(ctx context.Context, in *autoscaling.DescribeScalingActivitiesInput, optFns ...func(*autoscaling.Options)) (*autoscaling.DescribeScalingActivitiesOutput, error)
	SetDesiredCapacity(ctx context.Context, in *autoscaling.SetDesiredCapacityInput, optFns ...func(*autoscaling.Options)) (*autoscaling.SetDesiredCapacityOutput, error)
	TerminateInstanceInAutoScalingGroup(ctx context.Context, in *autoscaling.TerminateInstanceInAutoScalingGroupInput, optFns ...func(*autoscaling.Options)) (*autoscaling.TerminateInstanceInAutoScalingGroupOutput, error)
}

// ELBAPI is the subset of the Elastic Load Balancing v2 client the adapter
// calls.
type ELBAPI interface {
	DescribeTargetHealth(ctx context.Context, in *elbv2.DescribeTargetHealthInput, optFns ...func(*elbv2.Options)) (*elbv2.DescribeTargetHealthOutput, error)
}

var (
	_ AutoScalingAPI            = (*autoscaling.Client)(nil)
	_ ELBAPI                    = (*elbv2.Client)(nil)
	_ ports.InstanceProvisioner = (*Provisioner)(nil)
)

// ErrGroupNotFound is returned when the configured Auto Scaling group does
// not exist.
var ErrGroupNotFound = errors.New("asg: auto scaling group not found")

// instanceIDPattern extracts an EC2 instance ID from an activity
// description such as "Launching a new EC2 instance: i-0123456789abcdef0".
var instanceIDPattern = regexp.MustCompile(`\bi-[0-9a-f]{8,17}\b`)

// launchLookback bounds the activity read used to find launch times.
const launchLookback = 2 * time.Hour

// Config identifies the Auto Scaling group and its target group.
type Config struct {
	GroupName      string
	TargetGroupARN string
	// Now returns the current time, used as the fallback launch time for
	// an instance whose launch activity is not found. Required.
	Now func() time.Time
}

// Provisioner is the real InstanceProvisioner. It is safe for concurrent
// use.
type Provisioner struct {
	as  AutoScalingAPI
	elb ELBAPI
	cfg Config

	mu         sync.Mutex
	launchedAt map[string]time.Time // instance ID -> launch time
}

// NewFromConfig returns a Provisioner backed by Auto Scaling and ELBv2
// clients built from awsCfg (see package awsclient).
func NewFromConfig(awsCfg aws.Config, cfg Config) (*Provisioner, error) {
	return New(autoscaling.NewFromConfig(awsCfg), elbv2.NewFromConfig(awsCfg), cfg)
}

// New returns a Provisioner.
func New(as AutoScalingAPI, elb ELBAPI, cfg Config) (*Provisioner, error) {
	if as == nil || elb == nil {
		return nil, errors.New("asg: auto scaling and elb apis are required")
	}
	if cfg.GroupName == "" || cfg.TargetGroupARN == "" {
		return nil, errors.New("asg: group name and target group arn are required")
	}
	if cfg.Now == nil {
		return nil, errors.New("asg: now function is required")
	}
	return &Provisioner{as: as, elb: elb, cfg: cfg, launchedAt: map[string]time.Time{}}, nil
}

// DescribeCapacity returns the ASG and target-health ground truth.
func (p *Provisioner) DescribeCapacity(ctx context.Context) (core.CapacitySnapshot, error) {
	group, err := p.describeGroup(ctx)
	if err != nil {
		return core.CapacitySnapshot{}, err
	}
	health, err := p.targetHealth(ctx)
	if err != nil {
		return core.CapacitySnapshot{}, err
	}
	snap := core.CapacitySnapshot{
		Known:   true,
		Desired: int(aws.ToInt32(group.DesiredCapacity)),
		Min:     int(aws.ToInt32(group.MinSize)),
		Max:     int(aws.ToInt32(group.MaxSize)),
	}
	for _, st := range health {
		if st == elbtypes.TargetHealthStateEnumHealthy {
			snap.HealthyTargets++
		}
	}
	for _, in := range group.Instances {
		id := aws.ToString(in.InstanceId)
		state, keep := instanceState(in.LifecycleState, health[id])
		if !keep {
			continue
		}
		snap.Instances = append(snap.Instances, core.Instance{
			ID: id, AZ: aws.ToString(in.AvailabilityZone), State: state,
		})
	}
	if err := p.fillLaunchTimes(ctx, snap.Instances); err != nil {
		return core.CapacitySnapshot{}, err
	}
	slices.SortFunc(snap.Instances, func(a, b core.Instance) int { return strings.Compare(a.ID, b.ID) })
	return snap, nil
}

func (p *Provisioner) describeGroup(ctx context.Context) (astypes.AutoScalingGroup, error) {
	pager := autoscaling.NewDescribeAutoScalingGroupsPaginator(p.as, &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{p.cfg.GroupName},
	})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return astypes.AutoScalingGroup{}, fmt.Errorf("describe auto scaling groups: %w", err)
		}
		for _, g := range page.AutoScalingGroups {
			if aws.ToString(g.AutoScalingGroupName) == p.cfg.GroupName {
				return g, nil
			}
		}
	}
	return astypes.AutoScalingGroup{}, fmt.Errorf("%w: %s", ErrGroupNotFound, p.cfg.GroupName)
}

// targetHealth returns the target-health state of every registered target.
// DescribeTargetHealth is not paginated.
func (p *Provisioner) targetHealth(ctx context.Context) (map[string]elbtypes.TargetHealthStateEnum, error) {
	out, err := p.elb.DescribeTargetHealth(ctx, &elbv2.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(p.cfg.TargetGroupARN),
	})
	if err != nil {
		return nil, fmt.Errorf("describe target health: %w", err)
	}
	health := make(map[string]elbtypes.TargetHealthStateEnum, len(out.TargetHealthDescriptions))
	for _, d := range out.TargetHealthDescriptions {
		if d.Target == nil || d.TargetHealth == nil {
			continue
		}
		health[aws.ToString(d.Target.Id)] = d.TargetHealth.State
	}
	return health, nil
}

// instanceState maps the ASG lifecycle state and the target-health state to
// the controller's view (docs/spec/lifecycle-and-failures.md §4). keep is
// false for instances that are gone. Unknown or unused lifecycle states
// (standby, root-volume replacement, warm pool) are PENDING: not in
// service, so they never count toward N.
func instanceState(lc astypes.LifecycleState, target elbtypes.TargetHealthStateEnum) (state core.InstanceState, keep bool) {
	switch {
	case lc == astypes.LifecycleStateTerminated || lc == astypes.LifecycleStateDetached:
		return "", false
	case strings.HasPrefix(string(lc), "Terminating") || lc == astypes.LifecycleStateDetaching,
		target == elbtypes.TargetHealthStateEnumDraining || target == elbtypes.TargetHealthStateEnumUnhealthyDraining:
		return core.InstanceDraining, true
	case lc == astypes.LifecycleStateInService && target == elbtypes.TargetHealthStateEnumHealthy:
		return core.InstanceInService, true
	default:
		return core.InstancePending, true
	}
}

// fillLaunchTimes sets LaunchedAt from the cached launch time, reading the
// launch activities once when an instance is new, and falling back to the
// first time the instance was seen.
func (p *Provisioner) fillLaunchTimes(ctx context.Context, instances []core.Instance) error {
	p.mu.Lock()
	missing := false
	for _, in := range instances {
		if _, ok := p.launchedAt[in.ID]; !ok {
			missing = true
			break
		}
	}
	p.mu.Unlock()

	if missing {
		acts, err := p.ScalingActivities(ctx, p.cfg.Now().Add(-launchLookback))
		if err != nil {
			return err
		}
		p.mu.Lock()
		for _, a := range acts {
			if a.Launch && a.InstanceID != "" {
				if _, ok := p.launchedAt[a.InstanceID]; !ok {
					p.launchedAt[a.InstanceID] = a.StartedAt
				}
			}
		}
		p.mu.Unlock()
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.cfg.Now()
	present := make(map[string]bool, len(instances))
	for i := range instances {
		id := instances[i].ID
		present[id] = true
		t, ok := p.launchedAt[id]
		if !ok {
			t = now
			p.launchedAt[id] = t
		}
		instances[i].LaunchedAt = t
	}
	for id := range p.launchedAt {
		if !present[id] {
			delete(p.launchedAt, id)
		}
	}
	return nil
}

// SetDesiredCapacity sets an absolute desired capacity without honoring the
// ASG cooldown (docs/spec/lifecycle-and-failures.md §3).
func (p *Provisioner) SetDesiredCapacity(ctx context.Context, desired int) (ports.ActionResult, error) {
	if desired < 0 || desired > math.MaxInt32 {
		return ports.ActionResult{}, fmt.Errorf("asg: invalid desired capacity %d", desired)
	}
	out, err := p.as.SetDesiredCapacity(ctx, &autoscaling.SetDesiredCapacityInput{
		AutoScalingGroupName: aws.String(p.cfg.GroupName),
		DesiredCapacity:      aws.Int32(int32(desired)),
		HonorCooldown:        aws.Bool(false),
	})
	if err != nil {
		return ports.ActionResult{}, fmt.Errorf("set desired capacity: %w", err)
	}
	return ports.ActionResult{RequestID: requestID(out.ResultMetadata)}, nil
}

// TerminateInstance terminates one instance and decrements desired
// capacity, so the ASG does not replace it.
func (p *Provisioner) TerminateInstance(ctx context.Context, instanceID string) (ports.ActionResult, error) {
	if instanceID == "" {
		return ports.ActionResult{}, errors.New("asg: instance id is required")
	}
	out, err := p.as.TerminateInstanceInAutoScalingGroup(ctx, &autoscaling.TerminateInstanceInAutoScalingGroupInput{
		InstanceId:                     aws.String(instanceID),
		ShouldDecrementDesiredCapacity: aws.Bool(true),
	})
	if err != nil {
		return ports.ActionResult{}, fmt.Errorf("terminate instance %s: %w", instanceID, err)
	}
	return ports.ActionResult{RequestID: requestID(out.ResultMetadata)}, nil
}

// ScalingActivities returns the group's activities started at or after
// since, newest first. Pages are read until an older activity appears.
func (p *Provisioner) ScalingActivities(ctx context.Context, since time.Time) ([]ports.ScalingActivity, error) {
	pager := autoscaling.NewDescribeScalingActivitiesPaginator(p.as, &autoscaling.DescribeScalingActivitiesInput{
		AutoScalingGroupName: aws.String(p.cfg.GroupName),
	})
	var acts []ports.ScalingActivity
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describe scaling activities: %w", err)
		}
		older := false
		for _, a := range page.Activities {
			started := aws.ToTime(a.StartTime)
			if started.Before(since) {
				older = true
				continue
			}
			acts = append(acts, toActivity(a))
		}
		if older {
			break
		}
	}
	return acts, nil
}

func toActivity(a astypes.Activity) ports.ScalingActivity {
	desc := aws.ToString(a.Description)
	return ports.ScalingActivity{
		ID:          aws.ToString(a.ActivityId),
		InstanceID:  instanceIDPattern.FindString(desc),
		Launch:      strings.HasPrefix(desc, "Launching"),
		Status:      activityStatus(a.StatusCode),
		StartedAt:   aws.ToTime(a.StartTime),
		Description: desc,
	}
}

func activityStatus(s astypes.ScalingActivityStatusCode) ports.ActivityStatus {
	switch s {
	case astypes.ScalingActivityStatusCodeSuccessful:
		return ports.ActivitySuccessful
	case astypes.ScalingActivityStatusCodeFailed, astypes.ScalingActivityStatusCodeCancelled:
		return ports.ActivityFailed
	default:
		return ports.ActivityInProgress
	}
}

func requestID(md middleware.Metadata) string {
	id, _ := awsmiddleware.GetRequestIDMetadata(md)
	return id
}
