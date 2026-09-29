#!/bin/sh
# Collect the controller's decision logs from the project bucket into a
# local directory (docs/spec/infrastructure.md §3, ADR-0018).
#
# Run it BEFORE terraform destroy, and at least 60 seconds after stopping
# the controller (or after forcing a final sync), so the last minute of
# records has been uploaded by the evidence timer.
#
# Usage: scripts/collect-evidence.sh [BUCKET] [DEST]
#   BUCKET  project bucket; default: terraform -chdir=infra output -raw bucket
#   DEST    local directory; default: evidence/<UTC timestamp>
#
# Exits non-zero if no decision-log file was collected.
set -eu

bucket="${1:-}"
if [ -z "$bucket" ]; then
	script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
	bucket=$(terraform -chdir="$script_dir/../infra" output -raw bucket)
fi
if [ -z "$bucket" ]; then
	echo "collect-evidence: no bucket given and none in the Terraform outputs" >&2
	exit 2
fi

dest="${2:-evidence/$(date -u +%Y%m%dT%H%M%SZ)}"
mkdir -p "$dest"

echo "collect-evidence: syncing s3://$bucket/logs/ to $dest"
aws s3 sync "s3://$bucket/logs/" "$dest" --only-show-errors

count=$(find "$dest" -type f -name '*.jsonl' -size +0c | wc -l | tr -d ' ')
if [ "$count" -eq 0 ]; then
	echo "collect-evidence: FAILED, no decision-log file collected in $dest" >&2
	echo "collect-evidence: do not destroy the infrastructure yet; check the asc-evidence timer on the controller" >&2
	exit 1
fi

records=$(cat "$dest"/*.jsonl | wc -l | tr -d ' ')
echo "collect-evidence: OK, $count file(s), $records record(s) in $dest"
