package awsiam_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/asg"
	"github.com/SamuelRivero50/AutoScalingController/internal/adapters/cloudwatch"
)

// policyPath is the least-privilege policy, relative to this package.
var policyPath = filepath.Join("..", "..", "..", "infra", "iam", "controller-policy.json")

// apis are the consumer-side interfaces of the real adapters. They list
// every AWS operation the controller code can call.
var apis = []struct {
	name  string
	iface reflect.Type
}{
	{"cloudwatch.API", reflect.TypeFor[cloudwatch.API]()},
	{"asg.AutoScalingAPI", reflect.TypeFor[asg.AutoScalingAPI]()},
	{"asg.ELBAPI", reflect.TypeFor[asg.ELBAPI]()},
}

// actions maps each interface method to the IAM action it requires.
var actions = map[string]string{
	"cloudwatch.API.GetMetricData":                           "cloudwatch:GetMetricData",
	"cloudwatch.API.ListMetrics":                             "cloudwatch:ListMetrics",
	"asg.AutoScalingAPI.DescribeAutoScalingGroups":           "autoscaling:DescribeAutoScalingGroups",
	"asg.AutoScalingAPI.DescribeScalingActivities":           "autoscaling:DescribeScalingActivities",
	"asg.AutoScalingAPI.SetDesiredCapacity":                  "autoscaling:SetDesiredCapacity",
	"asg.AutoScalingAPI.TerminateInstanceInAutoScalingGroup": "autoscaling:TerminateInstanceInAutoScalingGroup",
	"asg.ELBAPI.DescribeTargetHealth":                        "elasticloadbalancing:DescribeTargetHealth",
}

type policy struct {
	Statement []struct {
		Effect string `json:"Effect"`
		Action any    `json:"Action"` // string or []string
	} `json:"Statement"`
}

func allowedActions(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatalf("read policy: %v", err)
	}
	var p policy
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	var out []string
	for _, s := range p.Statement {
		if s.Effect != "Allow" {
			continue
		}
		switch a := s.Action.(type) {
		case string:
			out = append(out, a)
		case []any:
			for _, v := range a {
				str, ok := v.(string)
				if !ok {
					t.Fatalf("policy action %v is not a string", v)
				}
				out = append(out, str)
			}
		default:
			t.Fatalf("unexpected Action type %T", s.Action)
		}
	}
	return out
}

// calledActions returns the IAM action of every method of every adapter
// interface, failing on a method without a mapping.
func calledActions(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, api := range apis {
		for i := range api.iface.NumMethod() {
			key := api.name + "." + api.iface.Method(i).Name
			action, ok := actions[key]
			if !ok {
				t.Errorf("%s has no IAM action mapping; add it here and to controller-policy.json", key)
				continue
			}
			out = append(out, action)
		}
	}
	return out
}

func TestPolicy_CoversEveryAdapterCall(t *testing.T) {
	t.Parallel()
	allowed := allowedActions(t)
	for _, a := range calledActions(t) {
		if !slices.Contains(allowed, a) {
			t.Errorf("code calls %s, which controller-policy.json does not allow", a)
		}
	}
}

func TestPolicy_GrantsNothingUnused(t *testing.T) {
	t.Parallel()
	called := calledActions(t)
	for _, a := range allowedActions(t) {
		if !slices.Contains(called, a) {
			t.Errorf("controller-policy.json allows %s, which no adapter calls (least privilege)", a)
		}
	}
}

func TestPolicy_MappingHasNoStaleEntries(t *testing.T) {
	t.Parallel()
	methods := map[string]bool{}
	for _, api := range apis {
		for i := range api.iface.NumMethod() {
			methods[api.name+"."+api.iface.Method(i).Name] = true
		}
	}
	var stale []string
	for k := range actions {
		if !methods[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("mapping entries without an interface method: %v", stale)
	}
}
