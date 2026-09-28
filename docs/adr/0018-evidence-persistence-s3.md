# ADR-0018: Persist Real-Run Evidence to S3 Before Destroy

- **Status**: Accepted
- **Related**: ADR-0008, ADR-0011, ADR-0016, `docs/spec/infrastructure.md` §3

## Context

The decision log is written to the controller instance's local disk (ADR-0008). The destroy-per-session policy (ADR-0016) deletes that instance at the end of every session, so without an extra step the only copy of the real-run evidence (REQ-DELIV-3, issue #27) is lost.

## Decision

- A systemd timer on the controller instance runs `aws s3 sync` of the log directory to `s3://<bucket>/logs/` every 60 seconds.
- `scripts/collect-evidence.sh` syncs `s3://<bucket>/logs/` to a local `evidence/` directory and exits non-zero if no decision-log file was collected.
- Running the script is a mandatory step before `terraform destroy`, at least 60 seconds after the controller stops.

## Alternatives considered

- **Upload from the controller binary**: would add `s3:PutObject` to `controller-policy.json` and a non-decision responsibility to the controller; the AWS CLI on a timer keeps the controller policy limited to what its code needs for deciding.
- **Copy over SSH before destroy**: depends on an open SSH path and a manual step that is easy to forget; S3 upload happens continuously, so a crash or a forgotten step loses at most one minute.
- **Keep the controller instance between sessions**: conflicts with ADR-0016's destroy-per-session cost control.

## Consequences

- At most the last 60 seconds of records can be missing if the instance dies abruptly.
- The bucket uses `force_destroy = true`, so evidence must be collected locally before the destroy.
- The upload permissions (`s3:PutObject`, `s3:ListBucket` on the project bucket) belong to the instance bootstrap statement, not to the controller policy (`docs/spec/iam.md` §2.1).
