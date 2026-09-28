# Roadmap

Work is organized into four milestones. Each maps to a GitHub milestone and a set of issues (see `docs/plan/backlog.md` and the issues created in the repository).

## Milestone 1 — Core domain and decision policy

Goal: a fully unit-tested, pure decision core with zero I/O, implementing `docs/spec/decision-policy.md` exactly.

- Domain types (signals, quality states, capacity snapshot, breaker state, decision, reason codes).
- `Decide()` pure function implementing the threshold/window/step-size rules.
- Unit tests covering every reason code and every threshold boundary (including boundary values: exactly 70%, exactly 55%, N=1 edge case for the projection formula).

## Milestone 2 — Adapters and closed-loop simulator

Goal: hexagonal adapters for both real AWS and simulation, plus the full S1-S10 scenario suite.

- Ports: `MetricsSource`, `InstanceProvisioner`, `StateStore`, `DecisionLogger`, `Clock`.
- Real adapters: CloudWatch, EC2/ASG, file-based state, JSONL logger, system clock.
- Simulation adapters: mock metrics generator (seeded), fake in-memory ASG (with latency/failure injection), `FakeClock`.
- Control loop (application layer) wiring ports together with the per-cycle time budget.
- Simulator entrypoint running scenarios S1-S10 against the fake adapters, asserting the acceptance criteria in `docs/spec/simulator.md`.

## Milestone 3 — Infrastructure and real deployment

Goal: reproducible AWS infrastructure and a working real-mode run.

- Terraform: VPC, ALB, target group, application ASG, launch template, controller instance, security groups (`docs/spec/infrastructure.md`).
- `infra/iam/controller-policy.json` (`docs/spec/iam.md`) plus the code-vs-policy consistency test.
- Minimal test application (`docs/spec/app.md`): `/`, `/health`, `/admin/stress`.
- Controller configuration for real mode; documented `terraform apply` → run → collect evidence → `terraform destroy` procedure in the README.
- One short, guarded real-AWS integration run producing at least one real decision-log file.

## Milestone 4 — Evaluation and reporting

Goal: turn decision logs into the metrics and report structure required by `docs/spec/evaluation.md`.

- Analysis script: reads JSONL logs, computes SLO compliance, instance-minutes vs. theoretical minimum, over/under-provisioning, oscillation count, time-to-relief, reason-code breakdown.
- Report sections mapped to REQ-PRESENT-1..8.
- Final consistency pass across all documentation (`docs/plan/traceability.md`, `docs/verification.md`).

## Sequencing note

Milestones 1 and 2 have no AWS dependency and no cost — they should be completed and thoroughly tested before Milestone 3 consumes any Learner Lab credits, since Milestone 3 is the only place real money is at stake.
