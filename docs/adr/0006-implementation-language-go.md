# ADR-0006: Implementation Language — Go

- **Status**: Accepted
- **Related**: `docs/spec/architecture.md`

## Context

Candidates considered were Go, Rust, C++, and Zig. The controller is I/O-bound (waiting on AWS API calls most of the time), needs a small, easily auditable decision core, and must be simple to build, cross-compile, and deploy onto a small EC2 instance under a tight time budget.

## Decision

Implement the entire controller in Go, including both the pure decision core and the imperative I/O shell.

## Alternatives considered

- **Rust**: stronger compile-time guarantees and zero-cost abstractions, but higher development overhead (borrow checker, less mature AWS SDK ergonomics at the time) for a project whose bottleneck is network I/O, not raw compute — the extra rigor would not pay for itself here.
- **Go + Rust combined** (functional core in Rust, imperative shell in Go, or vice versa): considered explicitly and rejected for this project. It adds cross-language FFI/build overhead disproportionate to the size of the decision logic, and Go's own type system and the hexagonal architecture (`docs/spec/architecture.md`) already give the decision core the isolation and testability that motivated considering Rust for it. This pattern is retained as a mental model for future projects with heavier decision logic, not applied here.
- **C++ / Zig**: rejected — no compelling advantage for an I/O-bound, single-binary controller; both add memory-management or tooling-maturity overhead without benefit for this workload.

## Consequences

- A single toolchain, a single dependency graph, a single cross-compilation target (`GOOS=linux GOARCH=amd64`) for the EC2 deployment.
- `log/slog` used for structured logging alongside the JSONL decision log.

## Sources

- AWS SDK for Go v2 documentation — https://aws.github.io/aws-sdk-go-v2/docs/
