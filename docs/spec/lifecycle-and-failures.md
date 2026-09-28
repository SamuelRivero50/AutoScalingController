# Instance Lifecycle, Timeouts, and Failure Handling

Answers REQ-Q-7 (partially), REQ-Q-8, REQ-Q-10, REQ-Q-11, REQ-CONSTRAINT-8.

## 1. Signal quality states

Every observed signal is classified per cycle before it is used by the decision policy:

| Quality | Meaning | Effect |
|---|---|---|
| `VALID` | Fresh, in-range value for the current period | Used normally |
| `NOT_EVALUABLE` | No traffic in the period, or fewer than 20 requests | Latency/error not considered this cycle; does not count as breaching or comfortable |
| `NO_NEW_DATA` | Same datapoint timestamp already consumed in a previous cycle | Does not count toward any window (prevents double-counting a stale CPU reading — see `docs/spec/signals.md` §3) |
| `MISSING` | Fetch failed, or no CPU datapoint at all for an in-service instance | Treated conservatively (see safety rules below) |
| `STALE` | Datapoint older than `2 × expected_period + metric_lag` | Discarded, treated like `MISSING` |
| `ANOMALOUS` | Value out of physically possible range (e.g., negative CPU) | Discarded, treated like `MISSING` — never used to trigger a decision by magnitude alone (spikes are only ever filtered by the M-of-N window, never by a magnitude cutoff, to avoid silently hiding real overload) |

**Absent-metric semantics that are NOT faults:**
- `RequestCount` absent for a period = zero traffic (ALB does not publish datapoints with zero traffic) — quality `NOT_EVALUABLE`, not `MISSING`.
- 5xx error-count metrics absent while `RequestCount` is present = zero errors for that period (ALB only publishes 5xx counters when the count is nonzero) — quality `VALID` with value 0.
- `CPUUtilization` absent for an in-service instance is a real gap: if fewer than 50% of in-service instances report a CPU value, the **aggregate** CPU signal becomes `MISSING`.

Only closed (already elapsed) metric periods are used; `metric_lag` (60s realistic, 0s demo) accounts for CloudWatch's own ingestion delay so the controller never reads a partial, still-filling period.

## 2. Safety rules under missing data

- **Never scale in blindly.** `REDUCE_CAPACITY` requires `VALID` + `comfortable` CPU evidence across **all** cycles in the scale-in window. Any `MISSING`/`STALE` CPU reading anywhere in that window blocks scale-in for that cycle.
- **Blind cycle**: if CPU is unusable (`MISSING`/`STALE`/`ANOMALOUS`), the decision is `MAINTAIN_CAPACITY` with reason code `MAINTAIN_BLIND`, regardless of latency/error quality. Latency and errors never trigger a scale-out on their own (they only confirm the CPU trigger, `docs/spec/decision-policy.md`), so without usable CPU neither overload nor safe scale-in can be established; valid latency/error values are still recorded in the cycle. `NO_NEW_DATA` CPU is not blind (the value is real, just already consumed). After **5 consecutive** blind cycles (realistic) / **3 consecutive** (demo), an alert event is logged (`type: event`, see `docs/spec/decision-log.md`) — the controller does not act unsafely, but a human is notified out-of-band once the experiment allows it.
- Capacity/instance-state truth (desired/pending/in-service/draining counts) comes from the ASG and ALB target-health APIs directly, never from CloudWatch, so a CloudWatch outage cannot corrupt the controller's understanding of current capacity — only its understanding of *load*.

## 3. Timeouts and warmup

| Parameter | Realistic profile | Demo profile |
|---|---|---|
| Instance warmup estimate (launch → healthy) | 180s (until measured; then replaced with the measured value) | 20s |
| Pending timeout (2× warmup) | 360s | 45s |
| Target-group deregistration delay | 60s (AWS default is 300s; overridden) | 10s |
| Drain timeout (delay + 30s) | 90s | 40s |
| Per-AWS-call attempt timeout | 10s **per attempt**, 3 retries, exponential backoff + jitter (1s, 2s, 4s; cap 30s); the whole call is bounded by the cycle budget | same |
| Per-cycle time budget | 40s | 10s |
| Circuit-breaker threshold | 3 consecutive provisioning failures | 3 |
| Circuit-breaker cool-off before one probe attempt | ~10 minutes | ~10 minutes (kept equal deliberately — see ADR-0004) |

**Warmup measurement**: warmup is measured automatically from ASG scaling-activity timestamps (launch request → `InService` → ALB target `healthy`), refined by an independent 5-second-interval measurement script run once against the real environment before the formal experiment, so the realistic-profile timeout is evidence-based rather than assumed.

**Per-cycle time budget rationale**: the 10s timeout applies to each attempt, not to the whole call, so a single call can take up to ~47 seconds in the worst case (4 attempts × 10s + 1+2+4s of backoff). The per-attempt timeout is enforced by the AWS adapters (HTTP client timeout plus the SDK retryer); the control loop bounds every call only by the remaining cycle budget, so retries cannot push a cycle past the budget; without a budget, a slow cycle could overlap with the next scheduled cycle. If the 40-second budget is exhausted, remaining unread signals become `MISSING` for that cycle, no action is executed, but the cycle is still logged (satisfying REQ-CONSTRAINT-7 — even a "gave up" cycle is explainable).

**Idempotent actions**: every capacity-changing call sets an **absolute** desired-capacity value (never a delta) with `HonorCooldown=false` (ASG's own cooldown is not used — see ADR-0004 for why). This makes retrying a timed-out action safe: re-sending the same absolute target cannot double-apply a delta.

## 4. Instance failure detection

- An instance is marked `FAILED` immediately when an explicit EC2/ASG API error is returned for it, or when its lifecycle state becomes `Terminated`/`Terminating` unexpectedly.
- An instance reported `unhealthy` by the ALB target-health check is **not** immediately marked failed — it instead waits out the pending-timeout, because early health-check failures during normal startup are expected and would otherwise cause false-positive failure detection.
- **State mapping**: the controller reports an instance as `IN_SERVICE` only when it is `InService` in the ASG **and** its target is `healthy` in the ALB; until then it is `PENDING`. The same signal therefore drives the state-based cooldown, the pending-timeout, and the circuit-breaker reset below.
- **Real launch failures are detected via `DescribeScalingActivities`**, not by inspecting the return value of `SetDesiredCapacity`. The ASG's own internal retry/replace behavior can silently retry a failed launch, so a simple "did the API call succeed" check would miss real failures. This required adding `autoscaling:DescribeScalingActivities` to the least-privilege IAM policy (see `docs/spec/iam.md`).
- **Stuck-pending instance**: if an instance remains `PENDING` past the pending-timeout (measured from its launch time), the controller terminates it explicitly (`TerminateInstanceInAutoScalingGroup`, with capacity decrement) and re-evaluates — this converts an indefinite wait into a bounded one, then lets the next cycle's decision logic decide whether to try again. At most one instance is terminated per cycle (oldest first), since a cycle executes a single action; no scaling decision is taken while instances are `PENDING`, so releasing one per cycle is safe.
- **Drain timeout**: when an instance stays `DRAINING` past the drain timeout, the controller logs an `INSTANCE_STATE_CHANGE` event noting the expired drain and stops counting that instance as `DRAINING` for the scale-in block. The ASG remains responsible for terminating it; without this rule a hung drain would block scale-in indefinitely. (Implemented with the real adapters in Milestone 3; in the simulator a drain always completes.)
- **Circuit breaker**: after 3 consecutive provisioning failures, the breaker opens: the controller sets desired capacity to the current healthy-instance count (never below 1) to stop the ASG's own retry loop, and does not request further scale-out for ~10 minutes. After the cool-off, exactly one probe scale-out attempt is made; success closes the breaker, failure reopens it. The probe succeeds when the launched instance reaches `IN_SERVICE` (as defined above) and fails on a failed launch activity or a stuck-pending termination.
- **Failure counting**: the ASG retries a failed launch on its own, and every retry appears as a new failed activity, so counting raw activities would open the breaker from a single decision. The counter therefore follows these rules:
  - A *provisioning failure* is a failed launch activity (`DescribeScalingActivities`) or a stuck-pending termination. An API error from `SetDesiredCapacity` is not a provisioning failure: it is recorded as `action.status: ERROR` (counting it would not help, since opening the breaker issues another `SetDesiredCapacity` that would fail the same way).
  - Each activity is counted once, deduplicated by `ActivityId`, even if it is visible across several cycles.
  - At most **one** failure is added per cycle, whatever the number of new failed activities or stuck terminations seen in that cycle.
  - The counter resets to 0 when an instance reaches `IN_SERVICE`.
  - The effective meaning of the threshold is therefore "3 consecutive cycles with a provisioning failure and no instance becoming healthy". Horizontal retrying is deliberately bounded this way — the assignment does not make ASG's own failure recovery the controller's responsibility, and unbounded retries would waste time and (potentially) money without addressing a root cause outside the controller's control (this is explicitly called out as an "alert a human" case, not a "solve it automatically" case).

## 5. Health-check interaction with overload testing

A real risk: an instance under genuine heavy load could become slow enough to fail its own ALB health check and be killed before the controller has a chance to act, corrupting the experiment. Mitigations:
- The application's `/health` endpoint is intentionally lightweight (no dependency on the stress-test code path).
- The ALB health-check timeout/threshold is set generously (beyond the default) for the experiment.
- The internal stress-test endpoint targets 80-90% CPU, not 100%, during the real-AWS run specifically to avoid this failure mode while still reliably crossing the 70% scale-out trigger.

## 6. Sources

- AWS. *Health checks for Auto Scaling instances* — https://docs.aws.amazon.com/autoscaling/ec2/userguide/ec2-auto-scaling-health-checks.html
- AWS. *Auto Scaling group cooldowns* — https://docs.aws.amazon.com/autoscaling/ec2/userguide/ec2-auto-scaling-cooldowns.html
- AWS. *DescribeScalingActivities API* — https://docs.aws.amazon.com/autoscaling/ec2/APIReference/API_DescribeScalingActivities.html
- Nygard, M. (2018). *Release It!* — Circuit Breaker pattern.
- AWS. *Deregistration delay for your target groups* — https://docs.aws.amazon.com/elasticloadbalancing/latest/application/application-load-balancers.html#deregistration-delay
