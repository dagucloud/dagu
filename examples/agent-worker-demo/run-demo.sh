#!/bin/bash
# Run the agent worker demo on one host and write the evidence to run/evidence/.
#
# usage: run-demo.sh [all|build|up|run|loop|endpoint|shots|down]     (default: all)
#
#   build  build the static dagu and the images
#   up     make the certificates, the keys and the configuration, start the server
#          container and the services of compose.yaml (the agent worker stays stopped), and
#          make the endpoint demo-upstream with the REST API. Each agent.run step of the demo
#          names this endpoint, so it must exist before the first agent run.
#   run    make the registry secret, start the run of agent-dev, run the three attacks while the
#          run waits, start the agent worker, run agent-dev-isolation (the hostile probes) and
#          hello-general, and write the evidence. Run it after "up".
#   loop   run the verify loop (the DAG agent-dev-loop) two times, with the fake claude: a
#          run that fixes the second bug in round 2 after it read the log of the verify, and a
#          run in which each verify fails and nothing is pushed. The evidence goes to
#          run/evidence/loop/. Run it after "up".
#   endpoint  run the endpoint path of Spec 094 with the fake claude, the fake model API and a fake
#          MCP server: make the endpoint demo-upstream again (with the same allow list as "up")
#          and the named MCP server vault with the REST API, run the DAG agent-dev-endpoint and
#          a DAG that the endpoint refuses, reload the models list, and check the claims. The
#          evidence goes to run/evidence/endpoint/. Run it after "up". The endpoint and the MCP
#          server stay, so the AI page shows them.
#   shots  take screenshots of the UI with Playwright into run/shots/ (after run). It needs
#          shots.cjs, which is not part of this tree: without it the command prints a message and stops.
#   down   remove the services, the networks and the images of the demo
#
# The script is idempotent. The variables below change its defaults.
#   DEMO_DATA_DIR   data directory of the server container (default ~/.local/share/dagu-unified-demo)
#   UNIFIED_IMAGE   image of the server container (default dagu-visual:unified)
#   UNIFIED_NAME    name of the server container (default dagu-unified-demo)
#   UNIFIED_PORT    host port of the UI (default 8097)
#   BUILD_UNIFIED   set to 1 to build UNIFIED_IMAGE from the repository root first
#   GOWT            a script like gowt.sh: GOWT <repo> '<command>' runs a command in a Go container
#   COMPOSE_OVERRIDE  one more compose file to merge with compose.yaml
#   SERVER_DAGU_BIN   a dagu binary to mount over the one of UNIFIED_IMAGE, to test a
#                   change of the server without a new image (the UI then has no assets)
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
run=$here/run
ev=$run/evidence
data=${DEMO_DATA_DIR:-$HOME/.local/share/dagu-unified-demo}
image=${UNIFIED_IMAGE:-dagu-visual:unified}
server=${UNIFIED_NAME:-dagu-unified-demo}
port=${UNIFIED_PORT:-8097}
base=http://127.0.0.1:$port/api/v1
label=dagu.demo=agent-worker

HOST_UID=$(id -u)
HOST_GID=$(id -g)
export HOST_UID HOST_GID
compose=(docker compose -f "$here/compose.yaml" --project-directory "$here")
# COMPOSE_OVERRIDE names one more compose file, for a local change.
if [ -n "${COMPOSE_OVERRIDE:-}" ]; then compose+=(-f "$COMPOSE_OVERRIDE"); fi

say() { printf '\n== %s\n' "$*"; }
die() { printf 'run-demo.sh: %s\n' "$*" >&2; exit 1; }

# api METHOD PATH [JSON BODY]: call the REST API and print the body. The exit code is
# nonzero for a status of 400 or more. The body goes on stdin, so no argument of a process holds it.
api() {
  local method=$1 path=$2 body=${3:-}
  if [ -n "$body" ]; then
    printf '%s' "$body" | curl -fsS -m 60 -X "$method" -H 'Content-Type: application/json' --data-binary @- "$base$path"
  else
    curl -fsS -m 60 -X "$method" "$base$path"
  fi
}

# The EXIT trap stops the background loops that a command started, and removes the files that
# the commands list in cleanup_files. A signal runs it too.
cleanup_files=()
trap 'kill $(jobs -p) 2>/dev/null; rm -f "${cleanup_files[@]+"${cleanup_files[@]}"}"' EXIT

# ---------------------------------------------------------------------------
# build
# ---------------------------------------------------------------------------

prepare() {
  say "Make the keys, the certificates and the configuration in run/"
  mkdir -p "$run/secrets" "$run/config" "$run/logs/fake-anthropic" "$ev" "$run/agent-image"
  "$here/certs.sh" "$run/certs" || die "certs.sh failed"
  (
    umask 077
    [ -s "$run/secrets/git-token" ] || openssl rand -hex 24 > "$run/secrets/git-token"
    [ -s "$run/secrets/anthropic-key" ] || echo "sk-ant-demo-$(openssl rand -hex 24)" > "$run/secrets/anthropic-key"
    [ -s "$run/secrets/mcp-token" ] || echo "mcp-demo-$(openssl rand -hex 24)" > "$run/secrets/mcp-token"
  )
  chmod 0400 "$run/secrets/git-token" "$run/secrets/anthropic-key" "$run/secrets/mcp-token"
  mkdir -p "$run/logs/fake-mcp"
  # The agent worker must not read these files: the restricted user is not the owner.
  for name in server agent-worker general-worker; do
    install -m 0400 "$here/config/$name.yaml" "$run/config/$name.yaml"
  done
}

build_dagu() {
  say "Build a static dagu"
  if [ -n "${GOWT:-}" ]; then
    "$GOWT" "$repo" 'CGO_ENABLED=0 go build -tags netgo,osusergo -o examples/agent-worker-demo/run/dagu ./cmd' || die "go build failed"
    return
  fi
  docker volume create --label "$label" dagu-aw-go-mod >/dev/null
  docker volume create --label "$label" dagu-aw-go-build >/dev/null
  docker run --rm --label "$label" -v "$repo":/src -w /src \
    -e HOME=/tmp -e GOCACHE=/tmp/gocache -e GOPATH=/tmp/gopath -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=0 \
    -v dagu-aw-go-mod:/tmp/gopath/pkg/mod -v dagu-aw-go-build:/tmp/gocache \
    golang:1.27.3 sh -c "go build -tags netgo,osusergo -o examples/agent-worker-demo/run/dagu ./cmd && chown $HOST_UID:$HOST_GID examples/agent-worker-demo/run/dagu" \
    || die "go build failed"
}

build_images() {
  say "Build the images"
  cp -f "$run/dagu" "$run/agent-image/dagu"
  local bin=$repo/conformance/spec091_agent_node/testdata/bin
  cp -f "$bin/claude" "$bin/demo_agent.py" "$bin/mcp_client.py" "$run/agent-image/"
  docker build --label "$label" -t dagu-aw-agent:demo -f "$here/Dockerfile.agent" "$run/agent-image" || die "agent image build failed"
  "${compose[@]}" build git-server || die "git server image build failed"
  if [ "${BUILD_UNIFIED:-}" = 1 ]; then
    say "Build the server image $image"
    docker build --label "$label" -t "$image" "$repo" || die "server image build failed"
  fi
}

# ---------------------------------------------------------------------------
# up
# ---------------------------------------------------------------------------

start_server() {
  say "Start the server container $server"
  docker rm -f "$server" >/dev/null 2>&1 || true
  mkdir -p "$data/dags"
  # cp -r keeps the other files in the folder.
  cp -r "$here/dags/." "$data/dags/"
  # The skills of the demo DAGs: the skills folder of the DAGs directory. Each skill is a
  # folder of the Agent Skills format (SKILL.md, references/, scripts/). The copy replaces
  # the folder, so that a removed file does not stay in a data directory of an earlier run.
  mkdir -p "$data/dags/skills"
  local skill
  for skill in "$here"/skills/*/; do
    rm -rf "$data/dags/skills/$(basename "$skill")"
  done
  cp -r "$here/skills/." "$data/dags/skills/"
  local extra=()
  if [ -n "${SERVER_DAGU_BIN:-}" ]; then extra=(-v "$SERVER_DAGU_BIN":/usr/local/bin/dagu:ro); fi
  # The container joins the internal network before it starts, so that the alias
  # dagu-coordinator resolves when the scheduler dials the coordinator.
  docker create --name "$server" --label "$label" \
    -p "127.0.0.1:$port:8080" \
    -e DAGU_AUTH_MODE=none -e PUID="$HOST_UID" -e PGID="$HOST_GID" \
    -v "$data":/var/lib/dagu \
    -v "$run/config/server.yaml":/etc/dagu/server.yaml:ro \
    -v "$run/certs/coordinator":/etc/dagu/tls:ro \
    ${extra[@]+"${extra[@]}"} \
    "$image" dagu start-all --config /etc/dagu/server.yaml >/dev/null || die "docker create failed"
  docker network connect --alias dagu-coordinator dagu-aw-internal "$server" || die "network connect failed"
  # The coordinator calls the model API and the MCP servers for the endpoints of the AI page.
  # The agent worker has no route to them: it is on the internal network only.
  docker network connect dagu-aw-egress "$server" || die "network connect to dagu-aw-egress failed"
  docker start "$server" >/dev/null || die "docker start failed"
}

wait_for() {
  # wait_for <seconds> <description> <command...>
  local seconds=$1 what=$2
  shift 2
  local i
  for ((i = 0; i < seconds; i++)); do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  die "timeout: $what"
}

general_worker_seen() {
  api GET /workers | grep '"general"'
}

# A stopped agent worker tells the coordinator that it is gone. A worker that
# exits without a word, such as a killed container, stays healthy in the
# coordinator for 30 seconds after its last heartbeat. This check makes sure
# that no agent worker of an earlier run takes the next agent run.
no_agent_worker_seen() {
  api GET /workers | python3 -I -c '
import datetime, json, sys
now = datetime.datetime.now(datetime.timezone.utc)
for worker in json.load(sys.stdin).get("workers", []):
    if worker.get("labels", {}).get("agent.provider.claude") != "true":
        continue
    seen = datetime.datetime.fromisoformat(worker["lastHeartbeatAt"].replace("Z", "+00:00"))
    if (now - seen).total_seconds() < 40:
        sys.exit(1)'
}

# The service registry in the data directory can still list the coordinator of
# an earlier server container. A dispatch goes to the next coordinator, but the
# list of the workers reports an error until that entry expires.
no_stale_coordinator() {
  [ "$(api GET /workers | python3 -I "$here/evidence.py" field errors)" = "[]" ]
}

# The DAGs of the demo that name the endpoint demo-upstream. The list "allow.dags" holds them.
# agent-dev-endpoint-denied is not here on purpose: it shows the refusal of the start check.
# Spec 093 accepts "*" in allow.dags, but "*" would also allow the DAG that must be refused.
DEMO_ENDPOINT_DAGS='"agent-dev", "agent-dev-isolation", "agent-dev-loop", "agent-dev-endpoint"'

# make_demo_endpoint [<response file>]: make the endpoint demo-upstream, the fake model API of
# the demo, with the REST API. An endpoint of the same name is deleted first, so the call is
# idempotent. The key is the file run/secrets/anthropic-key: no argument of a process holds it.
make_demo_endpoint() {
  rm -f "$run/logs/fake-anthropic/models.json"
  python3 -I "$here/evidence.py" ai-put "$base" endpoint '{
    "name": "demo-upstream", "description": "The fake model API of the demo",
    "preset": "custom", "protocol": "anthropic", "baseUrl": "http://fake-anthropic:8080", "auth": "x-api-key",
    "modelsUrl": "http://fake-anthropic:8080/v1/models", "scope": "global",
    "allow": {"workspaces": ["*"], "dags": ['"$DEMO_ENDPOINT_DAGS"']}}' \
    "$run/secrets/anthropic-key" "${1:-}" || die "cannot make the endpoint demo-upstream"
}

# The DAGs that name the endpoint claude-sub (the real Claude Code). The list does not have
# agent-endpoint-real-denied on purpose: it shows the refusal of the start check.
REAL_ENDPOINT_DAGS='"agent-dev-real", "agent-dev-loop-real", "agent-dev-endpoint-real", "agent-endpoint-real-smoke", "agent-endpoint-real-gone", "extra-cli-tools", "extra-cli-limits", "extra-cli-hostile", "extra-retry"'

# make_sub_endpoint <token file> [<response file>]: make the endpoint claude-sub (the preset
# Anthropic, the form anthropic-oauth) with the REST API. The key is the OAuth token of a
# subscription. It comes from the file, so no argument of a process and no output holds it.
# Only the managed secret store of the server holds it after this call. An endpoint of the same
# name is deleted first.
make_sub_endpoint() {
  python3 -I "$here/evidence.py" ai-put "$base" endpoint '{
    "name": "claude-sub", "description": "Subscription token, test of the real CLI",
    "preset": "anthropic", "protocol": "anthropic", "auth": "anthropic-oauth", "scope": "global",
    "allow": {"workspaces": ["*"], "dags": ['"$REAL_ENDPOINT_DAGS"']}}' \
    "$1" "${2:-}" || die "cannot make the endpoint claude-sub"
}

models_count() {
  # models_count <count>: true when the models list of the endpoint has this number of names
  [ "$(api GET /ai/endpoints/demo-upstream/models | python3 -I "$here/evidence.py" field count)" = "$1" ]
}

# require_demo_endpoint: stop when "up" did not make the endpoint. An agent run needs it.
require_demo_endpoint() {
  api GET /ai/endpoints 2>/dev/null | grep '"demo-upstream"' >/dev/null \
    || die "the endpoint demo-upstream is absent: run ./run-demo.sh up first"
}

up() {
  prepare
  say "Create the networks and the containers"
  "${compose[@]}" up --no-start || die "docker compose up --no-start failed"
  # An agent worker from an earlier run would take the next agent run before the attacks.
  "${compose[@]}" stop agent-worker >/dev/null 2>&1 || true
  start_server
  say "Start the services (the agent worker stays stopped)"
  "${compose[@]}" up -d egress-proxy fake-anthropic fake-mcp git-server general-worker || die "docker compose up failed"
  # A new git server starts from the seed folder.
  "${compose[@]}" up -d --force-recreate git-server || die "cannot restart the git server"
  wait_for 90 "the server answers on $base/health" curl -fs -m 5 "$base/health"
  wait_for 90 "the general worker is connected to the coordinator" general_worker_seen
  wait_for 180 "the coordinator of an earlier server container leaves the registry" no_stale_coordinator
  wait_for 180 "the coordinator sees no agent worker" no_agent_worker_seen
  say "Make the endpoint demo-upstream with the REST API (each agent.run step of the demo names it)"
  make_demo_endpoint
  wait_for 60 "the first read of the models list" models_count 2
}

# ---------------------------------------------------------------------------
# run
# ---------------------------------------------------------------------------

create_secret() {
  say "Make the registry secret git-token"
  # The script reads the value from the file, so no argument of a process holds it.
  python3 -I "$here/evidence.py" secret-put "$base" git-token "$run/secrets/git-token" || die "cannot make the secret git-token"
}

start_run() {
  # start_run <dag> [params]  ->  prints the run ID
  local dag=$1 params=${2:-} body='{}' id
  [ -n "$params" ] && body="{\"params\":\"$params\"}"
  id=$(api POST "/dags/$dag/start" "$body" | python3 -I "$here/evidence.py" field dagRunId) || return 1
  [ -n "$id" ] || return 1
  echo "$id"
}

run_status() {
  # run_status <dag> <run id> <file>
  api GET "/dag-runs/$1/$2" > "$3"
}

is_final() {
  case $(python3 -I "$here/evidence.py" field dagRunDetails.statusLabel < "$1") in
    succeeded | failed | aborted | partially_succeeded | rejected) return 0 ;;
  esac
  return 1
}

wait_final() {
  # wait_final <dag> <run id> <file> <seconds>
  local i
  for ((i = 0; i < $4; i++)); do
    run_status "$1" "$2" "$3" 2>/dev/null && is_final "$3" && return 0
    sleep 1
  done
  return 1
}

attacks() {
  say "Attacks: a wrong certificate must not claim the agent run"
  local name
  for name in rogue general other-ca; do
    echo "attack-$name"
    {
      echo "# docker compose --profile attacks run --rm attack-$name"
      "${compose[@]}" --profile attacks run --rm -T "attack-$name" 2>&1
      echo "# exit code $?"
    } > "$ev/attack-$name.log"
    tail -n 6 "$ev/attack-$name.log"
  done
}

collect() {
  # collect <agent-dev run id> <agent-dev-isolation run id> <general run id>
  local id=$1 iid=$2 gid=$3 step
  say "Collect the evidence in run/evidence/"
  mkdir -p "$ev/steps"
  run_status agent-dev "$id" "$ev/agent-dev.status.json"
  run_status agent-dev-isolation "$iid" "$ev/agent-dev-isolation.status.json" || true
  run_status hello-general "$gid" "$ev/hello-general.status.json" || true
  for step in clone code gate verify evidence check_paths message push; do
    api GET "/dag-runs/agent-dev/$id/steps/$step/log" > "$ev/steps/agent-dev.$step.log.json" 2>/dev/null || true
    # The tests and the fake claude write to stderr.
    api GET "/dag-runs/agent-dev/$id/steps/$step/log?stream=stderr" > "$ev/steps/agent-dev.$step.stderr.json" 2>/dev/null || true
    api GET "/dag-runs/agent-dev/$id/steps/$step/outputs" > "$ev/steps/agent-dev.$step.outputs.json" 2>/dev/null || true
  done
  for step in clone code bridge_gone evidence; do
    api GET "/dag-runs/agent-dev-isolation/$iid/steps/$step/log" > "$ev/steps/agent-dev-isolation.$step.log.json" 2>/dev/null || true
    api GET "/dag-runs/agent-dev-isolation/$iid/steps/$step/log?stream=stderr" > "$ev/steps/agent-dev-isolation.$step.stderr.json" 2>/dev/null || true
    api GET "/dag-runs/agent-dev-isolation/$iid/steps/$step/outputs" > "$ev/steps/agent-dev-isolation.$step.outputs.json" 2>/dev/null || true
  done
  api GET "/dag-runs/hello-general/$gid/steps/hostname/log" > "$ev/steps/hello-general.hostname.log.json" 2>/dev/null || true
  api GET /workers > "$ev/workers.json" 2>/dev/null || true
  python3 -I "$here/evidence.py" nodes "$ev/agent-dev.status.json" > "$ev/agent-dev.nodes.txt" || true
  python3 -I "$here/evidence.py" nodes "$ev/agent-dev-isolation.status.json" > "$ev/agent-dev-isolation.nodes.txt" || true
  python3 -I "$here/evidence.py" probes "$ev/steps/agent-dev-isolation.code.outputs.json" > "$ev/probes.txt" || true

  docker inspect dagu-aw-agent-worker > "$ev/agent-worker.inspect.json" 2>&1 || true
  docker inspect -f '{{.Config.Hostname}}' dagu-aw-agent-worker > "$ev/agent-worker.hostname.txt" 2>&1 || true
  docker inspect -f '{{.Config.Hostname}}' dagu-aw-general-worker > "$ev/general-worker.hostname.txt" 2>&1 || true
  docker inspect -f 'restart count: {{.RestartCount}}  policy: {{.HostConfig.RestartPolicy.Name}}  read-only root: {{.HostConfig.ReadonlyRootfs}}  user: {{.Config.User}}  cap_add: {{.HostConfig.CapAdd}}  cap_drop: {{.HostConfig.CapDrop}}  security: {{.HostConfig.SecurityOpt}}  networks: {{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' \
    dagu-aw-agent-worker > "$ev/agent-worker.summary.txt" 2>&1 || true
  local c
  for c in agent-worker general-worker egress-proxy fake-anthropic git-server; do
    docker logs "dagu-aw-$c" > "$ev/docker-$c.log" 2>&1 || true
  done
  docker logs "$server" > "$ev/docker-server.log" 2>&1 || true
  cp -f "$run/logs/fake-anthropic/requests.jsonl" "$ev/fake-anthropic.requests.jsonl" 2>/dev/null || true
  {
    echo "# git log --all --stat"
    docker exec dagu-aw-git-server git -C /srv/git/app.git log --all --stat --format='fuller' 2>&1
    echo
    echo "# git branch -a"
    docker exec dagu-aw-git-server git -C /srv/git/app.git branch -a 2>&1
  } > "$ev/git-log.txt" 2>&1 || true
  # A request with no token and a request with a wrong token. Both must get the status 401.
  docker exec dagu-aw-fake-anthropic python3 -c '
import base64, urllib.error, urllib.request
url = "http://git-server:8080/app.git/info/refs?service=git-upload-pack"
def status(headers):
    try:
        return urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=5).status
    except urllib.error.HTTPError as error:
        return error.code
print("no token:", status({}))
print("wrong token:", status({"Authorization": "Basic " + base64.b64encode(b"git:wrong").decode()}))
' > "$ev/git-server-unauthenticated.txt" 2>&1 || true
  local dag
  for dag in agent-dev agent-dev-isolation; do
    {
      echo "# dagu validate --config /etc/dagu/server.yaml /var/lib/dagu/dags/$dag.yaml"
      docker exec -u "$HOST_UID:$HOST_GID" "$server" dagu validate --config /etc/dagu/server.yaml "/var/lib/dagu/dags/$dag.yaml" 2>&1
      echo "# exit code $?"
    } > "$ev/validate-$dag.txt"
  done
}

run_scenario() {
  # Remove the evidence of an earlier run.
  rm -rf "$ev"
  mkdir -p "$ev/steps"
  # The agent worker of an earlier run restarted and polls. It must not take the run
  # before the attacks.
  "${compose[@]}" stop agent-worker >/dev/null 2>&1 || true
  wait_for 180 "the coordinator sees no agent worker" no_agent_worker_seen
  require_demo_endpoint
  create_secret
  : > "$run/logs/fake-anthropic/requests.jsonl"
  local branch id iid gid
  branch="agent/fix-calculator-$(date +%s)"
  say "Start the agent run (no agent worker is running)"
  id=$(start_run agent-dev "BRANCH=$branch") || die "cannot start agent-dev"
  echo "agent-dev run $id, branch $branch"
  echo "$id" > "$ev/agent-dev.run-id.txt"
  sleep 5
  run_status agent-dev "$id" "$ev/status-before-agent-worker.json" || true
  attacks
  run_status agent-dev "$id" "$ev/status-after-attacks.json" || true

  say "Start the agent worker"
  # A new container: its log and its restart count hold this run only.
  "${compose[@]}" up -d --force-recreate agent-worker || die "cannot start the agent worker"
  if wait_final agent-dev "$id" "$ev/agent-dev.status.json" 600; then
    echo "agent-dev ended: $(python3 -I "$here/evidence.py" field dagRunDetails.statusLabel < "$ev/agent-dev.status.json")"
  else
    echo "agent-dev did not end in 10 minutes"
  fi

  say "Start the isolation run (the hostile probes of the fake claude)"
  # The agent worker takes one run and exits. Docker restarts it. Make sure that it is up.
  "${compose[@]}" up -d agent-worker || die "cannot start the agent worker"
  iid=$(start_run agent-dev-isolation) || die "cannot start agent-dev-isolation"
  echo "agent-dev-isolation run $iid"
  echo "$iid" > "$ev/agent-dev-isolation.run-id.txt"
  if wait_final agent-dev-isolation "$iid" "$ev/agent-dev-isolation.status.json" 300; then
    echo "agent-dev-isolation ended: $(python3 -I "$here/evidence.py" field dagRunDetails.statusLabel < "$ev/agent-dev-isolation.status.json")"
  else
    echo "agent-dev-isolation did not end in 5 minutes"
  fi

  say "Start the general run"
  gid=$(start_run hello-general) || die "cannot start hello-general"
  echo "$gid" > "$ev/hello-general.run-id.txt"
  wait_final hello-general "$gid" "$ev/hello-general.status.json" 120 || echo "hello-general did not end in 2 minutes"
  # The worker restarts after the run. Give Docker the time to count the restart.
  sleep 5

  collect "$id" "$iid" "$gid"
  say "Checks"
  python3 -I "$here/evidence.py" check "$ev" "$run/secrets" | tee "$ev/SUMMARY.txt"
  local rc=${PIPESTATUS[0]}
  echo
  echo "Evidence: $ev"
  echo "UI: http://127.0.0.1:$port/dag-runs/agent-dev/$id"
  echo "UI: http://127.0.0.1:$port/dag-runs/agent-dev-isolation/$iid"
  return "$rc"
}

# ---------------------------------------------------------------------------
# loop
# ---------------------------------------------------------------------------

LOOP_STEPS="clone code_1 verify_1 code_2 verify_2 code_3 verify_3 evidence check_paths message push"

collect_loop_case() {
  # collect_loop_case <case> <run id> <branch>
  local name=$1 id=$2 branch=$3 dir=$ev/loop/$1 step bare=/srv/git/app.git
  mkdir -p "$dir/steps"
  run_status agent-dev-loop "$id" "$dir/status.json" || true
  echo "$id" > "$dir/run-id.txt"
  echo "$branch" > "$dir/branch.txt"
  for step in $LOOP_STEPS; do
    api GET "/dag-runs/agent-dev-loop/$id/steps/$step/log" > "$dir/steps/$step.log.json" 2>/dev/null || true
    api GET "/dag-runs/agent-dev-loop/$id/steps/$step/log?stream=stderr" > "$dir/steps/$step.stderr.json" 2>/dev/null || true
    api GET "/dag-runs/agent-dev-loop/$id/steps/$step/outputs" > "$dir/steps/$step.outputs.json" 2>/dev/null || true
  done
  python3 -I "$here/evidence.py" nodes "$dir/status.json" > "$dir/nodes.txt" || true
  {
    echo "# git branch -a"
    docker exec dagu-aw-git-server git -C "$bare" branch -a 2>&1
    echo "# git log --all --oneline"
    docker exec dagu-aw-git-server git -C "$bare" log --all --oneline 2>&1
  } > "$dir/git-log.txt" 2>&1 || true
  docker exec dagu-aw-git-server git -C "$bare" show "$branch:calc.py" > "$dir/branch-calc.py.txt" 2>&1 || true
  docker exec dagu-aw-git-server git -C "$bare" rev-list --count "main..$branch" > "$dir/branch-commits.txt" 2>&1 || true
}

run_loop_case() {
  # run_loop_case <case> <scenario of the fake claude>
  local name=$1 scenario=$2 branch id
  branch="agent/loop-$name-$(date +%s)"
  # The agent worker takes one run and exits. Docker restarts it. Make sure that it is up.
  "${compose[@]}" up -d agent-worker || die "cannot start the agent worker"
  id=$(start_run agent-dev-loop "BRANCH=$branch SCENARIO=$scenario") || die "cannot start agent-dev-loop"
  echo "agent-dev-loop run $id, case $name, scenario $scenario, branch $branch"
  if wait_final agent-dev-loop "$id" "$ev/loop/$name.status.json" 600; then
    echo "agent-dev-loop ended: $(python3 -I "$here/evidence.py" field dagRunDetails.statusLabel < "$ev/loop/$name.status.json")"
  else
    echo "agent-dev-loop did not end in 10 minutes"
  fi
  rm -f "$ev/loop/$name.status.json"
  # The worker restarts after the run. Give Docker the time to count the restart.
  sleep 5
  collect_loop_case "$name" "$id" "$branch"
  echo "UI: http://127.0.0.1:$port/dag-runs/agent-dev-loop/$id"
}

run_loop() {
  rm -rf "$ev/loop"
  mkdir -p "$ev/loop"
  require_demo_endpoint
  create_secret
  say "Run the verify loop: round 2 fixes the bug that the log of verify_1 names"
  run_loop_case fixed loop
  say "Run the verify loop: the agent never fixes the second bug, so the run fails"
  run_loop_case stuck loop_stuck
  docker inspect -f '{{.Config.Hostname}}' dagu-aw-agent-worker > "$ev/loop/agent-worker.hostname.txt" 2>&1 || true
  docker inspect -f 'restart count: {{.RestartCount}}  policy: {{.HostConfig.RestartPolicy.Name}}' \
    dagu-aw-agent-worker > "$ev/loop/agent-worker.summary.txt" 2>&1 || true
  docker logs dagu-aw-agent-worker > "$ev/loop/docker-agent-worker.log" 2>&1 || true
  cp -f "$run/logs/fake-anthropic/requests.jsonl" "$ev/loop/fake-anthropic.requests.jsonl" 2>/dev/null || true
  {
    echo "# dagu validate --config /etc/dagu/server.yaml /var/lib/dagu/dags/agent-dev-loop.yaml"
    docker exec -u "$HOST_UID:$HOST_GID" "$server" dagu validate --config /etc/dagu/server.yaml /var/lib/dagu/dags/agent-dev-loop.yaml 2>&1
    echo "# exit code $?"
  } > "$ev/loop/validate-agent-dev-loop.txt"
  say "Checks of the verify loop"
  python3 -I "$here/evidence.py" check-loop "$ev/loop" "$run/secrets" "$here/dags/agent-dev-loop.yaml" | tee "$ev/loop/SUMMARY.txt"
  return "${PIPESTATUS[0]}"
}

# ---------------------------------------------------------------------------
# endpoint
# ---------------------------------------------------------------------------

# leak_sampler <container> <needle file> <out file> <stop file>: each second, count the files of the
# container that hold the needle: the files of the work and of the configuration, and the
# environment and the arguments of each process. The needle goes to grep on stdin, so no
# argument of a process holds it. The loop ends when the stop file exists, or when the script
# that started it has exited.
leak_sampler() {
  local container=$1 needle=$2 out=$3 stop=$4 samples=0 hits=0 count parent=$$
  while [ ! -e "$stop" ] && kill -0 "$parent" 2>/dev/null; do
    if count=$(docker exec -i "$container" sh -c \
      'grep -rlF -f /dev/stdin /var/lib/dagu /tmp /etc/dagu /proc/[0-9]*/environ /proc/[0-9]*/cmdline 2>/dev/null | wc -l' < "$needle" 2>/dev/null) \
      && [ -n "$count" ]; then
      samples=$((samples + 1))
      hits=$((hits + count))
    fi
    sleep 1
  done
  printf 'samples: %d\nhits: %d\n' "$samples" "$hits" > "$out"
}

run_endpoint() {
  local dir=$ev/endpoint id did stop=$ev/endpoint-leak.stop
  rm -rf "$dir"
  mkdir -p "$dir/steps"
  : > "$run/logs/fake-anthropic/requests.jsonl"
  : > "$run/logs/fake-mcp/requests.jsonl"
  # The agent worker of an earlier run restarted and polls. It stays: this run needs it.
  "${compose[@]}" up -d agent-worker || die "cannot start the agent worker"

  say "Make the endpoint demo-upstream and the named MCP server vault with the REST API"
  make_demo_endpoint "$dir/endpoint.create.json"
  python3 -I "$here/evidence.py" ai-put "$base" mcp '{
    "name": "vault", "url": "http://fake-mcp:8080/mcp", "scope": "global",
    "allow": {"workspaces": ["*"], "dags": ["agent-dev-endpoint"]}}' \
    "$run/secrets/mcp-token" "$dir/mcp.create.json" || die "cannot make the MCP server"

  say "The models list: the first read, then a change at the upstream and a Reload"
  wait_for 60 "the first read of the models list" models_count 2
  api GET /ai/endpoints/demo-upstream/models > "$dir/models.initial.json"
  printf '["claude-demo","claude-demo-small","claude-demo-new"]' > "$run/logs/fake-anthropic/models.json"
  api POST /ai/endpoints/demo-upstream/models/reload > "$dir/models.reload.json" || die "the Reload failed"
  cat "$dir/models.reload.json"
  echo

  say "Run agent-dev-endpoint on the agent worker"
  local agent=dagu-aw-agent-worker
  docker inspect -f '{{.Config.Hostname}}' "$agent" > "$dir/agent-worker.hostname.txt" 2>&1 || true
  rm -f "$stop"
  cleanup_files+=("$stop")
  leak_sampler "$agent" "$run/secrets/anthropic-key" "$dir/leak-scan.model-key.txt" "$stop" &
  local pid_key=$!
  leak_sampler "$agent" "$run/secrets/mcp-token" "$dir/leak-scan.mcp-token.txt" "$stop" &
  local pid_token=$!
  id=$(start_run agent-dev-endpoint) || die "cannot start agent-dev-endpoint"
  echo "agent-dev-endpoint run $id"
  echo "$id" > "$dir/run-id.txt"
  wait_final agent-dev-endpoint "$id" "$dir/status.json" 300 || echo "agent-dev-endpoint did not end in 5 minutes"
  touch "$stop"
  wait "$pid_key" "$pid_token"
  rm -f "$stop"
  echo "agent-dev-endpoint ended: $(python3 -I "$here/evidence.py" field dagRunDetails.statusLabel < "$dir/status.json")"
  cp -f "$run/logs/fake-anthropic/requests.jsonl" "$dir/fake-anthropic.requests.jsonl"
  cp -f "$run/logs/fake-mcp/requests.jsonl" "$dir/fake-mcp.requests.jsonl"
  api GET "/dag-runs/agent-dev-endpoint/$id/steps/code/log" > "$dir/steps/agent-dev-endpoint.code.log.json" 2>/dev/null || true
  api GET "/dag-runs/agent-dev-endpoint/$id/steps/code/log?stream=stderr" > "$dir/steps/agent-dev-endpoint.code.stderr.json" 2>/dev/null || true
  api GET "/dag-runs/agent-dev-endpoint/$id/steps/code/outputs" > "$dir/steps/agent-dev-endpoint.code.outputs.json" 2>/dev/null || true
  python3 -I "$here/evidence.py" nodes "$dir/status.json" > "$dir/nodes.txt" || true
  docker inspect dagu-aw-agent-worker > "$dir/agent-worker.inspect.json" 2>&1 || true
  docker logs dagu-aw-agent-worker > "$dir/docker-agent-worker.log" 2>&1 || true
  docker logs "$server" 2>&1 | grep 'AI relay request' > "$dir/relay-audit.log" || true

  say "Run a DAG that is not in allow.dags of the endpoint"
  "${compose[@]}" up -d agent-worker || die "cannot start the agent worker"
  did=$(start_run agent-dev-endpoint-denied) || die "cannot start agent-dev-endpoint-denied"
  echo "agent-dev-endpoint-denied run $did"
  echo "$did" > "$dir/denied.run-id.txt"
  wait_final agent-dev-endpoint-denied "$did" "$dir/denied.status.json" 300 || echo "the denied run did not end in 5 minutes"
  python3 -I "$here/evidence.py" nodes "$dir/denied.status.json" > "$dir/denied.nodes.txt" || true
  cat "$dir/denied.nodes.txt"
  cp -f "$run/logs/fake-anthropic/requests.jsonl" "$dir/fake-anthropic.requests.after-denied.jsonl"
  {
    echo "# dagu validate --config /etc/dagu/server.yaml /var/lib/dagu/dags/agent-dev-endpoint.yaml"
    docker exec -u "$HOST_UID:$HOST_GID" "$server" dagu validate --config /etc/dagu/server.yaml /var/lib/dagu/dags/agent-dev-endpoint.yaml 2>&1
    echo "# exit code $?"
  } > "$dir/validate-agent-dev-endpoint.txt"

  say "Checks of the endpoint path"
  python3 -I "$here/evidence.py" check-endpoint "$dir" "$run/secrets" | tee "$dir/SUMMARY.txt"
  local rc=${PIPESTATUS[0]}
  echo "UI: http://127.0.0.1:$port/dag-runs/agent-dev-endpoint/$id"
  return "$rc"
}


# ---------------------------------------------------------------------------
# shots
# ---------------------------------------------------------------------------

shots() {
  if [ ! -f "$here/shots.cjs" ]; then
    echo "The file shots.cjs is not part of this tree: the screenshots step is skipped."
    return 0
  fi
  say "Take screenshots with Playwright"
  mkdir -p "$run/shots"
  docker run --rm --label "$label" --network host \
    -v "$here":/demo:ro -v "$run/shots":/shots \
    -e BASE="http://127.0.0.1:$port" \
    -e AGENT_RUN="$(cat "$ev/agent-dev.run-id.txt" 2>/dev/null || echo latest)" \
    -e ISOLATION_RUN="$(cat "$ev/agent-dev-isolation.run-id.txt" 2>/dev/null || echo latest)" \
    -e GENERAL_RUN="$(cat "$ev/hello-general.run-id.txt" 2>/dev/null || echo latest)" \
    mcr.microsoft.com/playwright:v1.59.1-noble \
    sh -c 'cd /tmp && npm install --silent playwright@1.59.1 >/dev/null 2>&1 && NODE_PATH=/tmp/node_modules node /demo/shots.cjs'
  # The container runs as root. Give the files back to the host user.
  docker run --rm --label "$label" -v "$run/shots":/shots debian:stable-slim chown -R "$HOST_UID:$HOST_GID" /shots
  echo "Screenshots: $run/shots"
}

# ---------------------------------------------------------------------------
# down
# ---------------------------------------------------------------------------

down() {
  say "Remove the services and the networks of the demo"
  docker network disconnect -f dagu-aw-internal "$server" >/dev/null 2>&1 || true
  "${compose[@]}" --profile attacks down --remove-orphans || true
  docker rmi dagu-aw-agent:demo dagu-aw-git-server:demo >/dev/null 2>&1 || true
  echo "The server container $server stays. Remove it with: docker rm -f $server"
}

# RUN_DEMO_LIB=1 skips the commands, so that another script can read the functions of this file.
if [ -n "${RUN_DEMO_LIB:-}" ]; then return 0; fi

command=${1:-all}
case $command in
  build) prepare; build_dagu; build_images ;;
  up) up ;;
  run) run_scenario ;;
  loop) run_loop ;;
  endpoint) run_endpoint ;;
  shots) shots ;;
  down) down ;;
  all) prepare; build_dagu; build_images; up; run_scenario; run_loop; run_endpoint ;;
  *) die "usage: run-demo.sh [all|build|up|run|loop|endpoint|shots|down]" ;;
esac
