# ADR-0013: Minimal Self-Built Test Application; No External Load Testing

- **Status**: Accepted
- **Related**: REQ-DELIV-2, `docs/spec/app.md`, `docs/context/constraints.md`

## Context

The real-AWS integration run needs an application to run behind the ALB, and needs a way to generate load — but AWS Academy Learner Lab accounts risk suspension from external load-testing traffic, and the course instructor explicitly confirmed a minimal application is acceptable.

## Decision

Build a minimal, stateless Go application (hello-world response, `/health`, and an internal `/admin/stress` endpoint that busies the instance's own CPU for a bounded duration at 80-90% utilization). No external load-testing tool or traffic is used.

## Alternatives considered

- **Deploying a realistic sample web application**: rejected — disproportionate to the remaining time budget and to what the challenge actually evaluates (the controller, not the application).
- **External load generation (e.g., a load-testing tool hitting the ALB from outside)**: rejected — explicit AWS Academy account-suspension risk; the instructor's own suggested alternative (internal CPU/memory stress via an admin endpoint) is used instead.

## Consequences

- The application's statelessness is a load-bearing assumption for safe scale-in and for the taxonomy's Scope classification (`docs/context/taxonomy.md`).
- CPU-vs-latency causality in the experiment stays clean because the application has no external dependencies of its own.

## Sources

- Course instructor guidance (verbatim, recorded in project history): internal CPU/memory-consuming endpoints as a substitute for load testing, to avoid account suspension risk.
