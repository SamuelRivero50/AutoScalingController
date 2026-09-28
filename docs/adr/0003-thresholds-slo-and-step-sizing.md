# ADR-0003: Thresholds, SLO, and Step Sizing

- **Status**: Accepted
- **Related**: REQ-Q-1, REQ-Q-5, REQ-Q-6, REQ-Q-12, `docs/spec/decision-policy.md`, `docs/spec/configuration.md`

## Context

The controller needs concrete numeric thresholds for "overload" and "comfortable," a proportional or fixed step size, and a way to decide when reducing capacity is safe.

## Decision

- Scale-out trigger: CPU ≥ 70%, or CPU ≥ 55% combined with an SLO breach (p95 > 500ms or error rate > 1%).
- Scale-in comfort bound: projected `CPU * N/(N-1) ≤ 55%`, with latency ≤ 350ms and error ≤ 0.5% when evaluable.
- Scale-out step: `min(2, 5-N, max(1, ceil(N*CPU/55) - N))`, capped at +1 if triggered by latency/errors alone.
- Scale-in step: always -1.

## Alternatives considered

- **Fixed 30% CPU scale-in threshold regardless of instance count**: rejected — it is safe but does not use "only the capacity actually needed" (the challenge's central question), because it ignores how load redistributes across the remaining instances after removal.
- **10-point dead-band margin (70% → 60%)**: initially proposed, then revised to 15 points (70% → 55%) after comparing against Kubernetes HPA's default 10% tolerance, which corresponds to a wider absolute band at typical utilization levels than a flat 10-point CPU gap.
- **Always scale by exactly 1 instance regardless of severity**: rejected as too slow under a sudden large spike; replaced by the capped proportional step.

## Consequences

- The projected `N/(N-1)` scale-in check is the core mechanism answering "how is safe-to-reduce determined" (REQ-Q-12).
- The proportional step (capped at +2) reduces time-to-relief for large spikes while the hard cap and 5-instance ceiling bound the response to any single noisy cycle.

## Sources

- Google SRE Book, *Service Level Objectives* — https://sre.google/sre-book/service-level-objectives/
- Kubernetes documentation, *Horizontal Pod Autoscaler — algorithm details* — https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/#algorithm-details
- Al-Dhuraibi, Y. et al. (2018). *Elasticity in Cloud Computing.* IEEE TSC 11(2), p. 435 (static-threshold reactive example).
