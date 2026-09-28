# Infrastructure

Answers REQ-Q-7, REQ-CONSTRAINT-1, REQ-CONSTRAINT-2. Provisioned entirely via Terraform (`infra/`).

## 1. Components

| Component | Purpose | Notes |
|---|---|---|
| VPC + subnets (≥2 AZs) | Network foundation | ALB requires at least 2 Availability Zones |
| Application Load Balancer | Entry point, source of latency/error/traffic metrics | Target group with a lightweight `/health` check |
| Auto Scaling Group (application) | Actuator only — min 1, max 5, no scaling policies attached | Cross-zone rebalancing suspended |
| Launch template | `t3.micro`, `unlimited` CPU credit mode, LabRole instance profile | Runs the minimal test application (`docs/spec/app.md`) |
| Controller EC2 instance | Runs the controller binary, outside the application ASG | Also `t3.micro`, LabRole instance profile |
| Security groups | ALB → app instances (app port only), controller instance → AWS APIs (outbound only), no public SSH beyond the student's own IP for debugging | |

## 2. Cost-control: destroy-per-session

**The Application Load Balancer does not stop when an AWS Academy Learner Lab session ends — only EC2 instances auto-stop. The ALB keeps accruing hourly charges, and billing visibility in the Academy dashboard lags by 8-12 hours.** Given the $50 total credit budget, this is a material risk if left unmanaged. See `docs/context/constraints.md` and ADR-0016.

**Decision**: `terraform destroy` is run at the end of every work session; `terraform apply` is run at the start of the next. This is documented as a mandatory step in `README.md`'s run instructions, not an optional cleanup task.

## 3. State

Terraform state is kept local (not committed — covered by `.gitignore`) for this single-developer, single-environment project; a remote backend is unnecessary given the short-lived, destroy-per-session nature of the deployment.

## 4. Reproducibility

`infra/` contains all Terraform source; `README.md` documents the exact `terraform apply`/`terraform destroy` sequence, required variables (region, allowed SSH CIDR, project tag used by the IAM policy's tag condition), and how to point the controller binary's configuration at the created ALB/ASG names — satisfying REQ-DELIV-2 (reproducible implementation).

## 5. Sources

- AWS. *Application Load Balancers must span at least two Availability Zones* — https://docs.aws.amazon.com/elasticloadbalancing/latest/application/application-load-balancers.html
- AWS. *Auto Scaling groups with multiple Availability Zones* — https://docs.aws.amazon.com/autoscaling/ec2/userguide/asg-in-vpc.html
- HashiCorp. *Terraform AWS Provider documentation* — https://registry.terraform.io/providers/hashicorp/aws/latest/docs
