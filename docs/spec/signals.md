# Signals (Metrics)

## 1. Signal set

| Signal | Source | Role | Statistic |
|---|---|---|---|
| `CPUUtilization` | EC2 (per instance, aggregated) | Saturation signal — primary scale-out/scale-in trigger | Average, per-instance, aggregated across in-service instances |
| `TargetResponseTime` | ALB | SLO signal (latency) | p95 |
| `HTTPCode_Target_5XX_Count` + `HTTPCode_ELB_5XX_Count` | ALB | SLO signal (errors), combined into a single error-rate metric | Sum / RequestCount |
| `RequestCount` | ALB | Traffic volume — used to determine evaluability of latency/error signals, and to detect the no-traffic case | Sum |
| `RequestCountPerTarget` | ALB | Context signal (per-instance load distribution) | Sum |
| `HealthyHostCount` | ALB (target group) | Context/safety signal — detects the "0 healthy targets" edge case | Instantaneous |
| Instance/target lifecycle state | ASG + ALB target health (not CloudWatch) | Capacity/state ground truth | N/A — API state, not a metric |

**Error rate** is computed as a single combined metric: `(HTTPCode_Target_5XX_Count + HTTPCode_ELB_5XX_Count) / RequestCount` over the same period, rather than treated as two separate signals — a single closed-form value simplifies the decision policy and both counters share the same failure semantics (a request that did not succeed).

## 2. Why these signals and not others

- **CPU is the primary trigger, not latency/errors alone.** ALB `TargetResponseTime` failures can result from causes unrelated to instance-count (e.g., a slow downstream dependency), and a 503/504 spike can also occur when the ALB fails open with zero healthy targets — a case scaling cannot fix. Latency/error signals only **confirm and lower** the CPU trigger threshold (see `docs/spec/decision-policy.md`); they never trigger a scale-out in isolation. A sustained high-latency/low-CPU combination is logged as `MAINTAIN_SLO_BREACH_LOW_CPU` — an explicitly documented blind spot (the bottleneck is elsewhere, e.g., a database), directly answering REQ-PRESENT-1.
- **RequestCount gates evaluability.** ALB does not publish `TargetResponseTime` or 5xx datapoints when there is no traffic in the period; an absent latency/error datapoint is therefore not a fault, it means zero requests. A minimum of 20 requests/period is required before latency or error rate is treated as evaluable — below that, one slow or failed request would produce a meaningless p95/rate. See `docs/spec/lifecycle-and-failures.md` for the full signal-quality rules.
- **HealthyHostCount is a safety valve.** If it drops to zero, the ASG is failing health checks faster than instances can be replaced; scaling out would not help (there is nothing to route traffic to), so the decision is `MAINTAIN_CAPACITY` with an alert, not `INCREASE_CAPACITY`.
- **Capacity/instance state comes from the ASG and ALB target-health APIs, not CloudWatch.** CloudWatch metrics can lag; the actual desired/pending/in-service/draining counts are read directly from `DescribeAutoScalingGroups` and `DescribeTargetHealth`, which are authoritative and low-latency.

## 3. Fetch cadence and CloudWatch limits

- ALB metrics are published to CloudWatch on a **fixed 60-second granularity** — this is not configurable and directly sets the realistic-profile evaluation interval (see `docs/spec/configuration.md`).
- EC2 **basic** monitoring reports CPU every 5 minutes by default. **Decision**: enable **Detailed Monitoring** (1-minute CPU) if the AWS Academy Learner Lab permits it (additional cost ~$0.03/instance/month, negligible). If detailed monitoring is unavailable in the lab, a datapoint-deduplication safeguard is used: the same 5-minute CPU reading is not allowed to satisfy more than one 60-second evaluation window (tracked via `last-consumed datapoint timestamp` in the state store), so a stale value cannot silently count as several independent confirmations.

## 4. Sources

- AWS. *CloudWatch metrics for your Application Load Balancer* — https://docs.aws.amazon.com/elasticloadbalancing/latest/application/load-balancer-cloudwatch-metrics.html
- AWS. *List the available CloudWatch metrics for your instances* — https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/viewing_metrics_with_cloudwatch.html
- AWS. *Enable or turn off detailed monitoring for your instances* — https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/using-cloudwatch-new.html
