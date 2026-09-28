# Auto-Scaling Controller

[![Go Version](https://img.shields.io/github/go-mod/go-version/SamuelRivero50/AutoScalingController)](https://go.dev/)
[![License](https://img.shields.io/github/license/SamuelRivero50/AutoScalingController)](./LICENSE)

A student-authored horizontal auto-scaling controller for AWS EC2, built for a Cloud Computing course challenge. The controller decides when to add or remove EC2 instances behind an Application Load Balancer using its own reactive, threshold-based policy — no AWS-managed dynamic scaling policy makes the scaling decision.

Every decision is a pure function of an observed snapshot (CPU, latency, error rate, capacity) and is written to an append-only decision log, so each action is reproducible and explainable.

## Status

The decision core and the closed-loop simulator are complete and tested. The real-AWS adapters and infrastructure are not implemented yet.

| Milestone | Scope | State |
| --- | --- | --- |
| M1 | Pure decision core and policy | Done |
| M2 | Ports, simulated adapters, control loop, simulator (S1-S10) | Done |
| M3 | Terraform infrastructure, real CloudWatch/EC2 adapters, test app | Not started |
| M4 | Log analysis and evaluation report | Not started |

## Requirements

- Go 1.27 or later (see `go.mod`)
- A POSIX shell and `make` (optional; every target maps to a plain `go` command)
- `golangci-lint` v2 for linting (`make tools` installs it into `$(go env GOPATH)/bin`)

No AWS account or credentials are needed to build, test, or run the simulator.

## Getting started

```bash
git clone https://github.com/SamuelRivero50/AutoScalingController.git
cd AutoScalingController
make sim          # run all simulator scenarios (no AWS, no cost)
```

`make sim` runs the ten scenarios against simulated adapters with a controllable clock, prints a PASS/FAIL line per scenario, and writes the JSONL decision logs under `sim-logs/`.

Run one scenario and inspect its log:

```bash
go run ./cmd/simulator -scenario S6 -seed 1 -out sim-logs
cat sim-logs/S6-seed1/*.jsonl | head
```

`cmd/simulator` flags:

| Flag | Default | Meaning |
| --- | --- | --- |
| `-scenario` | `all` | scenario ID (`S1`..`S10`) or `all` |
| `-seed` | `1` | seed for the deterministic metrics generator |
| `-out` | `sim-logs` | directory for the JSONL logs (one subdirectory per scenario) |

## Development

```bash
make test    # run the test suite
make race    # run the tests with the race detector
make lint    # run golangci-lint
make fmt     # format the code
make check   # fmt + vet + lint + race (what CI should run)
```

Every target is a thin wrapper over the underlying `go` command; run `make help`-style by reading the `Makefile` if you prefer to invoke `go` directly.

## How it works

The controller is a hexagon: a pure decision core surrounded by an imperative shell that talks to the outside world through five ports. The same core and control loop drive both the simulator and (later) the real AWS deployment; only the adapters differ.

```
        observe → analyze → decide → act → log   (one cycle)

  MetricsSource ─┐                        ┌─ InstanceProvisioner
                 ├─►  core.Decide (pure)  ─┤
        Clock ───┘        ▲                └─ StateStore
                          │
                   DecisionLogger
```

- `internal/core` — the pure decision function, signal classification, evaluation windows and circuit breaker. Standard library only; no I/O, no clock, no globals.
- `internal/ports` — the five port interfaces (`MetricsSource`, `InstanceProvisioner`, `StateStore`, `DecisionLogger`, `Clock`).
- `internal/app` — the control loop: per-cycle time budget, one action per cycle, event logging, config hashing.
- `internal/adapters` — simulated adapters (`fakeasg`, `mockmetrics`, `clock`, `filestate`, `memstate`, `jsonllog`); real AWS adapters arrive in M3.
- `internal/simulator` + `cmd/simulator` — the S1-S10 scenarios and the command that runs them.

The decision rules, thresholds, windows and failure handling are specified normatively in [`docs/spec/`](docs/spec/); the reasoning behind each choice is in [`docs/adr/`](docs/adr/).

## Repository layout

```
cmd/simulator/         closed-loop simulator (scenarios S1-S10)
internal/core/         pure decision function and domain types
internal/ports/        port interfaces
internal/app/          control loop
internal/adapters/     simulated adapters (real AWS adapters land in M3)
internal/simulator/    scenario definitions and runner
docs/                  design documentation (see below)
infra/iam/             least-privilege IAM policy document
```

## Documentation

- [`docs/context/challenge.md`](docs/context/challenge.md) — the assignment as traceable requirements.
- [`docs/context/constraints.md`](docs/context/constraints.md) — AWS Academy Learner Lab constraints that shaped the design.
- [`docs/context/taxonomy.md`](docs/context/taxonomy.md) — elasticity taxonomy classification.
- [`docs/spec/`](docs/spec/) — normative design specification.
- [`docs/adr/`](docs/adr/) — architecture decision records, one per decision, each with sources.
- [`docs/plan/`](docs/plan/) — roadmap, backlog and the requirement-to-decision-to-implementation traceability matrix.
- [`docs/verification.md`](docs/verification.md) — items to confirm empirically during the real-AWS run.

## Running against real AWS

Not available yet — the Terraform infrastructure and the real CloudWatch/EC2 adapters are Milestone 3. When implemented, the run will be guarded: a cost estimate and explicit confirmation before `terraform apply`, and a mandatory `terraform destroy` afterwards, because the Application Load Balancer keeps billing after an AWS Academy session ends even though EC2 instances auto-stop (see [`docs/context/constraints.md`](docs/context/constraints.md) and [ADR-0016](docs/adr/0016-destroy-per-session-cost-control.md)).

## Credentials

No AWS credentials are ever committed to this repository. Local runs use environment variables from the AWS Academy Learner Lab's temporary session-token credentials; the real deployment will use an EC2 instance profile. See [`docs/spec/iam.md`](docs/spec/iam.md).

## License

Course project. No license is granted; all rights reserved by the author unless stated otherwise.
