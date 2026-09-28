# ADR-0011: Experiment Design — Closed-Loop Simulator as Primary Evidence

- **Status**: Accepted
- **Related**: REQ-DELIV-3, REQ-OBJ-3/4/5, `docs/spec/simulator.md`

## Context

AWS Academy Learner Lab constraints (no external load testing, $50 budget, 4-hour sessions, an ALB that keeps billing after session end) make extensive real-AWS experimentation impractical and risky. The challenge still requires solid experimental evidence covering increase, decrease, and failure-handling behavior.

## Decision

Make a deterministic, seeded, `FakeClock`-driven closed-loop simulator the **primary** evidence source, with 10 explicit scenarios (S1-S10) doubling as automated regression tests. Reserve a single short (45-60 minute) real-AWS run for integration evidence only — proving the same code path works against real AWS APIs — not for sizing or behavior validation.

## Alternatives considered

- **Relying only on real AWS runs for evidence**: rejected — cost and account-suspension risk (external load testing is explicitly disallowed) make this impractical for the range of scenarios required (e.g., repeated failure injection, multi-hour sustained load).
- **Simulating without ever touching real AWS**: rejected — would not demonstrate that the adapters actually work against the real APIs (credential handling, real API shapes, real latency characteristics), which the deliverables require.

## Consequences

- The simulator and real adapters share the same core and logger, so results are structurally comparable even though only the simulator is used for full scenario coverage.
- The real run's guardrails (duration cap, cost estimate, mandatory destroy) are a first-class part of the experiment design, not an afterthought.

## Sources

- Herbst, N. R., Kounev, S., & Reussner, R. (2013). *Elasticity in Cloud Computing: What It Is, and What It Is Not.* ICAC 2013.
