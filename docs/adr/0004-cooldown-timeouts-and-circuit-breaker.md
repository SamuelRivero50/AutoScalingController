# ADR-0004: Cooldown, Timeouts, Warmup, and Circuit Breaker

- **Status**: Accepted
- **Related**: REQ-Q-7, REQ-Q-8, REQ-Q-9, REQ-Q-11, REQ-CONSTRAINT-8, `docs/spec/lifecycle-and-failures.md`, `docs/spec/configuration.md`

## Context

Cloud API actions are asynchronous (an instance takes time to launch and become healthy). The controller must avoid issuing new decisions while a previous one is still in flight, must recover from stuck or failing provisioning, and must never lose a decision to an overlapping evaluation cycle.

## Decision

- Cooldown is **state-based**, not a fixed timer: no new scale-out while any instance is `PENDING`; no scale-in while anything is `PENDING`/`DRAINING`; scale-out is still allowed during a drain.
- Windows reset on any capacity change.
- Pending timeout = 2× warmup estimate; a stuck-`PENDING` instance past this timeout is explicitly terminated and capacity decremented.
- A per-cycle time budget (40s realistic / 10s demo) prevents a slow cycle (multiple retried AWS calls) from overlapping the next scheduled cycle; if exhausted, unread signals become `MISSING` but the cycle is still logged.
- All capacity-changing calls set an **absolute** desired capacity (never a delta) with `HonorCooldown=false`, making retries idempotent.
- A circuit breaker (Closed/Open/Half-Open) opens after 3 consecutive provisioning failures, holds for ~10 minutes, then allows exactly one probe attempt.

## Alternatives considered

- **Fixed-timer cooldown** (e.g., "wait 5 minutes after any scaling action"): rejected — does not adapt to actual instance readiness; either too short (risking overlapping actions) or too long (needlessly delaying a legitimate follow-up decision).
- **Relying on ASG's own default cooldown**: rejected — the decision of *when* to act must remain entirely with the controller (REQ-LOGIC-2); ASG's cooldown is explicitly bypassed (`HonorCooldown=false`).
- **Unbounded retry on provisioning failure**: rejected — wastes time/money without addressing root causes outside the controller's control; replaced with the bounded circuit breaker plus an alert.

## Consequences

- Warmup is measured empirically from real ASG scaling-activity timestamps and fed back into the realistic-profile timeout, rather than assumed permanently.
- The circuit breaker distinguishes transient failures (worth a bounded retry) from persistent failures (worth an alert, not more automatic retrying).

## Sources

- AWS. *Auto Scaling group cooldowns* — https://docs.aws.amazon.com/autoscaling/ec2/userguide/ec2-auto-scaling-cooldowns.html
- Nygard, M. (2018). *Release It!* — Circuit Breaker pattern.
- AWS. *Health checks for Auto Scaling instances* — https://docs.aws.amazon.com/autoscaling/ec2/userguide/ec2-auto-scaling-health-checks.html
