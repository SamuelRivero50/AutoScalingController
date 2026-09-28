# Evaluation and Critical Analysis

Answers REQ-DELIV-3 (experimental evidence) and REQ-DELIV-4 (critical analysis report) by specifying exactly how the raw decision log becomes the final report.

## 1. Metric computation

All metrics are computed as specified in `docs/spec/simulator.md` §5, from the JSONL decision log (`docs/spec/decision-log.md`) — never estimated by hand. A single analysis script consumes one or more log files (tagged by `run_id`, `mode`, `profile`, `config_hash`) and produces the tables/graphs used in the report.

## 2. Report structure (maps to REQ-PRESENT-1..8)

| Report section | Content | Source |
|---|---|---|
| Metric adequacy | Discussion of the chosen signal set and its documented blind spot (`MAINTAIN_SLO_BREACH_LOW_CPU`) | `docs/spec/signals.md`, S10 log |
| Reaction time | Time-to-relief distribution across scenarios/real run | `docs/spec/simulator.md` §5 |
| Transient vs. sustained | S3 scenario result, with the 2-of-3 window shown preventing a 1-cycle spike from triggering action | S3 log |
| Oscillation prevention | S5 scenario result + oscillation count across all scenarios | S5, S10 logs |
| Failure handling | S6 (circuit breaker), S7 (stuck pending), S8 (blindness) results | S6-S8 logs |
| Example of an incorrect/late decision | At least one concrete `cycle` record annotated and explained (e.g., a `MAINTAIN_SLO_BREACH_LOW_CPU` cycle, or a delayed scale-out visible in S2/S10) | S2 or S10 log, quoted verbatim |
| Cost of maintaining the SLO | Instance-minutes used vs. theoretical minimum `R`, over/under-provisioning % | `docs/spec/simulator.md` §5 |
| Comparing two controllers | Methodology: run both configurations against the same seeded scenario, compare instance-count and change-count at equal SLO compliance %, using `config_hash` to guarantee the only difference between runs is the parameter under test | `docs/spec/configuration.md` §5 |

## 3. Critical analysis expectations

The report does not merely present passing scenarios — it explicitly states:
- Which design assumptions could not be empirically validated within the AWS Academy Learner Lab's constraints (e.g., true multi-hour sustained load, larger-than-5-instance behavior).
- That the real run's load is synthetic per-instance stress: an instance added by scale-out starts unstressed, so the fleet-average CPU falls because of how the stress is applied, not because real traffic redistributed (`docs/spec/app.md` §3). The simulator models redistribution; the real run only proves integration.
- The one confirmed blind spot (SLO breach with low CPU, attributable to a bottleneck the controller cannot see).
- Any reason codes that occurred far more or less often than expected, with a hypothesis why.

This structure is designed so that a reader can verify every claim in the report against a specific, cited log record rather than a summary paragraph — directly satisfying the "explainability" assessment criterion (REQ-ASSESS).
