# ADR-0007: Hexagonal Architecture + Functional Core, Imperative Shell

- **Status**: Accepted
- **Related**: REQ-CONSTRAINT-7, `docs/spec/architecture.md`

## Context

The decision logic must be entirely student-authored, fully explainable, and testable without needing live AWS access; the same logic must also drive both the free/fast simulator and the real deployment without duplication.

## Decision

Structure the controller as ports and adapters (hexagonal architecture) around a pure functional core (`Decide()`), with all I/O (AWS calls, file state, clock, logging) confined to adapters behind five ports: `MetricsSource`, `InstanceProvisioner`, `StateStore`, `DecisionLogger`, `Clock`.

## Alternatives considered

- **A single monolithic control-loop function mixing I/O and decision logic**: rejected — would make the decision logic untestable without mocking AWS at every call site, and would risk subtle divergence between simulated and real behavior.
- **Object-oriented modeling of the decision logic itself** (stateful decision objects): rejected for the decision function specifically — a pure function of an immutable snapshot is simpler to reason about and guarantees reproducibility from logged inputs (REQ-CONSTRAINT-7). OOP is still used appropriately elsewhere (e.g., modeling instance/resource state, which does have identity and mutation).

## Consequences

- `Decide()` has zero external dependencies and can be fuzz/property-tested directly.
- Simulator and real adapters are interchangeable at the application-layer boundary, so evidence from both is structurally comparable (`docs/spec/simulator.md`).

## Sources

- Cockburn, A. *Hexagonal Architecture (Ports and Adapters)* — https://alistair.cockburn.us/hexagonal-architecture/
