# Decision Policy

This is the normative specification of the `Decide()` pure function (see `docs/spec/architecture.md`, §3). It answers REQ-Q-1, REQ-Q-5, REQ-Q-6, REQ-Q-9, REQ-Q-12.

## 1. Core rule

```
overload(cycle)    = CPU >= 70
                     OR (CPU >= 55 AND (p95 > 500ms OR error_5xx > 1%))

comfortable(cycle) = CPU * N/(N-1) <= 55
                     AND p95 <= 350ms (if evaluable)
                     AND error_5xx <= 0.5% (if evaluable)

scale-out step     = min(2, 5-N, max(1, ceil(N * CPU / 55) - N))
                     (capped at +1 instead of +2 if triggered by latency/errors alone,
                      i.e. CPU was in [55,70) and only the confirming condition fired)

scale-in step       = always -1 (never more than one at a time)

0 healthy targets   = MAINTAIN_CAPACITY + alert (ASG self-heals; scaling would not help)
```

Where `N` is the current in-service instance count.

## 2. Rationale for each threshold

- **70% CPU scale-out trigger**: chosen as a saturation threshold with headroom before CPU-bound latency degradation typically becomes severe, consistent with the paper's own example of static-threshold reactive control (Al-Dhuraibi et al., 2018, p. 435, citing Amazon EC2's own scaling threshold conventions).
- **55% CPU scale-in comfort bound (15-point margin from 70%)**: the margin between the scale-out and scale-in thresholds is the anti-oscillation "dead-band" (hysteresis). An initial 10-point margin (70% → 60%) was judged too narrow after comparing against Kubernetes Horizontal Pod Autoscaler's default 10% *tolerance* band — which, once translated to an absolute CPU range at typical utilization levels, corresponds to a wider absolute gap than 10 points. The margin was revised to **15 points**, moving the scale-in bound to 55%.
- **`CPU * N/(N-1)` projection**: before removing an instance, the policy projects what the *remaining* N-1 instances' average CPU would become if the same total load persisted, and only allows scale-in if that projected value is still within the comfort bound. This is safer than a fixed threshold (e.g., "scale in below 30% regardless of N") because a fixed threshold either under-scales-in (leaves unnecessary capacity when N is large) or risks scaling into overload (when N is small and the fixed threshold doesn't account for redistributed load). The projection formula uses only the capacity actually needed, directly answering the challenge's central question (REQ-GOAL-1) and REQ-Q-12 ("safe to reduce").
- **Proportional scale-out step, capped at +2**: `ceil(N*CPU/55) - N` estimates how many instances would bring average CPU down to the comfort bound immediately, rather than always adding exactly one — this reduces reaction time under a sudden large spike (REQ-PRESENT-2) while the +2 cap and the 5-instance hard ceiling (REQ-CONSTRAINT-2) prevent overreacting to a single noisy reading.
- **Latency/error-only trigger capped at +1**: when CPU alone would not justify scaling but the SLO signals do (CPU in the 55-70% band with an SLO breach), the response is conservative (+1) because the causal link to CPU saturation is weaker.
- **Scale-in step always -1**: removing more than one instance at once was rejected as unsafe — it assumes the projection holds simultaneously for multiple removed instances, compounding the projection's own uncertainty of the demand's stability.

## 3. Evaluation windows (anti-oscillation, part 2)

The dead-band above prevents *rapid reversal*; it does not prevent flapping from noisy individual readings. Two further mechanisms apply (see `docs/spec/configuration.md` for exact parameters):

- **M-of-N stabilization window**: a decision to scale out requires `overload(cycle)` to be true in **2 of the last 3 fresh cycles**; scale-in requires `comfortable(cycle)` in **5 of 5** fresh cycles (realistic profile) or **3 of 3** (demo profile). This is modeled directly on CloudWatch alarm "M out of N" evaluation semantics (breaching datapoints need not be consecutive, but must fall within the last N fresh cycles).
- **State-based cooldown**: no new scale-out decision is issued while any instance is `PENDING`; no scale-in while anything is `PENDING` or `DRAINING` (scale-out is still allowed during a drain, since draining does not indicate overload elsewhere). The window resets whenever capacity actually changes. See `docs/spec/lifecycle-and-failures.md`, ADR-0004.

Together: dead-band (which threshold), M-of-N window (how many confirming cycles), and state-based cooldown (blocking overlapping actions) are complementary, not redundant — removing any one reintroduces a distinct flapping scenario (validated by simulator scenarios S3 and S5, `docs/spec/simulator.md`).

## 4. Reason codes

Every decision carries exactly one `reason_code` from the closed catalog defined in `docs/spec/decision-log.md`. This makes every decision traceable to the specific rule that produced it, directly satisfying REQ-CONSTRAINT-7 (explainability) and enabling the "example of an incorrect or late decision" analysis (REQ-PRESENT-6).

## 5. Sources

- Google SRE Book, *Service Level Objectives* — https://sre.google/sre-book/service-level-objectives/
- AWS. *Create a target tracking scaling policy for Amazon EC2 Auto Scaling* (background on threshold conventions) — https://docs.aws.amazon.com/autoscaling/ec2/userguide/as-scaling-target-tracking.html
- Kubernetes documentation. *Horizontal Pod Autoscaler — algorithm details* (tolerance/dead-band concept) — https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/#algorithm-details
- AWS. *Alarm evaluation — CloudWatch* (M out of N datapoints) — https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/AlarmThatSendsEmail.html
