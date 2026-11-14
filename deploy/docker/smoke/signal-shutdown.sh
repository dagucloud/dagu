#!/usr/bin/env bash
# Copyright (C) 2026 Yota Hamada
# SPDX-License-Identifier: GPL-3.0-or-later

set -euo pipefail

data_dir=$(mktemp -d)
container="dagu-signal-smoke-$$"
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

# Use the shipped command and Tini entrypoint with the host-owned data mount.
timeout 20 docker run -d --name "$container" \
  -e "PUID=$(id -u)" -e "PGID=$(id -g)" \
  -e DAGU_HOME=/data -e DAGU_AUTH_MODE=none \
  -e DAGU_SKIP_EXAMPLES=true -e DAGU_COORDINATOR_ENABLED=false \
  -e DAGU_SIGNAL_PROPAGATION=true \
  -v "$data_dir:/data" -v "$smoke_dir:/smoke:ro" dagu-ci:dev

timeout 20 docker exec --user "$(id -u):$(id -g)" "$container" \
  dagu validate /smoke/signal-shutdown.yaml
timeout 20 docker exec --user "$(id -u):$(id -g)" "$container" \
  dagu enqueue /smoke/signal-shutdown.yaml

for ((attempt = 0; attempt < 60; attempt++)); do
  if [ -f "$data_dir/ready" ]; then
    break
  fi
  sleep 1
done
test -f "$data_dir/ready"

# Container exit ends the PID namespace, so no runner can finish afterward.
timeout 20 docker stop --time 15 "$container"
test "$(timeout 10 docker inspect --format '{{.State.ExitCode}}' "$container")" = 0
test "$(cat "$data_dir/signal")" = TERM
test "$(cat "$data_dir/cleaned")" = done
test "$(cat "$data_dir/aborted")" = done
test "$(cat "$data_dir/exited")" = done

python3 - "$data_dir" <<'PY'
import json
import pathlib
import sys

statuses = list(pathlib.Path(sys.argv[1]).glob("data/dag-runs/**/status.jsonl"))
assert len(statuses) == 1, f"expected one run status file, found {statuses}"
status = json.loads(statuses[0].read_text().splitlines()[-1])
assert status["status"] == 3, f"expected persisted aborted status, got {status}"
for handler in ("onAbort", "onExit"):
    assert status[handler]["status"] == 4, f"expected successful {handler}, got {status}"
PY
