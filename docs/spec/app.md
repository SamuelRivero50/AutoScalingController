# Test Application

Answers the "application built by us" gap decision, and REQ-DELIV-2's load-generation requirement.

## 1. Purpose

A deliberately minimal Go application exists only to give the controller something real to observe and act on during the short real-AWS integration run (`docs/spec/simulator.md` §3). It carries no business logic of its own — the instructor explicitly confirmed a "hello world"-level application is acceptable, since the challenge evaluates the controller, not the application.

## 2. Endpoints

| Endpoint | Method | Purpose |
|---|---|---|
| `/` | GET | Returns a static "hello world" response — proves the instance is serving traffic through the ALB |
| `/health` | GET | Lightweight health check for the ALB target group; does not touch any shared state or the stress-test code path, so it stays responsive even while the stress endpoint is active (see `docs/spec/lifecycle-and-failures.md` §5) |
| `/admin/stress` | POST | Accepts a duration parameter; busies that single instance's CPU internally (a duty-cycled computational loop across goroutines) for the requested duration, targeting 80-90% utilization — never external load, never 100%. At most one stress run is active per instance. Not reachable through the ALB (a listener rule returns `403` for `/admin/*`); only the controller host's security group can reach the app port directly |

## 3. Why internal stress instead of load generation

AWS Academy Learner Lab accounts risk suspension if used to generate real load against the internet-facing infrastructure of an account with limited standing. The course instructor explicitly suggested triggering CPU/memory consumption **internally** via an admin endpoint as an accepted substitute for external load testing. This design follows that guidance exactly: the `/admin/stress` endpoint is called directly on the instances' private IPs, never through external traffic generation tools.

**Stress is triggered by a separate tool, not by the controller.** `cmd/stress` is a small operator tool that runs on the controller host and calls `/admin/stress` on one or all application instances' private IPs. It is deliberately a different binary from the controller: if the controller generated its own load it would influence its own inputs, weakening the claim that no agent intervenes in the decision loop (REQ-CONSTRAINT-5). It can target every instance because stressing one instance out of N does not raise the fleet average enough (with N=2, one instance at ~85% and one idle averages ~43%, below the 70% trigger).

**Experiment limitation.** Synthetic stress does not redistribute: an instance added by scale-out starts unstressed, so the fleet-average CPU drops immediately rather than because real load spread across more instances. This is inherent to internal stress (ADR-0013) and is reported in the critical analysis (`docs/spec/evaluation.md` §3).

## 4. Properties

- **Stateless**: no database, no session storage, no sticky sessions — this is a load-bearing assumption for the taxonomy classification's Scope dimension (`docs/context/taxonomy.md`) and for the safety of scale-in (terminating any in-service instance never loses state).
- **No external dependencies**: nothing the application calls out to, so `TargetResponseTime` degradation under stress is attributable only to the stressed instance's own CPU contention — keeping the CPU-vs-latency causal story in the experiment clean.
- Built and deployed as a single static Go binary via the launch template (`docs/spec/infrastructure.md`).
