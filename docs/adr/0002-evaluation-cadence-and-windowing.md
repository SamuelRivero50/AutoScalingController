# ADR-0002: Evaluation Cadence, Aggregation, and Windowing

- **Status**: Accepted
- **Related**: REQ-Q-3, REQ-Q-4, REQ-Q-9, `docs/spec/configuration.md`, `docs/spec/decision-policy.md`

## Context

The controller must poll metrics on some cadence and decide how many confirming observations are needed before acting, to balance reaction time against noise sensitivity.

## Decision

- Realistic-profile evaluation interval: 60 seconds, matching the ALB's fixed 60-second CloudWatch reporting granularity (not configurable on AWS's side).
- Demo profile: 10 seconds, to compress the live demonstration.
- Scale-out requires 2-of-3 recent fresh cycles overloaded; scale-in requires 5-of-5 (realistic) or 3-of-3 (demo) fresh cycles comfortable — modeled on CloudWatch alarm "M out of N" evaluation semantics.
- A cycle only counts if no capacity change is currently pending; any capacity change resets both windows.
- After a scale-out completes, the first CPU datapoint is discarded when its aggregation period started before the new instances arrived (stale-period guard). This prevents the smaller fleet's high CPU from re-triggering a second scale-out.

## Alternatives considered

- **5-minute evaluation interval** (matching EC2 basic monitoring): rejected as too slow for a responsive controller; addressed instead by enabling Detailed Monitoring where possible and a datapoint-dedup safeguard otherwise (`docs/spec/signals.md` §3).
- **Consecutive-only breaching requirement (strict N-of-N for scale-out)**: rejected as too slow to react to a genuine sustained spike compared to the "M of N, not necessarily consecutive" CloudWatch-style rule.

## Consequences

- Scale-out reacts faster (2 of 3) than scale-in is confirmed (5 of 5 / 3 of 3) — an intentional asymmetry favoring availability over cost when in doubt.
- The stale-period guard adds one discarded cycle after every scale-out, slightly increasing time-to-relief for the second increment in a multi-step scale-out. The tradeoff is correct: without it a re-trigger fires on stale high-CPU data and overshoots the target capacity.

## Sources

- AWS. *Alarm evaluation — CloudWatch* (M out of N datapoints) — https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/AlarmThatSendsEmail.html
- AWS. *CloudWatch metrics for your Application Load Balancer* — https://docs.aws.amazon.com/elasticloadbalancing/latest/application/load-balancer-cloudwatch-metrics.html
