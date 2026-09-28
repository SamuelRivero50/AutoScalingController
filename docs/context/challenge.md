# Challenge Statement (Source of Truth)

This document restates the assignment ("Build Your Own Auto-Scaling Controller", Cloud Computing course, individual challenge) in a structured, traceable form. Every requirement below is tagged with a `REQ-*` ID that is referenced from the specs, ADRs, and the traceability matrix in `docs/plan/traceability.md`.

## 1. Scope of scaling (REQ-SCALE)

- **REQ-SCALE-1**: The controller performs **horizontal** scaling only (add/remove EC2 instances). Vertical scaling is out of scope.
- **REQ-SCALE-2**: The feedback loop is: **observe → analyze → decide → act → observe** (repeats indefinitely).

## 2. Central question (REQ-GOAL)

- **REQ-GOAL-1**: The controller must answer: *"How can a controller maintain the expected application performance under changing demand while avoiding unnecessary capacity?"*

## 3. Decision logic ownership (REQ-LOGIC)

- **REQ-LOGIC-1**: The controller must decide between exactly three actions: `MAINTAIN_CAPACITY`, `INCREASE_CAPACITY`, `REDUCE_CAPACITY`.
- **REQ-LOGIC-2**: The decision logic (when/how much to scale) must be entirely designed by the student. No AWS-managed dynamic scaling policy (e.g., ASG target tracking, step scaling) may make the scaling *decision*.
- **REQ-LOGIC-3**: AWS-managed mechanisms **may** be used to *provision, configure, register, or remove* instances once the decision has already been made outside of them. (This is the basis for using an Auto Scaling Group purely as an actuator — see ADR-0009.)

## 4. Taxonomy classification (REQ-TAXO)

- **REQ-TAXO-1**: Before implementation, the solution must be classified using the elasticity taxonomy from Al-Dhuraibi, Paraiso, Djarallah & Merle, *"Elasticity in Cloud Computing: State of the Art and Research Challenges,"* IEEE TSC 11(2), 2018 — across the dimensions: type/direction, resources scaled, scope, purpose, mode, decision-making method, controller architecture, and cloud-provider scope.
- **REQ-TAXO-2**: For each dimension, the design must state whether the value is **imposed** by the challenge or **chosen** by the student.
- See `docs/context/taxonomy.md` for the full classification.

## 5. Objectives (REQ-OBJ)

- **REQ-OBJ-1** (general): Design, implement, and evaluate a working, student-authored auto-scaling controller for a real AWS deployment.
- **REQ-OBJ-2**: Justify metric selection and decision thresholds with evidence.
- **REQ-OBJ-3**: Demonstrate correct behavior under increasing load.
- **REQ-OBJ-4**: Demonstrate correct behavior under decreasing load.
- **REQ-OBJ-5**: Demonstrate resilience to failures and incomplete/missing information.
- **REQ-OBJ-6**: Produce evidence (logs, experiments) that supports a critical evaluation of the controller's behavior.

## 6. Scope and constraints (REQ-CONSTRAINT)

1. **REQ-CONSTRAINT-1**: EC2 instances must run behind a load balancer.
2. **REQ-CONSTRAINT-2**: Capacity bounds are **min 1, max 5** instances.
3. **REQ-CONSTRAINT-3**: The controller must draw substantial information from CloudWatch.
4. **REQ-CONSTRAINT-4**: No AWS-managed dynamic scaling policy may decide *when* or *how much* to scale (restates REQ-LOGIC-2).
5. **REQ-CONSTRAINT-5**: No human intervention is allowed during the experiment run.
6. **REQ-CONSTRAINT-6**: The controller must respond to both increases and decreases in demand.
7. **REQ-CONSTRAINT-7**: Every decision must be recorded and explainable from the observed state at that time.
8. **REQ-CONSTRAINT-8**: The controller must handle failures and incomplete information without jeopardizing availability.
9. **REQ-CONSTRAINT-9**: The controller must operate under least-privilege permissions.

## 7. Engineering decision questions (REQ-Q)

The assignment poses 12 questions that the design must answer explicitly. Each is mapped to the design artifact that answers it:

| # | Question (paraphrased) | Answered in |
|---|---|---|
| REQ-Q-1 | What counts as "adequate capacity"? | `docs/spec/decision-policy.md`, ADR-0003 |
| REQ-Q-2 | What metrics represent demand/state? | `docs/spec/signals.md`, ADR-0001 |
| REQ-Q-3 | How and how often are metrics obtained? | `docs/spec/signals.md`, ADR-0001, ADR-0002 |
| REQ-Q-4 | What is the observation interval? | `docs/spec/configuration.md`, ADR-0002 |
| REQ-Q-5 | Under what conditions to increase/reduce/maintain? | `docs/spec/decision-policy.md`, ADR-0003 |
| REQ-Q-6 | How many instances per decision (step size)? | `docs/spec/decision-policy.md`, ADR-0003 |
| REQ-Q-7 | Where does the controller run, and how is state retained? | `docs/spec/architecture.md`, `docs/spec/infrastructure.md`, ADR-0009, ADR-0011 |
| REQ-Q-8 | How is instance-availability time accounted for? | `docs/spec/lifecycle-and-failures.md`, ADR-0004 |
| REQ-Q-9 | How is oscillation avoided? | `docs/spec/decision-policy.md`, ADR-0002, ADR-0003, ADR-0004 |
| REQ-Q-10 | How are missing/delayed/anomalous metrics handled? | `docs/spec/lifecycle-and-failures.md`, ADR-0005 |
| REQ-Q-11 | How are AWS operation failures handled? | `docs/spec/lifecycle-and-failures.md`, ADR-0004, ADR-0009 |
| REQ-Q-12 | How is "safe to reduce" determined? | `docs/spec/decision-policy.md`, ADR-0003, ADR-0005 |

## 8. Decision vocabulary (REQ-VOCAB)

- **REQ-VOCAB-1**: The three decision literals must be used verbatim: `MAINTAIN_CAPACITY`, `INCREASE_CAPACITY`, `REDUCE_CAPACITY`.
- **REQ-VOCAB-2**: Each decision record must allow reconstructing: the time of the decision, the metrics and interval considered, the existing capacity and state, the decision and its justification, the requested action, and the result of that action. This defines the decision-log schema in `docs/spec/decision-log.md`.

## 9. Deliverables (REQ-DELIV)

- **REQ-DELIV-1** — Solution Design document (this `docs/` tree).
- **REQ-DELIV-2** — Reproducible implementation: source code, infrastructure as code, deployment/run instructions, a load-generation tool or procedure, and the decision-logging mechanism. **Credentials must never be stored in the repository** (see `docs/spec/iam.md`).
- **REQ-DELIV-3** — Experimental evidence (see `docs/spec/evaluation.md`).
- **REQ-DELIV-4** — Critical analysis report.
- **REQ-DELIV-5** — Live demonstration.

## 10. Assessment criteria (REQ-ASSESS)

Evaluated on: coherence of the design, stability of the controller, security (least privilege, no leaked credentials), and explainability of every decision — not merely whether instances are launched or terminated.

## 11. Final-presentation questions (REQ-PRESENT)

The design and evaluation must be able to answer, with evidence from the experiments:

1. **REQ-PRESENT-1**: Is the chosen metric set adequate? What blind spots exist?
2. **REQ-PRESENT-2**: How long does it take from overload to available extra capacity?
3. **REQ-PRESENT-3**: How is a transient demand spike distinguished from sustained demand?
4. **REQ-PRESENT-4**: How is oscillation (flapping) prevented?
5. **REQ-PRESENT-5**: How are failures handled?
6. **REQ-PRESENT-6**: Give an example of an incorrect or late decision the controller made.
7. **REQ-PRESENT-7**: What is the resource cost of maintaining the SLO?
8. **REQ-PRESENT-8**: How would two controllers be compared on instance-count and change-count at equal SLO?

Each is answered quantitatively in `docs/spec/evaluation.md` using the metrics computed from the decision log.

## References

- [1] Herbst, N. R., Kounev, S., & Reussner, R. (2013). *Elasticity in Cloud Computing: What It Is, and What It Is Not.* ICAC 2013.
- [2] AWS. *Amazon CloudWatch metrics for your ELB / EC2 resources* — https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/
- [3] AWS. *Dynamic scaling for Amazon EC2 Auto Scaling* — https://docs.aws.amazon.com/autoscaling/ec2/userguide/as-scale-based-on-demand.html
- [4] Al-Dhuraibi, Y., Paraiso, F., Djarallah, N., & Merle, P. (2018). *Elasticity in Cloud Computing: State of the Art and Research Challenges.* IEEE Transactions on Services Computing, 11(2), 430-447.
