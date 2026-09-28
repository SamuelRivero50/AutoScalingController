# ADR-0001: Metric Selection

- **Status**: Accepted
- **Related**: REQ-Q-2, REQ-CONSTRAINT-3, `docs/spec/signals.md`

## Context

The challenge requires the controller to draw substantial information from CloudWatch and decide based on demand/state metrics, but leaves the specific metric set to the student.

## Decision

Use `CPUUtilization` (EC2) as the primary saturation/trigger signal; ALB `TargetResponseTime` p95 and combined 5xx error rate as SLO confirmation/override signals; `RequestCount`, `RequestCountPerTarget`, and `HealthyHostCount` as context/evaluability/safety signals. Capacity and instance-lifecycle state are read from the ASG and ALB target-health APIs directly, not from CloudWatch.

## Alternatives considered

- **Latency/error rate as the primary trigger**: rejected — a latency or error spike can be caused by factors unrelated to instance count (e.g., a downstream dependency, or an ALB fail-open response when all targets are unhealthy), so scaling would not reliably fix it. Used only as a confirming/lowering signal instead.
- **CPU alone with no SLO signals**: rejected — would miss cases where a smaller CPU increase already causes an SLO breach, and would provide no direct evidence that scaling actually protects the user-facing objective.

## Consequences

- A documented blind spot exists: sustained high latency with low CPU is logged as `MAINTAIN_SLO_BREACH_LOW_CPU`, not treated as a trigger (see `docs/spec/decision-policy.md`).
- Evaluability of latency/error signals is gated by a minimum request count per period (`docs/spec/signals.md` §2).

## Sources

- AWS. *CloudWatch metrics for your Application Load Balancer* — https://docs.aws.amazon.com/elasticloadbalancing/latest/application/load-balancer-cloudwatch-metrics.html
- AWS. *List the available CloudWatch metrics for your instances* — https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/viewing_metrics_with_cloudwatch.html
