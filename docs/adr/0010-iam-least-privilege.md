# ADR-0010: IAM Least Privilege

- **Status**: Accepted
- **Related**: REQ-CONSTRAINT-9, `docs/spec/iam.md`

## Context

The controller needs read access to CloudWatch/ASG/ALB and narrowly-scoped write access to change capacity, without being granted broad account permissions — while operating inside an AWS Academy Learner Lab that does not allow custom IAM roles.

## Decision

Grant read-only Describe/Get/List actions with `Resource: "*"` (CloudWatch and Describe-style ASG/ALB actions do not support resource-level restriction); scope the two write actions (`SetDesiredCapacity`, `TerminateInstanceInAutoScalingGroup`) to the **specific ASG ARN**, with an `autoscaling:ResourceTag` condition as an additional, independent layer. Document this policy in `infra/iam/controller-policy.json` as the policy that would be attached in a normal AWS account, while acknowledging the controller actually runs under the lab's `LabRole`.

## Alternatives considered

- **Tag-condition-only restriction (no ARN)**: this was the original decision, made on the assumption that full ARN-level `Resource` restriction support for the write actions was not confirmed. That assumption was checked against the AWS Service Authorization Reference (see Sources) and found to be **incorrect** — the write actions do support ARN-level restriction. The design was corrected to use the ARN restriction as the primary control, keeping the tag condition as defense-in-depth rather than the sole mechanism. (This correction was made after being asked why the claim had not been verified — it was verifiable via authoritative AWS documentation and should have been checked at design time rather than left as an assumption.)
- **Also granting `SetInstanceHealth`**: included in an earlier version of the policy, then removed — no part of the design calls it (the stuck-pending case uses `TerminateInstanceInAutoScalingGroup`), so granting it contradicted least privilege.
- **Requesting a custom IAM role in the Learner Lab**: not possible — the lab does not support it; documented as an explicit, named limitation rather than silently working around it.

## Consequences

- A test asserts the code's actual AWS API calls are a subset of the documented policy's actions, preventing the policy document from silently drifting out of sync with the implementation.

## Sources

- AWS. *Actions, resources, and condition keys for Amazon EC2 Auto Scaling* — https://docs.aws.amazon.com/service-authorization/latest/reference/list_autoscaling.html
- AWS. *IAM policies for actions that don't support resource-level permissions* — https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_policies_actions-resources-contextkeys.html
- Kubernetes `cluster-autoscaler`, AWS cloud provider IAM policy — https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/cloudprovider/aws/README.md
