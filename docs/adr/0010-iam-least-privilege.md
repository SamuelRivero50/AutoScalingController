# ADR-0010: IAM Least Privilege

- **Status**: Accepted
- **Related**: REQ-CONSTRAINT-9, `docs/spec/iam.md`

## Context

The controller needs read access to CloudWatch/ASG/ALB and narrowly-scoped write access to change capacity, without being granted broad account permissions — while operating inside an AWS Academy Learner Lab that does not allow custom IAM roles.

## Decision

Grant read-only Describe/Get/List actions with `Resource: "*"` (CloudWatch and Describe-style ASG/ALB actions do not support resource-level restriction); scope the three write actions (`SetDesiredCapacity`, `SetInstanceHealth`, `TerminateInstanceInAutoScalingGroup`) via an `autoscaling:ResourceTag` condition rather than a specific ASG ARN. Document this policy in `infra/iam/controller-policy.json` as the policy that would be attached in a normal AWS account, while acknowledging the controller actually runs under the lab's `LabRole`.

## Alternatives considered

- **ARN-based resource restriction on the write actions**: considered first, but full support across all three actions was not confirmed reliably enough to depend on; the tag-based condition (used by the Kubernetes `cluster-autoscaler` project for the same problem) is a well-precedented alternative.
- **Requesting a custom IAM role in the Learner Lab**: not possible — the lab does not support it; documented as an explicit, named limitation rather than silently working around it.

## Consequences

- A test asserts the code's actual AWS API calls are a subset of the documented policy's actions, preventing the policy document from silently drifting out of sync with the implementation.

## Sources

- AWS. *IAM policies for actions that don't support resource-level permissions* — https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_policies_actions-resources-contextkeys.html
- Kubernetes `cluster-autoscaler`, AWS cloud provider IAM policy — https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/cloudprovider/aws/README.md
