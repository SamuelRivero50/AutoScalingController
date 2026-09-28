# Closed-Loop Simulator and Experiment Design

Answers REQ-DELIV-3, REQ-OBJ-3/4/5, and REQ-PRESENT-1..8 (the simulator is the primary evidence source; a short real-AWS run provides integration evidence only).

## 1. Two experiments

| | Primary: closed-loop simulator | Secondary: real AWS run |
|---|---|---|
| Purpose | Sizing/behavior validation, automated regression tests | Integration proof only — confirms the same code path works against real AWS APIs |
| Determinism | Seeded RNG, `FakeClock` — fully reproducible | Not reproducible bit-for-bit (real network/AWS timing) |
| Duration | Seconds (simulated hours compressed) | 45-60 minutes wall-clock |
| Cost | Free | Consumes AWS Academy credits — guardrails required (§3) |
| Uses same `Decide()` core and `DecisionLogger`? | Yes | Yes |

## 2. Simulator scenarios (S1-S10)

Each scenario is also an automated test with an explicit acceptance criterion.

| ID | Scenario | Acceptance criterion |
|---|---|---|
| S1 | Stable load, no threshold crossed | Decision is `MAINTAIN_CAPACITY` / `MAINTAIN_STABLE` for the entire run; capacity never changes |
| S2 | Sustained increase in demand | Controller reaches the instance count implied by `ceil(load/0.55)` (capped at 5) within the expected reaction time; decisions carry `INCREASE_CPU_HIGH` |
| S3 | Transient 1-cycle spike vs. genuine 2-cycle spike | A single breaching cycle does **not** trigger scale-out (confirms the 2-of-3 window); two consecutive breaching cycles do |
| S4 | Sustained decrease in demand | Controller scales in one instance at a time until the projection bound blocks further reduction or min=1 is reached |
| S5 | Edge noise oscillating around the threshold | No `INCREASE_CAPACITY` decision occurs within 10 minutes (simulated) after a `REDUCE_CAPACITY` decision — confirms the dead-band + windowing jointly prevent flapping |
| S6 | Launch failures / circuit breaker | After 3 consecutive simulated launch failures, breaker opens; no further scale-out attempted until the ~10-minute cool-off elapses; exactly one probe attempt follows |
| S7 | Stuck-pending instance | An instance that never reaches healthy within the pending-timeout is terminated and capacity decremented; the next cycle re-evaluates cleanly |
| S8 | Metrics outage (blindness) | All signals `MISSING` for several cycles → `MAINTAIN_BLIND`; alert event emitted after the configured consecutive-blind threshold; no capacity change during blindness |
| S9 | No-traffic scale-to-1 | With zero traffic (`RequestCount` absent), CPU eventually drops and the projection allows scale-in down to the minimum of 1, never below it |
| S10 | Composite 90-minute run | Combines multiple prior patterns in one run; used to generate the report's time-series graphs and the full evaluation-metric set (§4) |

## 3. Real-AWS run guardrails

- Hard duration cap enforced by the operator (target 45-60 minutes total, including infra apply/destroy).
- A cost estimate is computed and displayed before running `terraform apply`.
- Evidence is collected with `scripts/collect-evidence.sh` (S3 → local, fails if empty) and then `terraform destroy` runs immediately — neither step is optional (see `docs/spec/infrastructure.md` §3-§4).
- Load is generated only by `cmd/stress` from the controller host against the instances' private IPs, never by the controller itself (`docs/spec/app.md` §3).
- Explicit user confirmation is required before `terraform apply` and before any command that calls `/admin/stress` — no step in this run is automated end-to-end without a human present, which is acceptable because REQ-CONSTRAINT-5 (no human intervention) applies specifically to the *controller's decision loop* during the timed experiment, not to standing up/tearing down infrastructure.
- Stress level capped at 80-90% CPU (never 100%) per `docs/spec/lifecycle-and-failures.md` §5.

## 4. Live demonstration

The live demo (REQ-DELIV-5) uses the simulator with the **demo profile** plus a CLI "load dial" the presenter (or professor) can operate interactively to manually trigger overload/comfortable conditions and observe the controller's logged decisions in near-real-time, without incurring any AWS cost or waiting through realistic timeouts.

## 5. Evaluation metrics computed from the JSONL log

Using the Herbst et al. (2013) vocabulary and the Al-Dhuraibi et al. (2018) provisioning-state vocabulary:

- **SLO compliance %**: fraction of cycles where latency and error signals (when evaluable) were within SLO.
- **Instance-minutes used vs. theoretical minimum**: `R = max(1, ceil(load/0.55))`, capped at 5; compares actual instance-minutes to the minimum that would satisfy the comfort bound at every point in time — a direct over-/under-provisioning measure (REQ-PRESENT-7).
- **Over-/under-provisioning % and magnitude**: fraction of time actual capacity exceeded/fell short of `R`, and by how much.
- **Number of capacity changes / oscillation count**: total scale-out + scale-in actions over the run — directly answers REQ-PRESENT-4 and REQ-PRESENT-8.
- **Time-to-relief**: elapsed time from the first overload cycle to the first cycle where CPU returns below the scale-out trigger, decomposed into decision latency + reaction time — answers REQ-PRESENT-2.
- **Count and examples of late/incorrect decisions**: cycles where the eventual outcome (e.g., persistent SLO breach despite capacity being at max, or `MAINTAIN_SLO_BREACH_LOW_CPU`) is flagged by `reason_code`, giving a concrete answer to REQ-PRESENT-6.
- **Measured warmup**: the empirically measured launch → healthy duration, reported alongside the assumed value used to set timeouts.

## 6. Sources

- Herbst, N. R., Kounev, S., & Reussner, R. (2013). *Elasticity in Cloud Computing: What It Is, and What It Is Not.* ICAC 2013.
- Al-Dhuraibi, Y. et al. (2018). *Elasticity in Cloud Computing: State of the Art and Research Challenges.* IEEE TSC 11(2).
