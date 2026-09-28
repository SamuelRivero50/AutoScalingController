# ADR-0016: Mandatory Destroy-and-Recreate Infrastructure Per Session

- **Status**: Accepted
- **Related**: `docs/context/constraints.md`, `docs/spec/infrastructure.md`

## Context

AWS Academy Learner Lab sessions last 4 hours and auto-stop EC2 instances at the end, but the Application Load Balancer does **not** auto-stop and continues billing. Spend visibility in the Academy billing dashboard lags 8-12 hours, so an ALB left running between sessions can consume a meaningful fraction of the $50 total credit budget before it is even noticed.

## Decision

Run `terraform destroy` at the end of every work session and `terraform apply` at the start of the next one. This is documented as a mandatory operational step in the README, not an optional cleanup task.

## Alternatives considered

- **Leaving the ALB running between sessions**: rejected outright once the billing-lag risk was verified via research — this was the single highest-risk item to the project's viability given the fixed credit budget.
- **Manually remembering to stop just the ALB** (without destroying everything): rejected — Terraform destroy/apply is more reliable and repeatable than manually tracking which resources need manual stopping, and guarantees a clean, reproducible environment at the start of each session.

## Consequences

- Terraform outputs (ALB DNS name, ASG name) change on every recreate; the controller's configuration must be re-pointed at the new values each session (documented in the README run instructions).
- This is the single most important cost-control decision in the design, directly protecting the project's ability to complete the real-AWS deliverable within budget.

## Sources

- AWS Academy documentation on Learner Lab session behavior (EC2 auto-stop, non-EC2 resources continuing to bill) — course-provided reference material.
- AWS. *Elastic Load Balancing pricing* — https://aws.amazon.com/elasticloadbalancing/pricing/
