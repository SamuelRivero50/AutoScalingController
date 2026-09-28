# Verification Checklist

Items in the design that are stated but not yet empirically confirmed at the time of writing this documentation. Each should be checked off with evidence (a log excerpt, a measurement, or a link) as the implementation proceeds, and the corresponding spec/ADR updated if reality differs from the assumption.

- [ ] **Instance warmup duration** — assumed 180s (realistic profile) pending the dedicated 5-second-interval measurement script run against a real launch (`docs/spec/lifecycle-and-failures.md` §3).
- [ ] **AWS Academy Learner Lab: Detailed Monitoring availability** — confirm whether 1-minute EC2 CPU monitoring can be enabled in the lab; if not, confirm the datapoint-dedup safeguard behaves correctly against real 5-minute CPU data (`docs/spec/signals.md` §3).
- [ ] **AWS Academy Learner Lab: regional instance/vCPU cap** — confirm the exact cap (observed as 9 instances / 32 vCPUs) still applies at the time of the real run, since the controller instance plus up to 5 application instances must fit within it (`docs/context/constraints.md` §1).
- [ ] **IAM tag-based condition support** — confirm in practice that `autoscaling:ResourceTag` conditions are enforced as expected for `SetDesiredCapacity`, `SetInstanceHealth`, and `TerminateInstanceInAutoScalingGroup` once a policy is actually attached to a role outside the lab (documented policy only, since `LabRole` is used in the lab itself) (`docs/spec/iam.md`).
- [ ] **ALB post-session billing behavior** — reconfirm immediately before the real run that an ALB left running between AWS Academy sessions does in fact continue billing (this was verified via research once; re-confirm against the current lab terms before relying on it for the destroy-per-session policy) (`docs/context/constraints.md` §1, ADR-0016).
- [ ] **t3.micro unlimited-mode surplus cost** — confirm the actual surplus charge incurred (if any) after the real run, and record it in the evaluation report as part of the cost accounting (ADR-0014).
- [ ] **Health-check timeout tuning** — confirm empirically that the chosen ALB health-check timeout/threshold, combined with the 80-90% stress cap, does not produce a false-positive unhealthy verdict during the real run (`docs/spec/lifecycle-and-failures.md` §5).

Confirmed during design (not re-listed as open items, but noted here for traceability):

- t3 instances default to `unlimited` CPU credit mode, and `standard` mode would throttle CPU under sustained load — verified via AWS documentation (ADR-0014).
- AWS Academy Learner Lab sessions auto-stop EC2 instances after 4 hours but do not auto-stop the ALB, and billing visibility lags — verified via research (ADR-0016).
- ALBs require at least two Availability Zones — verified via AWS documentation (ADR-0015).
