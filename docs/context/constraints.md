# Operating Constraints

These constraints come from two independent sources: the challenge assignment (`docs/context/challenge.md`, §6) and the practical limits of running on an **AWS Academy Learner Lab**. They drive several infrastructure and process decisions (see ADR-0009, ADR-0012, ADR-0016).

## 1. AWS Academy Learner Lab

- **Budget**: ~$50 in credits for the whole course project (confirmed by the student's own account); a hard AWS-side spend cap exists ($50 or $100 depending on the lab), after which the lab locks.
- **Session length**: Each lab session lasts **4 hours**. When the session ends:
  - EC2 instances are **auto-stopped** (not terminated) — no charge while stopped.
  - **The Application Load Balancer is NOT auto-stopped** and keeps accruing hourly charges even with no active session. Cost visibility in the AWS Academy billing dashboard lags by roughly 8-12 hours, so an ALB left running can burn a meaningful fraction of the budget before it is even visible.
  - **Decision**: the environment is destroyed with `terraform destroy` at the end of every work session and recreated with `terraform apply` at the start of the next one. See ADR-0016.
- **IAM restrictions**: Custom IAM roles cannot be created, and policies cannot be attached to the lab's built-in `LabRole`. The controller runs under `LabRole` in the lab; the least-privilege policy in `infra/iam/controller-policy.json` documents what would be attached in a normal AWS account. See `docs/spec/iam.md`.
- **No load testing**: Generating real external load against the account risks the account being flagged/suspended. All load is generated **internally** (a single-request admin endpoint that busies the CPU on one instance for a bounded time), never via external load-testing tools or traffic. This was explicitly suggested by the course instructor as an acceptable substitute.
- **Region/instance limits**: The lab enforces a regional cap (9 instances / 32 vCPUs observed). The dedicated controller EC2 instance (outside the ASG) counts against this cap even though it does not count toward the application's 1-5 instance limit.

## 2. Instance sizing

- **Instance type**: `t3.micro` — smallest practical burstable instance, chosen to conserve the $50 credit budget.
- **CPU credit mode**: `unlimited` (t3's default). In the default *standard* credit mode, CPU is throttled once baseline credits are exhausted, which would prevent the internal stress-test endpoint from reliably reaching the 70% CPU scale-out threshold — breaking the experiment. `unlimited` mode allows sustained bursts above baseline at a small surplus cost ($0.05/vCPU-hour, only if the 24-hour rolling average exceeds baseline), which is negligible for runs of under an hour.

## 3. Networking

- The Application Load Balancer requires the Auto Scaling Group to span **at least two Availability Zones**. Cross-zone instance rebalancing is suspended via Terraform so that scale-in always removes a predictable, explicitly-chosen instance rather than being redistributed by ASG's own rebalancing logic.

## 4. Credentials

- No AWS credentials, session tokens, or `.pem` files are ever committed. `.gitignore` excludes `.env`, `.aws/`, `*.pem`, and Terraform state/plan files. `gitleaks` runs as a pre-commit hook (`.pre-commit-config.yaml`) and in CI (GitHub Actions), so the repository is scanned for key material on every change.
- Local development uses the Learner Lab's temporary session-token credentials via environment variables. The real deployment uses an EC2 instance profile — never hard-coded keys.

## 5. Time budget

- The student has limited remaining time before the deadline. This drove the decision to keep the test application deliberately minimal (a single Go "hello world" binary with a `/health` endpoint and an internal stress-test admin endpoint) and to prioritize the closed-loop simulator (fast, deterministic, no AWS cost) as the primary source of experimental evidence, with a single short real-AWS run reserved for integration proof only.
