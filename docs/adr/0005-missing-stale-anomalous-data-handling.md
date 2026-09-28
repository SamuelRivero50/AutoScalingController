# ADR-0005: Missing, Stale, and Anomalous Data Handling

- **Status**: Accepted
- **Related**: REQ-Q-10, REQ-CONSTRAINT-8, `docs/spec/lifecycle-and-failures.md`

## Context

The controller must not jeopardize availability when metrics are missing, delayed, or clearly wrong, and must not silently misinterpret the *absence* of a metric (which for ALB metrics can simply mean zero traffic).

## Decision

Classify every signal per cycle into one of six quality states (`VALID`, `NOT_EVALUABLE`, `NO_NEW_DATA`, `MISSING`, `STALE`, `ANOMALOUS`). Absence of `RequestCount` means zero traffic (not a fault); absence of 5xx counters while `RequestCount` is present means zero errors. `REDUCE_CAPACITY` requires positive, valid CPU evidence across the entire scale-in window — never inferred from absence. If CPU is unusable and latency/error are not evaluable, decide `MAINTAIN_CAPACITY` (`MAINTAIN_BLIND`); raise an alert after a configured number of consecutive blind cycles.

## Alternatives considered

- **Treating any missing metric as an automatic scale-out (fail-safe-out)**: rejected — could cause runaway scale-out during a monitoring outage, itself a cost/availability risk, and would not be explainable as "adequate."
- **Filtering anomalous spikes by magnitude**: rejected — could silently hide genuine extreme overload; anomalies are instead only ever filtered by the M-of-N window mechanism, never discarded solely for being large.

## Consequences

- Capacity/instance state truth comes from ASG/ALB APIs, not CloudWatch, so a CloudWatch outage degrades only the controller's view of *load*, never its view of *current capacity*.
- Blindness is visible and alertable, not silently absorbed.

## Sources

- AWS. *CloudWatch metrics for your Application Load Balancer* (zero-traffic non-publication behavior) — https://docs.aws.amazon.com/elasticloadbalancing/latest/application/load-balancer-cloudwatch-metrics.html
- AWS. *Alarm evaluation — CloudWatch* (missing-data treatment options) — https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/AlarmThatSendsEmail.html
