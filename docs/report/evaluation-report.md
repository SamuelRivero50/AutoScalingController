# Evaluation and Critical Analysis Report (draft)

Answers REQ-DELIV-3 and REQ-DELIV-4 following the structure of `docs/spec/evaluation.md` §2. Every number below is computed by `cmd/analyze` from the JSONL decision logs; every quoted record comes from those logs.

**Status**: simulator evidence complete; the real-AWS integration run (backlog #25) has not been performed yet. Sections that depend on it are marked *pending real run*.

## 0. Reproducing the numbers

```bash
make sim        # runs S1-S10 (seed 1) and writes sim-logs/<ID>-seed1/
make analyze    # prints the per-run metrics and the summary table
go run ./cmd/analyze -json sim-logs > metrics.json   # machine-readable
```

All runs use the realistic profile (`config_hash` `1ba530669424…`), a 60 s cycle and seed 1. The simulator is deterministic, so the same seed reproduces the same logs and numbers bit for bit.

### How each metric is computed (`internal/evaluation`)

| Metric | Computation from the log |
|---|---|
| SLO compliance % | Cycles where both `latency_p95_breach` and `error_rate_breach` were evaluable (non-null value) and neither was met, over evaluable cycles |
| Instance-minutes | Σ (in-service + pending + draining) × time until the next cycle |
| Theoretical minimum `R` | `max(1, ceil(load / 0.55))`, capped at 5, with load in instance units inferred as fleet CPU × in-service / 100 |
| Over/under-provisioning | Share of CPU-valid cycles with in-service above/below `R`, and the mean gap in instances |
| Capacity changes | Executed `SET_DESIRED_CAPACITY` actions, split into scale-out, scale-in (`REDUCE_CAPACITY`) and breaker capacity resets; plus stuck-pending terminations |
| Oscillation | Direction reversals between consecutive executed changes, and `INCREASE_CAPACITY` decisions within 10 min of a `REDUCE_CAPACITY` decision (the S5 criterion) |
| Time-to-relief | From the first cycle with CPU ≥ the scale-out trigger (70%) to the first cycle with valid CPU back below it; split into decision latency (to the first `INCREASE_CAPACITY`) and reaction time (the rest) |
| Late/incorrect decisions | Cycles with `MAINTAIN_SLO_BREACH_LOW_CPU`, `MAINTAIN_AT_MAX` with `overload` met, and `SKIPPED`/`ERROR` actions |
| Measured warmup | For each launched instance, first cycle seen `IN_SERVICE` minus `launched_at` (an upper bound, at cycle granularity) |

**Limits of the log-only method.** The log does not carry the simulator's true load, so `R` is inferred from CPU. That inference overestimates slightly, because it includes the 2% idle floor per instance. It is only a lower bound when CPU is saturated (the `saturated` count below). The same inference is the only one available for the real run, so both experiments are measured the same way.

## 1. Results

| Run | Cycles | SLO % | Instance-min | Minimum | Excess | Scale-out | Scale-in | Reset | Reversals | Increase <10 min after reduce | Relief (median) |
|---|---|---|---|---|---|---|---|---|---|---|---|
| S1 stable | 30 | 100.0 | 60 | 60 | +0.0% | 0 | 0 | 0 | 0 | 0 | — |
| S2 sustained increase | 45 | 82.2 | 145 | 152 | −4.6% | 2 | 0 | 0 | 0 | 0 | 8 min |
| S3 transient vs genuine | 30 | 100.0 | 67 | 63 | +6.3% | 1 | 1 | 0 | 1 | 0 | 2 min |
| S4 sustained decrease | 60 | 100.0 | 171 | 141 | +21.3% | 0 | 3 | 0 | 0 | 0 | — |
| S5 edge noise | 90 | 100.0 | 192 | 205 | −6.3% | 0 | 1 | 0 | 0 | 0 | — |
| S6 circuit breaker | 40 | 57.5 | 65 | 103 | −36.9% | 2 | 0 | 1 | 2 | 0 | 17 min |
| S7 stuck pending | 30 | 56.7 | 56 | 77 | −27.3% | 2 | 0 | 0 | 0 | 0 | 13 min |
| S8 blindness | 20 | 100.0 (12 evaluable) | 40 | 24 (12 cycles) | +0.0% | 0 | 0 | 0 | 0 | 0 | — |
| S9 no traffic | 45 | n/a (no traffic) | 60 | 45 | +33.3% | 0 | 2 | 0 | 0 | 0 | — |
| S10 composite 90 min | 90 | 84.4 | 277 | 233 | +18.9% | 2 | 4 | 0 | 1 | 0 | 2 min |

"Excess" compares instance-minutes with the minimum over the CPU-valid cycles only. That is why S8 compares 24 with 24 (12 cycles) and not the full 40.

## 2. Metric adequacy (REQ-PRESENT-1)

CPU, confirmed by p95 latency and the 5xx rate, drove every correct scale-out in S2, S3, S6, S7 and S10 (`INCREASE_CPU_HIGH` in every case). The documented blind spot (`docs/spec/signals.md`) is reproduced in S10. Between cycles 53 and 62 a slow downstream dependency pushes p95 latency to 880 ms while fleet CPU stays at 14-20%. The controller logs `MAINTAIN_SLO_BREACH_LOW_CPU` for all 10 cycles and does not add capacity, which is correct because more instances would not fix a downstream bottleneck. These 10 cycles account for most of S10's SLO misses (14 breaching cycles in total).

## 3. Reaction time (REQ-PRESENT-2)

Relieved incidents that required a scale-out:

| Run | Incident (cycles) | Time-to-relief | Decision latency | Reaction time |
|---|---|---|---|---|
| S3 | 18-20 | 2 min | 1 min | 1 min |
| S10 | 38-40 | 2 min | 1 min | 1 min |
| S10 | 18-22 | 4 min | 1 min | 3 min |
| S2 | 8-16 | 8 min | 1 min | 7 min |
| S7 | 1-14 | 13 min | 1 min | 12 min |
| S6 | 1-18 | 17 min | 1 min | 16 min |

Decision latency is always one cycle, because the 2-of-3 window needs a second breaching cycle. Reaction time is dominated by the 180 s warmup; the measured warmup is 3 min in every run, equal to the assumed value, as expected from the simulator. S6 and S7 are slow by construction (failed launches, stuck instance).

**Finding: CPU saturation limits the step size.** S2 cycle 9 decides `INCREASE_CAPACITY` with CPU at 100% on one instance and requests only `desired_capacity: 2`:

```
"ts":"2026-01-01T00:08:00Z"  "CPUUtilization","value":100  "breaching":[8,9]
"desired":1,"in_service":1   "decision":"INCREASE_CAPACITY","reason_code":"INCREASE_CPU_HIGH"
"type":"SET_DESIRED_CAPACITY","params":{"desired_capacity":2},"status":"OK"
```

The step `ceil(N × CPU / 55) − N` gives `ceil(100/55) − 1 = 1`. A saturated CPU hides how much demand exceeds capacity, so the proportional step cannot use its +2 cap. The controller needs a second scale-out and the incident takes 8 min instead of about 4. This is the spec'd formula working as documented (`docs/spec/decision-policy.md` §2), not a bug. It is a limitation worth stating: the step-sizing projection is blind above 100% CPU. It also shows in the log as 3 `saturated` cycles in S2 where `R` is a lower bound.

## 4. Transient vs. sustained (REQ-PRESENT-3)

S3 injects a 1-cycle spike and later a 2-cycle spike:

```
cycle 8   CPU 81.5  "breaching":[8]      MAINTAIN_CAPACITY / MAINTAIN_WINDOW_PENDING
cycle 9   CPU 46.8  "breaching":[8]      MAINTAIN_CAPACITY / MAINTAIN_STABLE
cycle 18  CPU 81.6  "breaching":[18]     MAINTAIN_CAPACITY / MAINTAIN_WINDOW_PENDING
cycle 19  CPU 82.1  "breaching":[18,19]  INCREASE_CAPACITY / INCREASE_CPU_HIGH  -> desired_capacity 3
```

The single breaching cycle is recorded in the window but does not act. It is counted as a transient incident resolved in 1 min without a scale-out. The genuine spike acts on its second breaching cycle. S5 shows the same pattern at the edge of the threshold: 2 transient incidents and no scale-out.

## 5. Oscillation prevention (REQ-PRESENT-4, REQ-PRESENT-8)

No run has an `INCREASE_CAPACITY` decision within 10 minutes of a `REDUCE_CAPACITY` decision, which is the S5 criterion, met in all ten runs. S5 (90 min of noise around the thresholds) makes a single capacity change: one scale-in. The direction reversals in S3 and S10 are a scale-out followed much later by a scale-in after the load genuinely dropped, not flapping. The reversals in S6 come from the breaker capacity reset (§6).

## 6. Failure handling (REQ-PRESENT-5)

- **S6, circuit breaker.** 8 scale-out attempts are `SKIPPED` with `skip_reason: BREAKER_OPEN` (first at cycle 7, CPU 99.5%). One breaker capacity reset brings desired capacity back to what actually runs. The S6 acceptance check verifies that exactly one probe follows the 10-minute cool-off. The SLO suffers (57.5%) because the group cannot grow, which is the intended trade-off: stop hammering a failing launch path.
- **S7, stuck pending.** 1 `TERMINATE_INSTANCE` on the instance that never became healthy. The replacement then relieves the incident.
- **S8, blindness.** 8 `MAINTAIN_BLIND` cycles with no capacity change. SLO compliance is computed over the 12 evaluable cycles only, so the blind period is visible as `evaluable_cycles < cycles` instead of being silently counted as compliant.

## 7. Example of an incorrect or late decision (REQ-PRESENT-6)

S10 cycle 53 (fields extracted verbatim from the cycle record):

```
"ts":"2026-01-01T00:52:00Z"
"name":"CPUUtilization","value":14.113754989495307
"name":"TargetResponseTime","value":880
"desired":4,"in_service":4,"pending":0
"decision":"MAINTAIN_CAPACITY","reason_code":"MAINTAIN_SLO_BREACH_LOW_CPU"
"name":"cpu_elevated","value":14.113754989495307,"threshold":55,"met":false
"name":"latency_p95_breach","value":880,"threshold":500,"met":true
```

From the user's point of view this is a failure: the SLO is breached for 10 consecutive minutes (cycles 53-62, until latency returns to 80 ms at cycle 63) and the controller does nothing. The decision is deliberate. Latency is only a confirming signal (ADR-0001), and with CPU at 14% no amount of added capacity would help. What makes it explainable is that the log names the case with a dedicated reason code instead of hiding it inside `MAINTAIN_STABLE`.

The late decision in S2 (§3, saturated step) is the second example.

## 8. Cost of maintaining the SLO (REQ-PRESENT-7)

- **Scale-in is deliberately slow.** S4 uses 21.3% more instance-minutes than the minimum, and S9 33.3% (1.5 extra instances on average while over-provisioned). The 5-of-5 scale-in window and the −1 step pay for stability with idle capacity.
- **Under-provisioning against `R` does not mean an SLO breach.** S5 is below `R` in 22.2% of cycles yet keeps 100% SLO compliance. `R` targets the 55% comfort bound, while the controller only acts at 70%. The 15-point dead band is where the controller intentionally sits.
- **The composite run (S10)** uses 277 instance-minutes for 233 minimum (+18.9%) at 84.4% SLO compliance, and the blind spot of §2 is most of the gap.
- **S6 and S7 run far below `R` (−36.9%, −27.3%).** Here the lack of capacity comes from injected failures, not from a policy choice.

## 9. Comparing two controllers (REQ-PRESENT-8)

Methodology (`docs/spec/configuration.md` §5):

1. Run both configurations against the same scenario and seed.
2. Check that the two runs differ only in the parameter under test by comparing their `config_hash`.
3. Run `cmd/analyze` on both logs and compare instance-minutes and change count at equal SLO compliance.

`cmd/analyze` already groups records by `run_id` and prints each run's `config_hash`, so both runs can be analyzed in one call. The simulator currently runs only the realistic profile (`simulator.Run` fixes the configuration), so no comparison has been executed yet. Running one needs a configuration override in the simulator runner.

## 10. Critical analysis (`docs/spec/evaluation.md` §3)

- **Not validated within Learner Lab constraints**: true multi-hour sustained load, behaviour beyond 5 instances, and real CloudWatch lag and jitter. The simulator's 1-minute datapoints arrive exactly on time.
- **Synthetic real-run load**: in the real run, an instance added by scale-out starts unstressed, so fleet CPU falls because of how stress is applied, not because traffic redistributed (`docs/spec/app.md` §3). The simulator models redistribution; the real run only proves integration.
- **Confirmed blind spot**: SLO breach with low CPU (§2, §7).
- **Reason codes more or less frequent than expected**:
  - `MAINTAIN_WINDOW_PENDING` is the most common non-stable code (24 of 90 cycles in S10), because it covers both "waiting for a second breaching cycle" and "waiting for 5 comfortable cycles".
  - `MAINTAIN_AT_MAX` never occurs, since no scenario pushes demand beyond 5 instances.
  - `INCREASE_LATENCY_SLO`, `INCREASE_CAPACITY_ERRORS` and `INCREASE_APP_ERRORS_WITH_LOAD` never occur in S1-S10: every overload in the scenarios is CPU-driven. The unit tests of the decision core cover these codes, but the scenarios do not exercise them.
- **Saturation**: the proportional step is limited at 100% CPU (§3).

## 11. Real-AWS integration run (*pending real run*)

To be filled after backlog #25, using `scripts/collect-evidence.sh` and then `go run ./cmd/analyze evidence/`:

- the same metric table for the real run (mode `real`);
- measured warmup against the assumed 180 s (open item in `docs/verification.md`);
- t3.micro unlimited-mode surplus charges, if any.
