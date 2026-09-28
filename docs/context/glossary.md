# Glossary

Terms are used consistently across all documents in this repository. When a term has a precise technical source, it is cited.

- **Elasticity**: The ability of a system to add and remove resources on the fly to adapt to load variation, defined as `scalability + automation + optimization` (Al-Dhuraibi et al., 2018, §2.1).
- **Scalability**: The ability of a system to sustain increasing workloads by adding resources; time-independent (Herbst et al., 2013).
- **Horizontal scaling**: Adding or removing whole instances (as opposed to resizing an existing instance — vertical scaling). This project uses horizontal scaling exclusively.
- **Reactive control**: Scaling decisions triggered by comparing current observed metrics against fixed thresholds, with no prediction of future load.
- **Over-provisioning**: State where supplied capacity exceeds demand (`S > D`).
- **Under-provisioning**: State where supplied capacity is less than demand (`S < D`).
- **Just-in-need**: A balanced state where capacity closely matches demand and QoS is satisfied without waste (Al-Dhuraibi et al., 2018, §2.1, citing Ai, 2015).
- **Reaction time**: The interval between when a reconfiguration is triggered/requested and when the adaptation completes (Al-Dhuraibi et al., 2018, §2.1).
- **Cycle**: One execution of the control loop (observe → analyze → decide → act) at the configured evaluation interval.
- **Evaluation window (M-of-N)**: A rule requiring M breaching (or comfortable) cycles out of the last N fresh cycles before a decision is confirmed, modeled on CloudWatch alarm evaluation semantics. Used to prevent flapping.
- **Cooldown**: In this design, a *state-based* block on new capacity-changing decisions while an instance is `PENDING` (scale-out) or while anything is `PENDING`/`DRAINING` (scale-in) — not a fixed timer.
- **Stabilization window**: Synonym used interchangeably with "evaluation window (M-of-N)" in early design discussion; the final mechanism is the M-of-N window described above.
- **Hysteresis / dead-band**: The gap between the scale-out threshold (CPU ≥ 70%) and the scale-in comfort bound (CPU × N/(N-1) ≤ 55%) that prevents a scale-in decision from being made immediately after a scale-out (and vice versa).
- **Circuit breaker**: A pattern (Closed / Open / Half-Open) used to stop retrying a provisioning action after repeated failures, to avoid making the situation worse; re-probed after a cooling-off period.
- **Signal quality**: A per-cycle classification of each observed metric: `VALID`, `NOT_EVALUABLE`, `NO_NEW_DATA`, `MISSING`, `STALE`, or `ANOMALOUS`. See `docs/spec/signals.md`.
- **Blind cycle**: A cycle where the controller cannot evaluate enough signals to safely decide anything other than `MAINTAIN_CAPACITY`.
- **SLI / SLO**: Service Level Indicator / Objective, per the Google SRE Book. This design uses p95 `TargetResponseTime` and 5xx error rate as SLIs.
- **Decision record**: One JSON Lines entry logging a single cycle's observation, evaluation, decision, and resulting action. See `docs/spec/decision-log.md`.
- **Reason code**: A closed vocabulary string explaining *why* a particular decision was reached (e.g., `INCREASE_CPU_HIGH`, `MAINTAIN_BLIND`). See `docs/spec/decision-log.md`.
- **Functional core, imperative shell**: An architecture pattern where the decision logic is a pure function with no I/O (the "core"), and all I/O (AWS calls, file writes, clocks) lives in a thin surrounding layer (the "shell"). Used here to keep the decision function unit-testable and fully deterministic.
- **Hexagonal architecture (ports and adapters)**: An architecture style separating the domain core from the outside world via explicit interfaces ("ports") and swappable implementations ("adapters" — e.g., a real CloudWatch adapter vs. a mock metrics adapter for the simulator).
- **ASG**: EC2 Auto Scaling Group. Used here purely as an actuator (min/max/desired capacity, health-check-based self-healing) — never as the decision-maker.
- **ALB**: Application Load Balancer. Source of the `TargetResponseTime`, `HTTPCode_*`, `HealthyHostCount`, and `RequestCount*` metrics.
- **Learner Lab**: AWS Academy's constrained AWS environment used for this project (see `docs/context/constraints.md`).
