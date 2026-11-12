#!/bin/bash
# Run one extra test with the real Claude Code and collect its evidence. The tests need the
# server, the proxy and the two workers of the demo, an agent worker that runs the real Claude
# Code (the image dagu-aw-agent:real) and the endpoint claude-sub.
#
# usage: run-extra.sh <test>        (the evidence goes to run/real/extras/<test>/)
#
#   extra-cli-tools    Claude Code with two DAG tools through the tool
#                      bridge, and the MCP tool dagu_read of the Dagu server
#   extra-cli-limits   max_tool_iterations and max_tool_calls of Claude Code
#   extra-cli-hostile  Claude Code is asked to read the credentials and to reach other hosts
#   extra-retry        a run fails at a gate, the script opens the gate and retries the run
#
# The variables DEMO_DATA_DIR and UNIFIED_PORT of run-demo.sh apply.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
demo=$(cd "$here/.." && pwd)
data=${DEMO_DATA_DIR:-$HOME/.local/share/dagu-unified-demo}
base=http://127.0.0.1:${UNIFIED_PORT:-8097}/api/v1
outs=$demo/run/real/extras

die() { printf 'run-extra.sh: %s\n' "$*" >&2; exit 1; }
ev() { python3 -I "$demo/evidence.py" "$@"; }

# The body goes on stdin, so no argument of a process holds it.
api() {
  local method=$1 path=$2 body=${3:-}
  if [ -n "$body" ]; then
    printf '%s' "$body" | curl -fsS -m 60 -X "$method" -H 'Content-Type: application/json' --data-binary @- "$base$path"
  else
    curl -fsS -m 60 -X "$method" "$base$path"
  fi
}

# wait_final <dag> <run id> <file> <seconds>: print the final status of the run.
wait_final() {
  local i label
  for ((i = 0; i < $4; i += 2)); do
    if api GET "/dag-runs/$1/$2" > "$3" 2>/dev/null; then
      label=$(ev field dagRunDetails.statusLabel < "$3")
      case $label in succeeded | failed | aborted | partially_succeeded | rejected) echo "$label"; return 0 ;; esac
    fi
    sleep 2
  done
  echo "timeout"
  return 1
}

# collect <dag> <run id> <folder> <start time>: the status, the logs and the outputs of
# each step, the streams of the CLI and the logs of the containers.
collect() {
  local dag=$1 id=$2 out=$3 since=$4 step stream
  mkdir -p "$out/steps"
  api GET "/dag-runs/$dag/$id" > "$out/status.json"
  ev nodes "$out/status.json" > "$out/nodes.txt"
  for step in $(python3 -I -c 'import json, sys
for node in json.load(open(sys.argv[1]))["dagRunDetails"]["nodes"]:
    print(node["step"].get("id") or node["step"]["name"])' "$out/status.json"); do
    api GET "/dag-runs/$dag/$id/steps/$step/log" > "$out/steps/$step.log.json" 2>/dev/null || true
    api GET "/dag-runs/$dag/$id/steps/$step/log?stream=stderr" > "$out/steps/$step.stderr.json" 2>/dev/null || true
    api GET "/dag-runs/$dag/$id/steps/$step/outputs" > "$out/steps/$step.outputs.json" 2>/dev/null || true
  done
  while IFS= read -r stream; do
    cp -f "$stream" "$out/$(basename "$stream")"
    ev stream "$out/$(basename "$stream")" > "$out/$(basename "$stream").summary.txt" || true
  done < <(find "$data/logs/$dag/$id" -name '*.stream.jsonl' 2>/dev/null)
  local c
  for c in egress-proxy agent-worker general-worker; do
    docker logs --since "$since" "dagu-aw-$c" > "$out/docker-$c.log" 2>&1 || true
  done
  cat "$out/nodes.txt"
}

# run_dag <dag> <folder>: start a run and wait for its end. Print the run ID.
run_dag() {
  local dag=$1 out=$2 id
  mkdir -p "$out"
  id=$(api POST "/dags/$dag/start" '{}' | ev field dagRunId) || die "cannot start $dag"
  echo "$id" > "$out/run-id.txt"
  echo "$dag run $id: $(wait_final "$dag" "$id" "$out/status.json" 600)" >&2
  echo "$id"
}

# set_gate <value>: write the registry secret demo-gate. The value comes from a file.
set_gate() {
  mkdir -p "$demo/run/real/secrets"
  (umask 077; printf '%s' "$1" > "$demo/run/real/secrets/demo-gate")
  ev secret-put "$base" demo-gate "$demo/run/real/secrets/demo-gate" || die "cannot write the secret demo-gate"
}

test=${1:-}
case $test in
  "" | *[!A-Za-z0-9_-]*) die "usage: run-extra.sh <test>: the name of a test has letters, digits, - and _ only" ;;
esac
[ -f "$here/dags/$test.yaml" ] || die "usage: run-extra.sh <test>: no file $here/dags/$test.yaml"
out=$outs/$test
rm -rf "$out"
mkdir -p "$out"
cp -f "$here/dags/$test.yaml" "$data/dags/$test.yaml"
# The DAG tools of extra-cli-tools and extra-cli-limits.
cp -f "$demo/dags/lookup-ticket.yaml" "$data/dags/lookup-ticket.yaml"
cp -f "$demo/dags/ops_notify.yaml" "$data/dags/ops_notify.yaml"
# The server reads a new DAG file in less than a few seconds.
sleep 3
since=$(date -u +%Y-%m-%dT%H:%M:%SZ)

if [ "$test" != extra-retry ]; then
  id=$(run_dag "$test" "$out") || die "cannot run $test"
  sleep 3
  collect "$test" "$id" "$out" "$since"
  exit 0
fi

# The retry test: the first attempt fails at the gate. Then the gate opens and the
# script retries the run with the REST API. A new agent worker takes the retry.
set_gate closed
id=$(run_dag "$test" "$out/attempt-1") || die "cannot run $test"
sleep 3
collect "$test" "$id" "$out/attempt-1" "$since"
set_gate open
docker inspect -f 'restart count before the retry: {{.RestartCount}}' dagu-aw-agent-worker > "$out/agent-worker-restarts.txt" 2>&1
since=$(date -u +%Y-%m-%dT%H:%M:%SZ)
api POST "/dag-runs/$test/$id/retry" "{\"dagRunId\":\"$id\"}" > "$out/retry-response.json" || die "the retry request failed"
sleep 5
echo "retry of $test $id: $(wait_final "$test" "$id" "$out/status.json" 600)"
sleep 5
docker inspect -f 'restart count after the retry: {{.RestartCount}}' dagu-aw-agent-worker >> "$out/agent-worker-restarts.txt" 2>&1
collect "$test" "$id" "$out/retry" "$since"
cat "$out/agent-worker-restarts.txt"
