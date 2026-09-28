# ADR-0008: Decision Log Schema (Append-Only JSONL)

- **Status**: Accepted
- **Related**: REQ-VOCAB-2, REQ-CONSTRAINT-7, `docs/spec/decision-log.md`

## Context

Every decision must be reconstructible: the time, the metrics/interval considered, the existing capacity/state, the decision and its justification, the requested action, and the action's result.

## Decision

Use append-only JSON Lines, one file per day, flushed after every cycle, with two record types (`cycle`, `event`) sharing one schema (`docs/spec/decision-log.schema.json`), used identically in simulation and real modes. Decision and action are recorded as separate fields so a decision can be `INCREASE_CAPACITY` while the action is `SKIPPED` (e.g., breaker open, at max capacity). Reason codes are drawn from a closed catalog.

## Alternatives considered

- **A relational database for the log**: rejected — adds an operational dependency disproportionate to a single-process controller and a short-lived AWS deployment; a flat append-only file is trivially reproducible, diffable, and requires no schema migration tooling.
- **Rewriting/updating log entries** (e.g., updating a cycle's action result after the fact): rejected — violates the append-only guarantee that makes the log tamper-evident and simple to reason about; any follow-up state change is instead logged as a new `event` record.

## Consequences

- The analysis pipeline (`docs/spec/evaluation.md`) can be a simple streaming reader over one or more JSONL files.
- Schema changes are versioned via `schema_version` and documented alongside a `reason_code` catalog change.

## Sources

- JSON Lines specification — https://jsonlines.org/
