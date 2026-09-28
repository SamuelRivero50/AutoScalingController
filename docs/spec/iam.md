# IAM Least Privilege

Answers REQ-CONSTRAINT-9 and REQ-DELIV-2's credential-safety requirement.

## 1. Policy

The full policy JSON lives at `infra/iam/controller-policy.json` and is the single source of truth — this document explains it, it does not restate it verbatim.

| Action | Resource restriction | Why |
|---|---|---|
| `cloudwatch:GetMetricData` | `*` | CloudWatch does not support resource-level restriction on this action |
| `cloudwatch:ListMetrics` | `*` | Used once at startup to validate that expected metrics exist before the control loop begins; same restriction limitation as above |
| `autoscaling:DescribeAutoScalingGroups` | `*` | Read-only Describe actions do not support ARN-level restriction |
| `autoscaling:DescribeScalingActivities` | `*` | Needed to detect real launch failures (see `docs/spec/lifecycle-and-failures.md`); same restriction limitation |
| `elasticloadbalancing:DescribeTargetHealth` | `*` | Same restriction limitation |
| `autoscaling:SetDesiredCapacity` | Restricted via `autoscaling:ResourceTag/Project = auto-scaling-controller` condition | Write action — must be scoped to only this project's Auto Scaling Group |
| `autoscaling:SetInstanceHealth` | Same tag condition | Write action |
| `autoscaling:TerminateInstanceInAutoScalingGroup` | Same tag condition | Write action, used for the stuck-pending-instance case |

The tag-based condition (rather than a specific ASG ARN) is modeled on the IAM pattern used by the Kubernetes `cluster-autoscaler` project for AWS, which uses resource tags rather than ARNs because full ARN-level `Resource` restriction support for these three actions was not confirmed to be complete across all AWS partitions/versions at the time of writing.

## 2. AWS Academy Learner Lab limitation

The Learner Lab does not allow creating custom IAM roles or attaching policies to the built-in `LabRole`. Consequently:
- The controller **runs under `LabRole`** in the lab environment, which is broader than the policy above.
- `infra/iam/controller-policy.json` documents the policy that **would** be attached to a dedicated role in a normal AWS account, and is included specifically so the design can be assessed on its least-privilege merits independent of the lab's own constraint (satisfying REQ-CONSTRAINT-9 as a design property, with the lab limitation called out explicitly rather than hidden).
- A test (`docs/spec/architecture.md` source layout, `/internal/adapters/...` tests) asserts that the controller's actual AWS API calls, enumerated in code, are a subset of the actions listed in `controller-policy.json` — so the policy document cannot silently drift from what the code actually does.

## 3. Credential handling

- No credentials, session tokens, or key files are committed. `.gitignore` excludes `.env`, `.aws/`, `*.pem`, and all Terraform state/plan files.
- A secret-scanning pre-commit hook (e.g., `gitleaks`) is part of the repository tooling and blocks accidental commits of key material.
- Local development (simulation mode, or pointing the real adapters at the lab from a laptop) uses the Learner Lab's temporary session-token credentials via environment variables, obtained through the lab's own credential-vending UI.
- The real deployment (controller running on its dedicated EC2 instance) uses an **EC2 instance profile** — the instance itself assumes `LabRole` via the instance metadata service; no static keys are ever placed on disk.

## 4. Sources

- AWS. *IAM policies for actions that don't support resource-level permissions* — https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_policies_actions-resources-contextkeys.html
- Kubernetes `cluster-autoscaler`, *AWS cloud provider IAM policy* — https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/cloudprovider/aws/README.md
- AWS Academy documentation on Learner Lab IAM restrictions (`LabRole`) — https://awsacademy.instructure.com (course-provided reference material)
