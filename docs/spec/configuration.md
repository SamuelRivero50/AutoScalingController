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
| Per-AWS-call attempt timeout / retries | 10s per attempt / 3 retries, backoff 1-2-4s (cap 30s); total bounded by the cycle budget | same |
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

## 6. Configuration file format

The file is JSON (standard library only) and is decoded strictly: unknown fields and trailing data are rejected, so a misspelled key cannot silently leave a parameter at its profile default. In the real deployment Terraform renders it (`infra/templates/controller.json.tftpl`).

| Key | Meaning |
|---|---|
| `version` | file format version; only `1` is accepted |
| `mode` | `real` (CloudWatch + Auto Scaling) or `sim` (simulated adapters on the wall clock, a local smoke test; experiments use `cmd/simulator`) |
| `profile` | `realistic` or `demo`, the base the overrides apply to |
| `run_id` | optional; generated as `run-<UTC timestamp>-<random>` when absent |
| `aws` | real mode only: `region`, `asg_name`, `target_group_arn`, `load_balancer_dimension` and `target_group_dimension` (the ARN suffixes used as CloudWatch dimensions), optional `metrics_wait_timeout` (default `10m`) |
| `sim` | sim mode only: `load` (instance units), `initial_desired`, `seed` |
| `paths` | `log_dir` (decision log) and `state_file` (state store) |
| `overrides` | optional: `latency_slo_ms`, `latency_comfort_ms`, `error_slo_pct`, `error_comfort_pct`, `scale_out_cpu`, `scale_in_projected_cpu`, and the durations `warmup`, `pending_timeout`, `deregistration_delay`, `drain_timeout` (Go duration strings such as `"90s"`) |

Overrides exist for the values §2 and §3 expect to be replaced by measurements (latency SLO from the idle baseline, warmup from the measured launch time) and their dependents; window sizes, bounds and step sizes are not overridable. The resolved configuration is validated as a whole before the controller starts.

At startup in real mode the controller checks with `cloudwatch:ListMetrics` that the target group's `HealthyHostCount` exists for the configured load balancer (proving both dimension values), retrying every 30 seconds until `metrics_wait_timeout`; a fresh deployment publishes it a few minutes after the first health checks. Traffic metrics are not required, because the ALB publishes them only after the first request.
