# Decision Log Schema

Answers REQ-VOCAB-2 and REQ-CONSTRAINT-7: every decision must be reconstructible — time, metrics/interval considered, existing capacity/state, decision + justification, requested action, and action result.

## 1. Format

- Append-only **JSON Lines (JSONL)**, one file per calendar day (`decisions-YYYY-MM-DD.jsonl`).
- Every record is flushed to disk immediately after being written. Files are never rewritten or edited in place.
- Two record types share the same file: `cycle` (exactly one per evaluation cycle, including `MAINTAIN_CAPACITY` cycles) and `event` (asynchronous occurrences between cycles: instance state transitions, breaker state changes, blindness alerts, fetch failures, state-rebuild-from-AWS, controller startup/shutdown).
- The same schema and the same `DecisionLogger` adapter are used in simulation and real modes, so simulator output and real-run output are directly comparable.
- No AWS credentials are ever logged. When exporting evidence for submission, AWS account IDs are redacted.

## 2. `cycle` record — full schema

See `docs/spec/decision-log.schema.json` for the machine-readable JSON Schema. Summary of top-level fields:

| Field | Type | Notes |
|---|---|---|
| `schema_version` | string | e.g. `"1.0"` |
| `type` | string | `"cycle"` |
| `run_id` | string | UUID, one per controller process run |
| `cycle_id` | integer | monotonically increasing within a run |
| `mode` | string | `"sim"` \| `"real"` |
| `profile` | string | `"demo"` \| `"realistic"` |
| `ts` | string | UTC RFC3339, from the `Clock` port |
| `config_hash` | string | SHA-256 of the resolved configuration (see `docs/spec/configuration.md` §5) |
| `observation.signals[]` | array | one entry per signal: `{name, value, unit, quality, datapoint_ts, period_start, period_end}` |
| `windows.scale_out` | object | `{m, n, breaching: [cycle_id,...]}` |
| `windows.scale_in` | object | `{n, comfortable: [cycle_id,...]}` |
| `capacity` | object | `{desired, in_service, pending, draining, min, max, instances: [{id, az, state, launched_at}]}` |
| `breaker` | object | `{state, consecutive_failures}` |
| `decision` | string | exactly one of `MAINTAIN_CAPACITY` \| `INCREASE_CAPACITY` \| `REDUCE_CAPACITY` |
| `reason_code` | string | see catalog below |
| `justification.conditions[]` | array | every evaluated condition: `{name, value, threshold, met}` |
| `action` | object | `{type, params, status, skip_reason, api_request_id, duration_ms, error}` |

**Decision and action are recorded as separate fields.** A cycle can have `decision: INCREASE_CAPACITY` with `action.status: SKIPPED` (e.g., breaker open, or already at max capacity) — this distinction is required to correctly analyze "incorrect or late decisions" (REQ-PRESENT-6) separately from action-execution failures.

## 3. Reason code catalog (closed vocabulary)

```
INCREASE_CPU_HIGH
INCREASE_LATENCY_SLO
INCREASE_CAPACITY_ERRORS
INCREASE_APP_ERRORS_WITH_LOAD
REDUCE_PROJECTION_OK
MAINTAIN_STABLE
MAINTAIN_WINDOW_PENDING
MAINTAIN_PENDING_CAPACITY
MAINTAIN_SCALE_IN_BLOCKED
MAINTAIN_AT_MAX
MAINTAIN_AT_MIN
MAINTAIN_BLIND
MAINTAIN_STATE_UNKNOWN
MAINTAIN_SLO_BREACH_LOW_CPU
```

No code outside this list may be emitted; adding a new one requires updating this document and the schema together (traceability, ADR-0008).

## 4. `event` record

| Field | Type | Notes |
|---|---|---|
| `schema_version`, `type` (`"event"`), `run_id`, `ts` | as above | |
| `event_type` | string | `INSTANCE_STATE_CHANGE` \| `BREAKER_STATE_CHANGE` \| `BLIND_ALERT` \| `FETCH_FAILURE` \| `STATE_REBUILT` \| `CONTROLLER_STARTED` \| `CONTROLLER_STOPPED` |
| `details` | object | event-specific payload |

## 5. Sources

- JSON Lines specification — https://jsonlines.org/
- Google SRE Book, *Monitoring Distributed Systems* (structured, explainable logging of automated decisions) — https://sre.google/sre-book/monitoring-distributed-systems/
