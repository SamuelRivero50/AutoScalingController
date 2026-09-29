# Auto-Scaling Controller

[![Go Version](https://img.shields.io/github/go-mod/go-version/SamuelRivero50/AutoScalingController)](https://go.dev/)
[![License](https://img.shields.io/github/license/SamuelRivero50/AutoScalingController)](./LICENSE)

A student-authored horizontal auto-scaling controller for AWS EC2, built for a Cloud Computing course challenge. The controller decides when to add or remove EC2 instances behind an Application Load Balancer using its own reactive, threshold-based policy — no AWS-managed dynamic scaling policy makes the scaling decision.

Every decision is a pure function of an observed snapshot (CPU, latency, error rate, capacity) and is written to an append-only decision log, so each action is reproducible and explainable.

## Status

The decision core, the closed-loop simulator, the real AWS adapters, the controller entrypoint, the Terraform infrastructure, the log analysis and the live demo are implemented and tested. The guarded real-AWS run, and the report sections that depend on it, are still pending.

| Milestone | Scope | State |
| --- | --- | --- |
| M1 | Pure decision core and policy | Done |
| M2 | Ports, simulated adapters, control loop, simulator (S1-S10) | Done |
| M3 | Terraform infrastructure, real CloudWatch/EC2 adapters, test app, stress tool | Implemented; real run pending |
| M4 | Log analysis, evaluation report, live demo | Implemented on simulator evidence; real-run section pending |

## Requirements

- Go 1.27 or later (see `go.mod`)
- A POSIX shell and `make` (optional; every target maps to a plain `go` command)
- `golangci-lint` v2 for linting (`make tools` installs it into `$(go env GOPATH)/bin`)
- For the real-AWS run only: Terraform 1.10 or later and AWS CLI v2

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
| `-demo` | off | run the interactive live demo instead of the scenarios |
| `-load`, `-initial`, `-cycles` | `0.8`, `2`, `0` | demo only: initial load (instance units), initial instances, cycle limit (`0` = until `quit`) |

## Evaluation and live demo

```bash
make sim && make analyze                 # metrics per scenario plus a summary table
go run ./cmd/analyze -json sim-logs      # the same metrics as JSON
go run ./cmd/analyze evidence/           # works on the logs of a real run too
make demo                                # live demo: demo profile, 10 s cycles
```

`cmd/analyze` reads any `.jsonl` files or directories, groups the records by `run_id` and computes the metrics of `docs/spec/simulator.md` §5 from the log alone. The metrics are SLO compliance, instance-minutes against the theoretical minimum, over/under-provisioning, capacity changes and oscillation, time-to-relief, late or incorrect decisions, and measured warmup. The results and their critical analysis are in [`docs/report/evaluation-report.md`](docs/report/evaluation-report.md).

The demo runs the real control loop on simulated adapters in real time. It reads the load from standard input: type a number in instance units (`1.0` saturates one instance), `+`/`-`, a preset (`idle`, `comfortable`, `elevated`, `overload`, `peak`), `help` or `quit`. Each cycle prints one line with the load, CPU, capacity, decision, reason code and action, and the full decision log goes to `sim-logs/demo/`.

## Development

```bash
make test    # run the test suite
make race    # run the tests with the race detector
make lint    # run golangci-lint
make fmt     # format the code
make vet     # go vet
make check   # fmt + vet + lint + race (what CI should run)
make tf-check     # terraform fmt -check + validate (no AWS calls)
make build-linux  # linux/amd64 testapp, controller and stress binaries in bin/
```

The controller can also run locally on simulated adapters and the wall clock, as a smoke test of the real entrypoint:

```bash
cat > /tmp/controller.json <<'EOF'
{"version": 1, "mode": "sim", "profile": "demo",
 "sim": {"load": 1.5, "initial_desired": 1, "seed": 1},
 "paths": {"log_dir": "logs", "state_file": "logs/state.json"}}
EOF
go run ./cmd/controller -config /tmp/controller.json   # Ctrl-C to stop
```

Secret scanning: `pre-commit install` enables the gitleaks hook from `.pre-commit-config.yaml`; the same scan runs in GitHub Actions on every push.

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
- `internal/adapters` — real AWS adapters (`cloudwatch`, `asg`, with the shared SDK setup in `awsclient`) and simulated adapters (`fakeasg`, `mockmetrics`, `clock`, `filestate`, `memstate`, `jsonllog`). A test (`awsiam`) keeps `infra/iam/controller-policy.json` equal to the AWS operations the adapters can call.
- `internal/config` + `cmd/controller` — the versioned configuration file, resolved on a profile, and the entrypoint that wires the adapters.
- `internal/simulator` + `cmd/simulator` — the S1-S10 scenarios, the command that runs them, and the interactive live demo.
- `internal/evaluation` + `cmd/analyze` — reads decision logs back and computes the evaluation metrics, for simulator and real runs alike.
- `cmd/testapp` and `cmd/stress` — the minimal application behind the ALB and the operator tool that triggers its CPU stress.

The decision rules, thresholds, windows and failure handling are specified normatively in [`docs/spec/`](docs/spec/); the reasoning behind each choice is in [`docs/adr/`](docs/adr/).

## Repository layout

```
cmd/controller/        controller entrypoint (real or sim adapters, from the config file)
cmd/simulator/         closed-loop simulator (scenarios S1-S10) and live demo (-demo)
cmd/analyze/           evaluation metrics from JSONL decision logs
cmd/testapp/           test application (/, /health, /admin/stress)
cmd/stress/            operator tool that triggers /admin/stress
internal/core/         pure decision function and domain types
internal/ports/        port interfaces
internal/app/          control loop
internal/config/       configuration file decoding and resolution
internal/adapters/     real AWS and simulated adapters
internal/simulator/    scenario definitions and runner
internal/evaluation/   decision-log reader and evaluation metrics
infra/                 Terraform root module; infra/iam/ holds the least-privilege policy
scripts/               operator scripts (collect-evidence.sh)
docs/                  design documentation (see below)
```

## Documentation

- [`docs/context/challenge.md`](docs/context/challenge.md) — the assignment as traceable requirements.
- [`docs/context/constraints.md`](docs/context/constraints.md) — AWS Academy Learner Lab constraints that shaped the design.
- [`docs/context/taxonomy.md`](docs/context/taxonomy.md) — elasticity taxonomy classification.
- [`docs/spec/`](docs/spec/) — normative design specification.
- [`docs/adr/`](docs/adr/) — architecture decision records, one per decision, each with sources.
- [`docs/plan/`](docs/plan/) — roadmap, backlog and the requirement-to-decision-to-implementation traceability matrix.
- [`docs/verification.md`](docs/verification.md) — items to confirm empirically during the real-AWS run.
- [`docs/report/evaluation-report.md`](docs/report/evaluation-report.md) — evaluation results and critical analysis (draft; real-run section pending).

## Running against real AWS

> **Destroy per session.** The Application Load Balancer keeps billing after an AWS Academy session ends, even though EC2 instances auto-stop, and the Academy billing view lags by 8-12 hours. Every session is `apply` → run → collect evidence → `destroy`. See [`docs/context/constraints.md`](docs/context/constraints.md) and [ADR-0016](docs/adr/0016-destroy-per-session-cost-control.md).

### 1. Review the cost estimate

Estimate for a 4-hour session in `us-east-1`: the controller plus an average of 3 application instances (`t3.micro`), one ALB and its 2 public IPv4 addresses. Review it against the current [EC2](https://aws.amazon.com/ec2/pricing/on-demand/), [ELB](https://aws.amazon.com/elasticloadbalancing/pricing/), [VPC](https://aws.amazon.com/vpc/pricing/), [CloudWatch](https://aws.amazon.com/cloudwatch/pricing/) and [S3](https://aws.amazon.com/s3/pricing/) pricing before each apply.

| Item | Rate (us-east-1) | 4-hour session |
| --- | --- | --- |
| ALB hours | $0.0225 per hour | $0.09 |
| ALB LCUs (about 1 LCU at test traffic) | $0.008 per LCU-hour | $0.03 |
| EC2 `t3.micro` (1 controller + 3 app on average) | $0.0104 per instance-hour | $0.17 |
| `unlimited` surplus credits (worst case: 3 instances at 85% CPU for 1 hour) | $0.05 per vCPU-hour | $0.23 |
| Public IPv4 (2 ALB nodes + 4 instances) | $0.005 per address-hour | $0.12 |
| Detailed monitoring (3 app instances) | 7 metrics × $0.30 per metric-month, prorated hourly | $0.04 |
| `GetMetricData` (about 11 metrics per minute) | $0.01 per 1,000 metrics | $0.03 |
| S3 (about 25 MB of binaries plus logs) | storage and requests | < $0.01 |
| **Total** | | **≈ $0.70** |

A forgotten ALB alone costs about $0.54 a day; the destroy step is not optional.

### 2. Apply

```bash
make build-linux                                  # bin/testapp, bin/controller, bin/stress
cp infra/terraform.tfvars.example infra/terraform.tfvars   # set operator_ssh_cidr, key_name
terraform -chdir=infra init
terraform -chdir=infra plan -out=session.tfplan
terraform -chdir=infra apply session.tfplan
```

Required variables: `operator_ssh_cidr` (your IP as `/32`). Common ones: `region` (default `us-east-1`), `key_name` (Learner Lab: `vockey`), `instance_profile_name` (default `LabInstanceProfile`), `create_iam` (default `false`; `true` in a normal account creates the least-privilege roles), `detailed_monitoring` (default `true`).

Terraform renders the controller configuration from its own outputs and delivers it through user data, so nothing is re-pointed by hand. The controller starts under systemd and waits up to 10 minutes for the target-group metrics to appear before its first cycle.

### 3. Run the experiment

```bash
ssh ec2-user@"$(terraform -chdir=infra output -raw controller_public_ip)"
sudo journalctl -u asc-controller -f              # operational log
sudo tail -f /var/log/asc/decisions-*.jsonl       # decision log
# Stress one or all application instances from the controller host (private IPs):
stress -targets 10.40.0.12,10.40.1.34 -duration 5m
```

`/admin/stress` is reachable only from the controller host; the ALB answers `403` for `/admin/*`.

### 4. Collect the evidence, then destroy

```bash
ssh ec2-user@<controller> 'sudo systemctl stop asc-controller && sudo systemctl start asc-evidence'
scripts/collect-evidence.sh                       # syncs s3://<bucket>/logs/ to evidence/<timestamp>; fails if empty
terraform -chdir=infra destroy
```

The bucket uses `force_destroy`, so `destroy` deletes the uploaded logs too: run `collect-evidence.sh` first and check that it reports `OK`.

## Credentials

No AWS credentials are ever committed to this repository. Local runs use environment variables from the AWS Academy Learner Lab's temporary session-token credentials; the real deployment uses an EC2 instance profile. See [`docs/spec/iam.md`](docs/spec/iam.md).

## License

Course project. No license is granted; all rights reserved by the author unless stated otherwise.
