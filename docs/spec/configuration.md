# Configuration Parameters

Two named profiles exist: **realistic** (values chosen to be defensible against real AWS timing behavior) and **demo** (compressed timings so the live demonstration, REQ-DELIV-5, fits a short session while exercising the same logic paths). The simulator (`docs/spec/simulator.md`) can run either profile with a `FakeClock`; the real deployment always uses the realistic profile.

## 1. Cadence and windowing

| Parameter | Realistic | Demo |
|---|---|---|
| Evaluation interval | 60s | 10s |
| Aggregation period | 60s | 10s |
| Metric lag (closed-period buffer) | 60s | 0s |
| Scale-out window | 2 of last 3 fresh cycles overload | 2 of 3 |
| Scale-in window | 5 of last 5 fresh cycles comfortable | 3 of 3 |
| Min requests/period for latency+error evaluability | 20 | 20 |

Rule (both profiles): a cycle only counts toward a window if it is "fresh" (no pending capacity change in flight when it was evaluated); any capacity change resets both windows.

## 2. Thresholds

| Parameter | Value |
|---|---|
| Scale-out CPU trigger | ≥ 70% |
| Scale-in CPU projection bound | ≤ 55% (of projected N-1 average) |
| Latency SLO (p95) | ≤ 500ms initial; replace with ≈3× measured idle baseline once measured |
| Error SLO | < 1% 5xx |
| Scale-in comfort — latency | ≤ 350ms (70% of SLO) |
| Scale-in comfort — error | ≤ 0.5% |
| Min instances | 1 (imposed, REQ-CONSTRAINT-2) |
| Max instances | 5 (imposed, REQ-CONSTRAINT-2) |
| Max scale-out step | +2 (or +1 if triggered by latency/error alone) |
| Scale-in step | always -1 |

## 3. Timeouts (see `docs/spec/lifecycle-and-failures.md` for full rationale)

| Parameter | Realistic | Demo |
|---|---|---|
| Warmup estimate | 180s (replaced by measured value) | 20s |
| Pending timeout | 360s | 45s |
| Deregistration delay | 60s | 10s |
| Drain timeout | 90s | 40s |
| Per-AWS-call timeout / retries | 10s / 3 retries, backoff 1-2-4s (cap 30s) | same |
| Per-cycle time budget | 40s | 10s |
| Circuit-breaker failure threshold | 3 consecutive | 3 |
| Circuit-breaker cool-off | ~10 min | ~10 min |

## 4. Instance and infra sizing

| Parameter | Value |
|---|---|
| Instance type | `t3.micro` |
| CPU credit mode | `unlimited` |
| Availability Zones | ≥ 2 (ALB requirement) |
| Cross-zone rebalancing | suspended |
| Blind-cycle alert threshold | 5 consecutive (realistic) / 3 (demo) |

## 5. Configuration mechanism

The two profiles are defined in code as Go structs (the single source of the parameter values above). In real mode, parameters are loaded from a single versioned configuration file at controller startup and resolved on top of a profile (file loading is part of Milestone 3). A SHA-256 hash of the **resolved** configuration (`config_hash`) is computed over its canonical JSON encoding — never over the bytes of a file, so formatting or comment changes in the file do not change the hash, while any parameter change does. The hash (`config_hash`) is recorded in every decision-log `cycle` record (`docs/spec/decision-log.md`), so any two runs (or a run vs. a simulator scenario) can be verified to have used identical parameters before their results are compared — this directly supports REQ-PRESENT-8 (comparing two controller configurations at equal SLO).
