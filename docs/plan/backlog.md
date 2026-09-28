# Backlog (Issue List)

This is the source list used to create GitHub issues (see `docs/plan/traceability.md` for the REQ/ADR mapping). Each item below becomes one GitHub issue with the milestone and labels shown.

## Milestone 1 — Core domain and decision policy

1. **Define domain types** (signals, quality enum, capacity snapshot, breaker state, decision, reason-code enum) — labels: `core`, `milestone-1`
2. **Implement `Decide()` per `docs/spec/decision-policy.md`** — labels: `core`, `milestone-1`
3. **Unit tests: threshold boundaries and reason codes** — labels: `core`, `tests`, `milestone-1`
4. **Unit tests: M-of-N window logic (scale-out 2/3, scale-in 5/5 and 3/3)** — labels: `core`, `tests`, `milestone-1`
5. **Unit tests: signal-quality classification (`docs/spec/lifecycle-and-failures.md` §1)** — labels: `core`, `tests`, `milestone-1`

## Milestone 2 — Adapters and closed-loop simulator

6. **Define ports** (`MetricsSource`, `InstanceProvisioner`, `StateStore`, `DecisionLogger`, `Clock`) — labels: `adapters`, `milestone-2`
7. **Implement JSONL `DecisionLogger` adapter + schema validation test against `decision-log.schema.json`** — labels: `adapters`, `milestone-2`
8. **Implement file-based `StateStore` adapter (atomic write, rebuild-from-AWS on corruption)** — labels: `adapters`, `milestone-2`
9. **Implement fake in-memory `InstanceProvisioner` with configurable launch latency and failure injection** — labels: `adapters`, `simulator`, `milestone-2`
10. **Implement mock/deterministic `MetricsSource` generator (seeded RNG)** — labels: `adapters`, `simulator`, `milestone-2`
11. **Implement `FakeClock`** — labels: `adapters`, `simulator`, `milestone-2`
12. **Implement application-layer control loop with per-cycle time budget** — labels: `core`, `milestone-2`
13. **Implement circuit breaker (Closed/Open/Half-Open)** — labels: `core`, `milestone-2`
14. **Implement scenarios S1-S5** (stable, sustained increase, transient vs. genuine spike, sustained decrease, edge-noise oscillation) — labels: `simulator`, `milestone-2`
15. **Implement scenarios S6-S10** (circuit breaker, stuck pending, blindness, no-traffic scale-to-1, composite 90-minute run) — labels: `simulator`, `milestone-2`

## Milestone 3 — Infrastructure and real deployment

16. **Terraform: VPC + subnets across ≥2 AZs** — labels: `infra`, `milestone-3`
17. **Terraform: ALB + target group with lightweight health check** — labels: `infra`, `milestone-3`
18. **Terraform: application ASG (min 1, max 5, no scaling policies, cross-zone rebalancing suspended)** — labels: `infra`, `milestone-3`
19. **Terraform: launch template (t3.micro, unlimited credit mode) + controller instance** — labels: `infra`, `milestone-3`
20. **`infra/iam/controller-policy.json` + code-vs-policy consistency test** — labels: `infra`, `security`, `milestone-3`
21. **Real adapters: CloudWatch `MetricsSource`** — labels: `adapters`, `milestone-3`
22. **Real adapters: EC2/ASG `InstanceProvisioner` (incl. `DescribeScalingActivities` failure detection)** — labels: `adapters`, `milestone-3`
23. **Minimal test application: `/`, `/health`, `/admin/stress`** — labels: `app`, `milestone-3`
24. **README: destroy-per-session run procedure, cost estimate step** — labels: `docs`, `milestone-3`
25. **Guarded real-AWS integration run + evidence collection** — labels: `experiment`, `milestone-3`
31. **Controller entrypoint: config file resolved on a profile, real adapters wiring, per-attempt AWS timeouts, drain timeout** — labels: `core`, `adapters`, `milestone-3`
32. **Stress operator tool (`cmd/stress`): trigger `/admin/stress` on one or all instances from the controller host** — labels: `app`, `experiment`, `milestone-3`
33. **Evidence persistence: S3 upload timer + `scripts/collect-evidence.sh` (fails if empty)** — labels: `infra`, `experiment`, `milestone-3`
34. **Secret scanning: gitleaks pre-commit hook + GitHub Actions workflow** — labels: `security`, `milestone-3`

## Milestone 4 — Evaluation and reporting

26. **Analysis script: SLO compliance, instance-minutes vs. theoretical minimum** — labels: `evaluation`, `milestone-4`
27. **Analysis script: oscillation count, time-to-relief, reason-code breakdown** — labels: `evaluation`, `milestone-4`
28. **Critical analysis report draft (REQ-PRESENT-1..8)** — labels: `docs`, `evaluation`, `milestone-4`
29. **Final documentation consistency pass (traceability matrix, verification checklist)** — labels: `docs`, `milestone-4`
30. **Live-demo load dial for the simulator (demo profile, interactive overload/comfortable control)** — labels: `simulator`, `milestone-4`
