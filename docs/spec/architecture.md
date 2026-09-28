# Architecture

## 1. Style: Hexagonal (Ports and Adapters) + Functional Core, Imperative Shell

The controller is organized as a hexagon so the decision logic can be tested without touching AWS, and so the same decision logic drives both the simulator and the real deployment.

```
                         ┌─────────────────────────────┐
                         │        Domain Core          │
                         │  (pure, no I/O, no clock)    │
                         │                              │
                         │   Decide(state) -> Decision  │
                         └───────────────▲──────────────┘
                                         │ pure function call
                         ┌───────────────┴──────────────┐
                         │      Application Layer        │
                         │   Control loop (imperative)    │
                         │  observe -> analyze -> decide  │
                         │        -> act -> log           │
                         └──┬───────┬───────┬───────┬────┘
                            │       │       │       │
                     ┌──────▼─┐ ┌───▼───┐ ┌─▼─────┐ ┌▼──────────┐
                     │Metrics │ │Instance│ │State  │ │Decision   │
                     │Source  │ │Provis- │ │Store  │ │Logger     │
                     │(port)  │ │ioner   │ │(port) │ │(port)     │
                     │        │ │(port)  │ │       │ │           │
                     └───┬────┘ └───┬───┘ └───┬───┘ └─────┬─────┘
                         │          │         │           │
             ┌───────────▼──┐  ┌────▼─────┐ ┌─▼────────┐ ┌▼──────────┐
             │CloudWatch    │  │EC2/ASG   │ │File-based│ │JSONL file │
             │adapter       │  │adapter   │ │state     │ │logger     │
             │(real) /      │  │(real) /  │ │adapter   │ │adapter    │
             │Mock adapter  │  │Fake      │ │          │ │           │
             │(simulator)   │  │adapter   │ │          │ │           │
             │              │  │(sim)     │ │          │ │           │
             └──────────────┘  └──────────┘ └──────────┘ └───────────┘

                    A fifth port, Clock, is used everywhere time is read,
                    so the simulator can use a FakeClock and run scenarios
                    in seconds instead of real minutes.
```

## 2. Ports (interfaces)

| Port | Responsibility | Real adapter | Test/simulation adapter |
|---|---|---|---|
| `MetricsSource` | Fetch CPU, `TargetResponseTime`, error counts, `RequestCount`, `HealthyHostCount` for a time window | CloudWatch adapter (`GetMetricData`) | Mock/deterministic-generator adapter |
| `InstanceProvisioner` | Read current capacity/health, request a new desired capacity, read scaling-activity outcomes | EC2/ASG adapter (`DescribeAutoScalingGroups`, `SetDesiredCapacity`, `DescribeScalingActivities`, `DescribeTargetHealth`) | Fake in-memory ASG adapter with configurable launch latency/failure injection |
| `StateStore` | Persist/restore the controller's own transient memory (windows, breaker counters, last-consumed datapoint markers) | Atomic local JSON file | In-memory adapter |
| `DecisionLogger` | Append `cycle` and `event` records | JSONL file adapter (append-only) | Same adapter (used identically in both modes — this is deliberate, see `docs/spec/decision-log.md`) |
| `Clock` | Provide current time, sleep/wait | Real system clock | `FakeClock` (controllable, used to compress simulated hours into seconds) |

## 3. Domain core

The core exposes one pure function:

```go
func Decide(state PolicyInput) Decision
```

`PolicyInput` is an immutable snapshot: signal values + qualities, evaluation windows, current capacity/instance states, and breaker state. `Decision` is an immutable value: the decision literal, the reason code, the justification (evaluated conditions), and the requested action (if any). No port is accessible from this function — it cannot call AWS, read a clock, or write a file. This makes `Decide` trivially unit-testable and guarantees that every decision is reproducible from logged inputs (satisfying REQ-CONSTRAINT-7).

## 4. Application layer (control loop)

Pseudocode of one cycle:

```
loop every EvaluationInterval (or immediately in simulator time):
    budget := StartCycleBudget(40s realistic / 10s demo)
    signals := MetricsSource.Fetch(window, budget)
    capacity := InstanceProvisioner.DescribeCapacity()
    windowState := StateStore.Load()
    input := BuildPolicyInput(signals, capacity, windowState, BreakerState)
    decision := Decide(input)              // pure, core
    result := Shell.Execute(decision)      // impure: calls InstanceProvisioner if needed
    StateStore.Save(UpdatedWindowState)
    DecisionLogger.Append(CycleRecord{input, decision, result})
```

## 5. Deployment topology

- **Simulation mode**: runs entirely on the developer's machine (WSL), no AWS calls, `FakeClock` compresses time.
- **Real mode**: the controller binary runs on a small dedicated EC2 instance **outside** the Auto Scaling Group being controlled. This is deliberate: a controller crash must not take down the application, and the application's own scaling must not accidentally affect the controller's host. The controller instance is not part of the 1-5 instance limit (REQ-CONSTRAINT-2) but does count against the Learner Lab's regional instance cap (see `docs/context/constraints.md`).
- Both modes use the exact same application layer, core, and `DecisionLogger`; only the adapters differ. This is the primary payoff of the hexagonal structure: the evidence produced by the simulator (fast, free, deterministic) and by the real run (slow, costs credits) is structurally the same and comparable.

## 6. Source layout (implementation guideline)

```
/cmd/controller/          entrypoint (selects real or sim adapters via config)
/internal/core/           pure decision function + types (no imports outside stdlib)
/internal/app/            control loop, cycle budget, window/breaker bookkeeping
/internal/ports/          port interfaces
/internal/adapters/cloudwatch/
/internal/adapters/mockmetrics/
/internal/adapters/asg/
/internal/adapters/fakeasg/
/internal/adapters/filestate/
/internal/adapters/jsonllog/
/internal/adapters/clock/
/cmd/simulator/           closed-loop simulator entrypoint (scenarios S1-S10)
/cmd/testapp/             minimal Go test application (hello world + /health + stress endpoint)
```
