#!/usr/bin/env bash
# Copyright (C) 2026 Yota Hamada
# SPDX-License-Identifier: GPL-3.0-or-later

# A container stopped with a drain timeout lets its running DAG run finish
# before Dagu exits.

set -euo pipefail

data_dir=$(mktemp -d)
container="dagu-drain-smoke-$$"
smoke_dir=$(cd "$(dirname "$0")" && pwd)

cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    timeout 10 docker logs "$container" || true
  fi
  timeout 10 docker rm -f "$container" >/dev/null 2>&1 || true
  rm -rf "$data_dir"
  exit "$status"
}
trap cleanup EXIT

timeout 20 docker run -d --name "$container" \
  -e "PUID=$(id -u)" -e "PGID=$(id -g)" \
  -e DAGU_HOME=/data -e DAGU_AUTH_MODE=none \
  -e DAGU_SKIP_EXAMPLES=true -e DAGU_COORDINATOR_ENABLED=false \
  -e DAGU_SIGNAL_DRAIN_TIMEOUT=60s \
  -v "$data_dir:/data" -v "$smoke_dir:/smoke:ro" dagu-ci:dev

timeout 20 docker exec --user "$(id -u):$(id -g)" "$container" \
  dagu enqueue /smoke/drain-shutdown.yaml

for ((attempt = 0; attempt < 60; attempt++)); do
  if [ -f "$data_dir/ready" ]; then
    break
  fi
  sleep 1
done
test -f "$data_dir/ready"

# The stop timeout leaves room for the drain; Dagu exits once the run ends.
started=$(date +%s)
timeout 120 docker stop --time 90 "$container"
elapsed=$(($(date +%s) - started))
test "$elapsed" -lt 60
test "$(timeout 10 docker inspect --format '{{.State.ExitCode}}' "$container")" = 0
test "$(cat "$data_dir/finished")" = done
test ! -e "$data_dir/signal"

python3 - "$data_dir" <<'PY'
import json
import pathlib
import sys

statuses = list(pathlib.Path(sys.argv[1]).glob("data/dag-runs/**/status.jsonl"))
assert len(statuses) == 1, f"expected one run status file, found {statuses}"
status = json.loads(statuses[0].read_text().splitlines()[-1])
assert status["status"] == 4, f"expected the drained run to succeed, got {status}"
PY
