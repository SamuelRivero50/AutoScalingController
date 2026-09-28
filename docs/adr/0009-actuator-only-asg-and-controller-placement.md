# ADR-0009: Auto Scaling Group as Actuator Only; Controller Placement and State

- **Status**: Accepted
- **Related**: REQ-LOGIC-2, REQ-LOGIC-3, REQ-Q-7, `docs/spec/architecture.md`, `docs/spec/infrastructure.md`, `docs/spec/lifecycle-and-failures.md`

## Context

The challenge forbids AWS-managed dynamic scaling policies from making the scaling *decision*, but allows AWS mechanisms to be used to provision/configure/register/remove instances once a decision is made externally. The controller also needs somewhere to run that does not put the application's availability at risk, and somewhere to keep its own transient bookkeeping.

## Decision

- Use an EC2 Auto Scaling Group purely as an actuator: min 1, max 5, **no scaling policies attached at all**; ELB health checks enabled with a grace period above the pending-timeout. The controller only ever calls `SetDesiredCapacity` (absolute values) and detects real launch failures via `DescribeScalingActivities`.
- Run the controller as a single Go binary on a small dedicated EC2 instance **outside** the application's ASG, so a controller crash cannot affect the running application and vice versa.
- Use AWS (ASG + target-group health) as the single source of truth for capacity/instance state; keep only the controller's own transient memory (windows, breaker counters, last-consumed datapoint markers) in a local JSON file, written atomically (temp file + rename); rebuild from AWS if missing or corrupt at startup.

## Alternatives considered

- **Attaching a target-tracking or step-scaling policy alongside the controller** (even if the controller could "override" it): rejected outright — this would let AWS make part of the scaling decision, directly violating REQ-LOGIC-2, regardless of how it were combined with the controller's own logic.
- **Running the controller inside the same ASG it manages**: rejected — a scale-in event could terminate the controller itself, and a controller crash would have no independent recovery path.
- **Storing capacity/instance state locally instead of reading it from AWS**: rejected — local state could drift from reality after any AWS-side event the controller didn't directly cause (e.g., an ASG health-check replacement); AWS is authoritative for capacity, the local file is only for the controller's own memory of past evaluation windows.

## Consequences

- Detecting real provisioning failures requires the additional `autoscaling:DescribeScalingActivities` IAM permission (`docs/spec/iam.md`).
- A corrupt/missing local state file only delays decisions (extra blind cycles while windows rebuild) — it never corrupts the controller's view of actual capacity.

## Sources

- AWS. *Dynamic scaling for Amazon EC2 Auto Scaling* — https://docs.aws.amazon.com/autoscaling/ec2/userguide/as-scale-based-on-demand.html
- AWS. *DescribeScalingActivities API* — https://docs.aws.amazon.com/autoscaling/ec2/APIReference/API_DescribeScalingActivities.html
