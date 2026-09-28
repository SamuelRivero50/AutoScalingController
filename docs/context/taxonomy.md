# Elasticity Taxonomy Classification

The challenge requires classifying this solution using the taxonomy from Al-Dhuraibi, Y., Paraiso, F., Djarallah, N., & Merle, P. (2018). *Elasticity in Cloud Computing: State of the Art and Research Challenges.* IEEE Transactions on Services Computing, 11(2), 430-447. (Verified against the primary text, §2.2 "Elasticity Taxonomy" and Fig. 2, pp. 432-437.)

The paper defines elasticity as:

> Elasticity = (scalability + automation) [auto-scaling] + optimization  (p. 432)

and introduces three system states used later in `docs/spec/evaluation.md`: **over-provisioning** (supply > demand), **under-provisioning** (supply < demand), and **just-in-need** (a balanced state).

## Classification table

| Dimension (paper §) | Paper's categories | Chosen value | Imposed or decided |
|---|---|---|---|
| Type / direction (§2.1, Fig. 1) | Horizontal / Vertical | **Horizontal**: add/remove EC2 instances; both scale-out and scale-in directions are exercised | **Imposed** by challenge §2.1 (horizontal only) and §7.6 (must respond to both directions). The specific step-size logic for each direction is a design decision (ADR-0003). |
| Configuration (§2.2.1) | Rigid / Configurable | **Rigid**: fixed instance type (`t3.micro`), on-demand reservation, no runtime resizing of instance size | **Imposed** by the platform (AWS Academy Learner Lab does not support configurable/auction-based reservation) and by the decision to use horizontal-only scaling, which makes per-instance configuration irrelevant. |
| Scope (§2.2.2) | Infrastructure / Application-Platform (single-tier, multi-tier, application map, code-embedded) | **Infrastructure**: the controller acts on EC2 instances behind an ALB, with no knowledge of application internals | **Decided**, conditioned by the challenge (§2.1 mandates EC2 + load balancer, which is an infrastructure-level target). The paper's *sticky-sessions* caveat (p. 434) — that stateful, session-affine applications limit safe scale-in — does not apply here: the test application is explicitly stateless (documented assumption, `docs/context/glossary.md`). |
| Purpose (§2.2.3) | Performance / Cost / Capacity / Energy / Availability | **Mixed**: Performance is primary (SLO on p95 latency and error rate drives scale-out); Cost is secondary (scale-in step and instance-count minimization, motivated by the $50 credit budget); Availability is a safety constraint (never scale to 0, self-heal via ASG health checks) | **Decided**. The challenge's central question (§3) explicitly frames the trade-off as performance vs. avoiding unnecessary capacity, which maps directly onto the paper's Performance+Cost purposes. |
| Mode / Policy (§2.2.4) | Manual / Automatic → {Reactive, Proactive} | **Automatic + Reactive** (static/fixed thresholds, no prediction) | **Automatic is imposed** by challenge §7.5 (no human intervention during the experiment). **Reactive vs. proactive is a decision**: proactive (time-series forecasting, ML, queuing theory, control theory) was rejected as disproportionate to the available time and data volume; reactive threshold-based rules match the paper's own primary example (Amazon EC2's own scaling, RightScale — p. 435) and are directly explainable, satisfying challenge §7.7 (every decision must be explainable). |
| Method / Action (§2.2.5) | Horizontal (CPU/Mem/Both) / Vertical / Hybrid | **Horizontal** | **Imposed** (same as Type/direction above; the paper treats "method" as the mechanism used to realize the chosen direction). |
| Architecture (§2.2.6) | Centralized / Decentralized | **Centralized**: a single controller process is the sole decision-maker | **Decided**. A single application (min 1, max 5 instances) does not need a decentralized multi-agent architecture; centralization keeps the decision auditable and matches most academic/industrial solutions surveyed in the paper's Table 1. |
| Provider (§2.2.7) | Single / Multiple | **Single provider** (AWS), single region | **Imposed** by the AWS Academy Learner Lab constraint (single AWS account/region). |

## Notes for the design

- The **reactive, static-threshold** choice is the one dimension most directly reflected in `docs/spec/decision-policy.md` (ADR-0002, ADR-0003): the paper explicitly distinguishes *static thresholds* from *dynamic/adaptive thresholds* (p. 435); this design uses static thresholds recalibrated once via measured baselines (idle p95 latency), which is documented as a limitation, not adaptive control.
- The paper's **reaction time** and **over/under-provisioning/just-in-need** vocabulary (§2.1, p. 432) is reused directly as the evaluation vocabulary in `docs/spec/evaluation.md`, so the experimental report can answer challenge questions REQ-PRESENT-2 and REQ-PRESENT-7 in the paper's own terms.
- The **sticky-sessions** caveat (§2.2.2, p. 434) is recorded here as an explicit assumption: the test application must remain stateless for the chosen scope/architecture to be safe under scale-in. If the application were stateful, scale-in would additionally require session draining logic beyond what is designed here.
