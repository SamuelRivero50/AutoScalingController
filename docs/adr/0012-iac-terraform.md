# ADR-0012: Infrastructure as Code — Terraform

- **Status**: Accepted
- **Related**: REQ-DELIV-2, `docs/spec/infrastructure.md`

## Context

The deliverables require reproducible infrastructure setup instructions, and the destroy-per-session cost-control policy (ADR-0016) requires the environment to be torn down and recreated reliably and repeatedly.

## Decision

Use Terraform to define the VPC, ALB, target group, application ASG, launch template, controller instance, and security groups.

## Alternatives considered

- **AWS CDK / CloudFormation**: viable alternatives, but Terraform's plan/apply/destroy workflow and wide familiarity make it a safer choice under a tight time budget, and its declarative HCL is easy to review for the design documentation.
- **Manual console setup**: rejected outright — not reproducible, and directly conflicts with REQ-DELIV-2's requirement for a repo containing infrastructure as code.

## Consequences

- `terraform destroy`/`terraform apply` becomes the standard, documented start/end-of-session ritual (ADR-0016).
- Terraform state is kept local and gitignored, appropriate for a single-developer, short-lived deployment.

## Sources

- HashiCorp. *Terraform AWS Provider documentation* — https://registry.terraform.io/providers/hashicorp/aws/latest/docs
