# Auto-Scaling Controller

A custom, student-authored horizontal auto-scaling controller for AWS EC2, built for a Cloud Computing course challenge. The controller decides when to add or remove EC2 instances behind an Application Load Balancer, using its own reactive, threshold-based policy — no AWS-managed dynamic scaling policy makes the decision.

## Documentation

Start here:

- [`docs/context/challenge.md`](docs/context/challenge.md) — the assignment restated as traceable requirements.
- [`docs/context/constraints.md`](docs/context/constraints.md) — AWS Academy Learner Lab constraints that shaped the design.
- [`docs/context/taxonomy.md`](docs/context/taxonomy.md) — elasticity taxonomy classification.
- [`docs/spec/`](docs/spec/) — normative design specification (architecture, decision policy, signals, failure handling, configuration, decision log, IAM, infrastructure, application, simulator, evaluation).
- [`docs/adr/`](docs/adr/) — architecture decision records, one per design decision, each with sources.
- [`docs/plan/`](docs/plan/) — roadmap, backlog, and a requirement-to-decision-to-implementation traceability matrix.
- [`docs/verification.md`](docs/verification.md) — open items to confirm empirically as implementation proceeds.

## Repository layout

```
cmd/controller/       controller entrypoint
cmd/simulator/         closed-loop simulator (scenarios S1-S10)
cmd/testapp/           minimal test application
internal/core/         pure decision function and domain types
internal/app/          control loop (application layer)
internal/ports/        port interfaces
internal/adapters/     real and simulated adapters
infra/                 Terraform infrastructure
infra/iam/             least-privilege IAM policy document
docs/                  design documentation (see above)
```

## Running the simulator

```
go run ./cmd/simulator -scenario all
```

Runs all scenarios (S1-S10) against the fake adapters with a `FakeClock`; produces JSONL decision logs under `./logs/` for analysis.

## Running against real AWS (guarded)

1. Review the cost estimate and confirm you intend to spend AWS Academy credits.
2. `cd infra && terraform apply`
3. Point the controller's configuration at the Terraform outputs (ALB DNS name, ASG name).
4. `go run ./cmd/controller -mode real -profile realistic`
5. Trigger load via the test application's `/admin/stress` endpoint (never external load generation — see [`docs/spec/app.md`](docs/spec/app.md)).
6. Collect the decision log, then **immediately** run `cd infra && terraform destroy`.

**Step 6 is mandatory, not optional.** The Application Load Balancer keeps billing after an AWS Academy Learner Lab session ends, even though EC2 instances auto-stop. See [`docs/context/constraints.md`](docs/context/constraints.md) and [ADR-0016](docs/adr/0016-destroy-per-session-cost-control.md).

## Credentials

No AWS credentials are ever committed to this repository. Local runs use environment variables populated from the AWS Academy Learner Lab's temporary session-token credentials; the real deployment uses an EC2 instance profile. See [`docs/spec/iam.md`](docs/spec/iam.md).

## License

Course project — no license specified; all rights reserved by the author unless stated otherwise.
