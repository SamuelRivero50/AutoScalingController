# ADR-0017: Manual Constructor Injection

- **Status**: Accepted
- **Related**: ADR-0007, `docs/spec/architecture.md` §4.1

## Context

The hexagonal structure (ADR-0007) has five ports, each with a real adapter and a simulated one. The controller and the simulator must wire the same application layer to different adapter sets.

## Decision

Wire dependencies by manual constructor injection in the two composition roots, `cmd/controller` and `cmd/simulator`. Constructors accept port interfaces and return concrete types.

## Alternatives considered

- **Compile-time DI (google/wire)**: generates the same wiring code, but adds a code-generation step and a tool dependency for a graph of about ten nodes.
- **Runtime DI containers (uber-go/fx, samber/do)**: add reflection-based or container-based lifecycle management that the project does not need; the controller has a single long-running loop and a trivial shutdown.

## Consequences

- The dependency graph is explicit and readable in each `main` package.
- Adding a port means updating both composition roots by hand, which is acceptable at this size.
