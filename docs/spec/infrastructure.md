# Infrastructure

Answers REQ-Q-7, REQ-CONSTRAINT-1, REQ-CONSTRAINT-2. Provisioned entirely via Terraform (`infra/`).

## 1. Components

| Component | Purpose | Notes |
|---|---|---|
| VPC + public subnets (≥2 AZs) | Network foundation | ALB requires at least 2 Availability Zones |
| Application Load Balancer | Entry point, source of latency/error/traffic metrics | HTTP listener on port 80; a listener rule returns `403` for `/admin/*`, so the stress endpoint is never reachable through the ALB |
| Target group | Health checks and draining | `/health` check: interval 10s, timeout 5s, healthy threshold 2, unhealthy threshold 5 (50s before a target is marked unhealthy); deregistration delay 60s |
| Auto Scaling Group (application) | Actuator only — min 1, max 5, no scaling policies attached | `AZRebalance` suspended; ELB health checks; health-check grace period 400s (above the 360s pending timeout, ADR-0009) |
| Launch template | Application instances | `t3.micro`, `unlimited` CPU credits, IMDSv2 required, detailed monitoring from the `detailed_monitoring` variable (default `true`), Amazon Linux 2023 AMI from the public SSM parameter |
| Controller EC2 instance | Runs the controller binary, outside the application ASG | `t3.micro`; controller runs under systemd with `Restart=on-failure` |
| S3 bucket | Binary delivery and evidence persistence | `force_destroy = true` so `terraform destroy` succeeds while it holds objects; holds the `testapp`, `controller` and `stress` binaries and the uploaded decision logs |
| Security groups | Least-privilege network paths | ALB: port 80 from the internet. App instances: app port only from the ALB SG and the controller SG. Controller: no inbound except SSH from the operator's IP for debugging; outbound to AWS APIs |
| IAM | Instance permissions | Learner Lab: the existing `LabInstanceProfile` (variable). Normal accounts: `create_iam = true` creates the controller role from `infra/iam/controller-policy.json` plus a separate bootstrap statement for S3 (see `docs/spec/iam.md` §2.1) |

Region is a variable (default `us-east-1`). Every resource carries the `Project = auto-scaling-controller` tag used by the IAM condition.

**Known limitation — HTTP only.** The Learner Lab offers no domain or certificate, so the ALB serves plain HTTP on port 80. Traffic in transit is unencrypted; acceptable for a short-lived test application with no user data, and documented rather than hidden.

## 2. Binary delivery and controller configuration

- `testapp`, `controller` and `stress` are built for `linux/amd64` and uploaded to the project bucket by Terraform (`aws_s3_object`). Go binaries exceed the 16 KB user-data limit, and compiling on boot would inflate the measured warmup.
- User data downloads the binaries with the instance profile and installs systemd units.
- The controller's configuration file is rendered by Terraform (`templatefile`) from its own outputs — ASG name, target group ARN, and the ALB and target-group ARN suffixes used as CloudWatch dimensions — and written to the controller instance by user data. No manual re-pointing is needed after a recreate (ADR-0016).

## 3. Evidence persistence

The decision logs are written to the controller instance's disk, which `terraform destroy` deletes. To keep the evidence of the real run (issue #27):

- A systemd timer on the controller instance runs `aws s3 sync` of the log directory to `s3://<bucket>/logs/` every 60 seconds. The upload is done by the AWS CLI, not by the controller binary, so `controller-policy.json` stays limited to what the controller code calls.
- `scripts/collect-evidence.sh` syncs `s3://<bucket>/logs/` to a local `evidence/` directory and fails if no decision-log file was collected. Run it at least 60 seconds after stopping the controller (or after forcing a final sync), so the last minute of records is included.
- Collecting the evidence is a mandatory step **before** `terraform destroy` (see §4 and `README.md`).

## 4. Cost-control: destroy-per-session

**The Application Load Balancer does not stop when an AWS Academy Learner Lab session ends — only EC2 instances auto-stop. The ALB keeps accruing hourly charges, and billing visibility in the Academy dashboard lags by 8-12 hours.** Given the $50 total credit budget, this is a material risk if left unmanaged. See `docs/context/constraints.md` and ADR-0016.

**Decision**: `terraform apply` at the start of every work session; collect evidence, then `terraform destroy` at the end. This is documented as a mandatory step in `README.md`'s run instructions, not an optional cleanup task. The README includes a cost estimate (ALB hours and LCUs, EC2 `t3.micro` hours, public IPv4 addresses at $0.005/hour each for the ALB nodes and every instance with a public IP, and S3) that is reviewed before each apply.

## 5. State

Terraform state is kept local (not committed — covered by `.gitignore`) for this single-developer, single-environment project; a remote backend is unnecessary given the short-lived, destroy-per-session nature of the deployment. The provider lock file (`.terraform.lock.hcl`) **is** committed so provider versions are reproducible.

## 6. Reproducibility

`infra/` contains all Terraform source; `README.md` documents the exact `terraform apply` → run → collect evidence → `terraform destroy` sequence and the required variables (region, operator SSH CIDR, instance profile name, `detailed_monitoring`) — satisfying REQ-DELIV-2 (reproducible implementation).

## 7. Sources

- AWS. *Application Load Balancers must span at least two Availability Zones* — https://docs.aws.amazon.com/elasticloadbalancing/latest/application/application-load-balancers.html
- AWS. *Auto Scaling groups with multiple Availability Zones* — https://docs.aws.amazon.com/autoscaling/ec2/userguide/asg-in-vpc.html
- AWS. *Amazon VPC pricing — public IPv4 addresses* — https://aws.amazon.com/vpc/pricing/
- AWS. *User data size limit (16 KB)* — https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/instancedata-add-user-data.html
- HashiCorp. *Terraform AWS Provider documentation* — https://registry.terraform.io/providers/hashicorp/aws/latest/docs
- HashiCorp. *Dependency lock file* — https://developer.hashicorp.com/terraform/language/files/dependency-lock
