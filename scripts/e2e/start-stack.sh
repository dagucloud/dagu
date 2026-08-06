#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
STATE_DIR="${DAGU_E2E_STATE_DIR:-$ROOT_DIR/ui/test-results/e2e-stack}"
BIN_PATH="${DAGU_E2E_BIN:-$ROOT_DIR/.local/bin/dagu-e2e}"
STACK_FILE="$STATE_DIR/stack.json"
LICENSE_FILE="$STATE_DIR/license.json"
PID_DIR="$STATE_DIR/pids"
SERVICE_LOG_DIR="$STATE_DIR/service-logs"

LOCAL_DIR="$STATE_DIR/local"
LOCAL_DAGS_DIR="$LOCAL_DIR/dags"
LOCAL_CONFIG_DIR="$LOCAL_DIR/config"
LOCAL_RUNTIME_DIR="$LOCAL_DIR/runtime"

REMOTE_DIR="$STATE_DIR/remote"
REMOTE_DAGS_DIR="$REMOTE_DIR/dags"
REMOTE_CONFIG_DIR="$REMOTE_DIR/config"
REMOTE_RUNTIME_DIR="$REMOTE_DIR/runtime"

LOCAL_CONFIG_FILE="$LOCAL_CONFIG_DIR/local.yaml"
WORKER_ONE_CONFIG_FILE="$LOCAL_CONFIG_DIR/worker-1.yaml"
WORKER_TWO_CONFIG_FILE="$LOCAL_CONFIG_DIR/worker-2.yaml"
REMOTE_CONFIG_FILE="$REMOTE_CONFIG_DIR/remote.yaml"

SERVER_PORT="${DAGU_E2E_SERVER_PORT:-32180}"
COORDINATOR_PORT="${DAGU_E2E_COORDINATOR_PORT:-32181}"
REMOTE_SERVER_PORT="${DAGU_E2E_REMOTE_SERVER_PORT:-32182}"

BASE_URL="http://127.0.0.1:${SERVER_PORT}"
REMOTE_API_BASE_URL="http://127.0.0.1:${REMOTE_SERVER_PORT}/api/v1"

ADMIN_USERNAME="${DAGU_E2E_ADMIN_USERNAME:-e2e-admin}"
ADMIN_PASSWORD="${DAGU_E2E_ADMIN_PASSWORD:-e2e-admin-pass}"
AUTH_TOKEN_SECRET="${DAGU_E2E_AUTH_TOKEN_SECRET:-e2e-auth-token-secret}"

WORKER_ONE_ID="worker-1"
WORKER_TWO_ID="worker-2"

QUEUE_SHARED="e2e-shared"
QUEUE_BALANCE="e2e-balance"

mode="${1:-up}"

if [[ ! -x "$BIN_PATH" ]]; then
  echo "expected Dagu E2E binary at $BIN_PATH; run 'make test-e2e' or 'make bin-e2e'" >&2
  exit 1
fi

mkdir -p "$STATE_DIR" "$PID_DIR" "$SERVICE_LOG_DIR"

license_token=""

ensure_license_env() {
  if [[ -n "$license_token" ]]; then
    return 0
  fi

  if [[ -n "${DAGU_LICENSE:-}" ]]; then
    license_token="$DAGU_LICENSE"
    return 0
  fi

  if [[ -f "$LICENSE_FILE" ]]; then
    license_token="$(cat "$LICENSE_FILE")"
    if [[ -n "$license_token" ]]; then
      return 0
    fi
  fi

  license_token="$(node "$ROOT_DIR/scripts/e2e/generate-dev-license.mjs")"
  if [[ -z "$license_token" ]]; then
    echo "warning: no license generated (DAGU_LICENSE_PRIVKEY_B64 not set)" >&2
    return 0
  fi
  printf '%s' "$license_token" >"$LICENSE_FILE"
}

pid_file_for() {
  printf '%s/%s.pid\n' "$PID_DIR" "$1"
}

config_file_for() {
  case "$1" in
    coordinator|scheduler|server)
      printf '%s\n' "$LOCAL_CONFIG_FILE"
      ;;
    worker-1)
      printf '%s\n' "$WORKER_ONE_CONFIG_FILE"
      ;;
    worker-2)
      printf '%s\n' "$WORKER_TWO_CONFIG_FILE"
      ;;
    remote-server)
      printf '%s\n' "$REMOTE_CONFIG_FILE"
      ;;
    *)
      echo "unknown service: $1" >&2
      exit 1
      ;;
  esac
}

command_name_for() {
  case "$1" in
    coordinator)
      printf 'coordinator\n'
      ;;
    scheduler)
      printf 'scheduler\n'
      ;;
    server|remote-server)
      printf 'server\n'
      ;;
    worker-1|worker-2)
      printf 'worker\n'
      ;;
    *)
      echo "unknown service: $1" >&2
      exit 1
      ;;
  esac
}

pid_is_running() {
  local pid="$1"
  kill -0 "$pid" 2>/dev/null
}

current_pid() {
  local service_name="$1"
  local pid_file
  pid_file="$(pid_file_for "$service_name")"

  if [[ ! -f "$pid_file" ]]; then
    return 1
  fi

  local pid
  pid="$(cat "$pid_file")"
  if [[ -z "$pid" ]]; then
    return 1
  fi

  if ! pid_is_running "$pid"; then
    rm -f "$pid_file"
    return 1
  fi

  printf '%s\n' "$pid"
}

stop_service() {
  local service_name="$1"
  local signal_name="${2:-TERM}"
  local pid

  if ! pid="$(current_pid "$service_name")"; then
    return 0
  fi

  kill "-${signal_name}" "$pid" 2>/dev/null || true

  local attempt
  for attempt in $(seq 1 50); do
    if ! pid_is_running "$pid"; then
      rm -f "$(pid_file_for "$service_name")"
      return 0
    fi
    sleep 0.2
  done

  kill -KILL "$pid" 2>/dev/null || true
  rm -f "$(pid_file_for "$service_name")"
}

kill_all_services() {
  local service_name
  for service_name in remote-server worker-2 worker-1 server scheduler coordinator; do
    stop_service "$service_name" TERM
  done
}

check_service_running() {
  local service_name="$1"
  local pid
  if ! pid="$(current_pid "$service_name")"; then
    echo "${service_name} is not running" >&2
    if [[ -f "$SERVICE_LOG_DIR/${service_name}.log" ]]; then
      tail -n 200 "$SERVICE_LOG_DIR/${service_name}.log" >&2 || true
    fi
    exit 1
  fi
}

login_token() {
  curl --fail --silent --show-error \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"${ADMIN_USERNAME}\",\"password\":\"${ADMIN_PASSWORD}\"}" \
    "$BASE_URL/api/v1/auth/login" | node -e '
      const fs = require("node:fs");
      const payload = JSON.parse(fs.readFileSync(0, "utf8"));
      if (!payload.token) {
        process.exit(1);
      }
      process.stdout.write(payload.token);
    '
}

wait_for_url() {
  local service_name="$1"
  local url="$2"
  local attempts="${3:-300}"
  local sleep_interval="${4:-0.2}"
  local i

  for ((i = 0; i < attempts; i++)); do
    check_service_running "$service_name"
    if curl --fail --silent --show-error "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep "$sleep_interval"
  done

  echo "timed out waiting for ${url}" >&2
  exit 1
}

wait_for_worker_registration() {
  local worker_id="$1"
  local attempts="${2:-300}"
  local sleep_interval="${3:-0.2}"
  local i

  for ((i = 0; i < attempts; i++)); do
    check_service_running server
    check_service_running "$worker_id"
    local token=""
    if token="$(login_token 2>/dev/null)" && curl --fail --silent --show-error \
      -H "Authorization: Bearer ${token}" \
      "$BASE_URL/api/v1/workers" | grep -q "\"id\":\"${worker_id}\""; then
      return 0
    fi
    sleep "$sleep_interval"
  done

  echo "timed out waiting for worker registration: ${worker_id}" >&2
  exit 1
}

start_service() {
  local service_name="$1"
  local command_name
  local config_file
  local log_file
  local pid_file

  if current_pid "$service_name" >/dev/null 2>&1; then
    return 0
  fi

  command_name="$(command_name_for "$service_name")"
  config_file="$(config_file_for "$service_name")"
  log_file="$SERVICE_LOG_DIR/${service_name}.log"
  pid_file="$(pid_file_for "$service_name")"

  ensure_license_env

  (
    cd "$ROOT_DIR"
    export DAGU_LICENSE="$license_token"
    nohup "$BIN_PATH" "$command_name" --config "$config_file" >>"$log_file" 2>&1 &
    printf '%s\n' "$!" >"$pid_file"
  )

  case "$service_name" in
    server)
      wait_for_url server "$BASE_URL/api/v1/health"
      ;;
    remote-server)
      wait_for_url remote-server "http://127.0.0.1:${REMOTE_SERVER_PORT}/api/v1/health"
      ;;
    worker-1|worker-2)
      wait_for_worker_registration "$service_name"
      ;;
  esac
}

restart_service() {
  local service_name="$1"
  local signal_name="${2:-TERM}"
  stop_service "$service_name" "$signal_name"
  start_service "$service_name"
}

write_stack_file() {
  cat >"$STACK_FILE" <<EOF
{
  "stateDir": "$STATE_DIR",
  "binPath": "$BIN_PATH",
  "local": {
    "baseURL": "$BASE_URL",
    "apiBaseURL": "$BASE_URL/api/v1",
    "dagsDir": "$LOCAL_DAGS_DIR"
  },
  "remote": {
    "baseURL": "http://127.0.0.1:${REMOTE_SERVER_PORT}",
    "apiBaseURL": "$REMOTE_API_BASE_URL",
    "dagsDir": "$REMOTE_DAGS_DIR"
  },
  "auth": {
    "adminUsername": "$ADMIN_USERNAME",
    "adminPassword": "$ADMIN_PASSWORD"
  },
  "queues": {
    "shared": "$QUEUE_SHARED",
    "balance": "$QUEUE_BALANCE"
  },
  "workers": ["$WORKER_ONE_ID", "$WORKER_TWO_ID"]
}
EOF
}

write_local_config() {
  local worker_id="$1"
  local config_file="$2"

  cat >"$config_file" <<EOF
debug: true
log_format: text
access_log_mode: none
skip_examples: true
host: 127.0.0.1
port: ${SERVER_PORT}
auth:
  mode: builtin
  builtin:
    token:
      secret: ${AUTH_TOKEN_SECRET}
      ttl: 24h
    initial_admin:
      username: ${ADMIN_USERNAME}
      password: ${ADMIN_PASSWORD}
permissions:
  write_dags: true
  run_dags: true
paths:
  dags_dir: ${LOCAL_DAGS_DIR}
  log_dir: ${LOCAL_RUNTIME_DIR}/logs
  data_dir: ${LOCAL_RUNTIME_DIR}/data
  suspend_flags_dir: ${LOCAL_RUNTIME_DIR}/suspend-flags
  admin_logs_dir: ${LOCAL_RUNTIME_DIR}/admin-logs
  event_store_dir: ${LOCAL_RUNTIME_DIR}/admin-logs/events
  users_dir: ${LOCAL_RUNTIME_DIR}/users
  api_keys_dir: ${LOCAL_RUNTIME_DIR}/api-keys
  webhooks_dir: ${LOCAL_RUNTIME_DIR}/webhooks
  sessions_dir: ${LOCAL_RUNTIME_DIR}/sessions
  contexts_dir: ${LOCAL_RUNTIME_DIR}/contexts
  remote_nodes_dir: ${LOCAL_RUNTIME_DIR}/remote-nodes
  workspaces_dir: ${LOCAL_RUNTIME_DIR}/workspaces
coordinator:
  enabled: true
  host: 127.0.0.1
  advertise: 127.0.0.1
  port: ${COORDINATOR_PORT}
  health_port: 0
worker:
  id: ${worker_id}
  max_active_runs: 1
  health_port: 0
  labels:
    role: e2e
  coordinators:
    - 127.0.0.1:${COORDINATOR_PORT}
scheduler:
  port: 0
  lock_retry_interval: 250ms
  zombie_detection_interval: 500ms
proc:
  heartbeat_interval: 500ms
  stale_threshold: 3s
queues:
  enabled: true
  config:
    - name: ${QUEUE_SHARED}
      max_active_runs: 1
    - name: ${QUEUE_BALANCE}
      max_active_runs: 2
EOF
}

write_remote_config() {
  cat >"$REMOTE_CONFIG_FILE" <<EOF
debug: true
log_format: text
access_log_mode: none
skip_examples: true
host: 127.0.0.1
port: ${REMOTE_SERVER_PORT}
auth:
  mode: none
permissions:
  write_dags: true
  run_dags: true
paths:
  dags_dir: ${REMOTE_DAGS_DIR}
  log_dir: ${REMOTE_RUNTIME_DIR}/logs
  data_dir: ${REMOTE_RUNTIME_DIR}/data
  suspend_flags_dir: ${REMOTE_RUNTIME_DIR}/suspend-flags
  admin_logs_dir: ${REMOTE_RUNTIME_DIR}/admin-logs
  event_store_dir: ${REMOTE_RUNTIME_DIR}/admin-logs/events
  users_dir: ${REMOTE_RUNTIME_DIR}/users
  api_keys_dir: ${REMOTE_RUNTIME_DIR}/api-keys
  webhooks_dir: ${REMOTE_RUNTIME_DIR}/webhooks
  sessions_dir: ${REMOTE_RUNTIME_DIR}/sessions
  contexts_dir: ${REMOTE_RUNTIME_DIR}/contexts
  remote_nodes_dir: ${REMOTE_RUNTIME_DIR}/remote-nodes
  workspaces_dir: ${REMOTE_RUNTIME_DIR}/workspaces
coordinator:
  enabled: false
scheduler:
  port: 0
queues:
  enabled: true
EOF
}

prepare_stack() {
  rm -rf "$STATE_DIR"
  mkdir -p \
    "$PID_DIR" \
    "$SERVICE_LOG_DIR" \
    "$LOCAL_DAGS_DIR" \
    "$LOCAL_CONFIG_DIR" \
    "$LOCAL_RUNTIME_DIR" \
    "$REMOTE_DAGS_DIR" \
    "$REMOTE_CONFIG_DIR" \
    "$REMOTE_RUNTIME_DIR"

  cp "$ROOT_DIR/ui/e2e/fixtures/dags/e2e-distributed-queue.yaml" "$LOCAL_DAGS_DIR/"

  ensure_license_env
  write_local_config "$WORKER_ONE_ID" "$LOCAL_CONFIG_FILE"
  write_local_config "$WORKER_ONE_ID" "$WORKER_ONE_CONFIG_FILE"
  write_local_config "$WORKER_TWO_ID" "$WORKER_TWO_CONFIG_FILE"
  write_remote_config
  write_stack_file
}

run_stack() {
  prepare_stack

  trap 'kill_all_services' EXIT INT TERM

  start_service coordinator
  start_service server
  start_service worker-1
  start_service worker-2
  start_service scheduler
  start_service remote-server

  echo "Dagu e2e stack is ready at $BASE_URL" >&2

  while true; do
    sleep 3600 &
    wait $! || true
  done
}

case "$mode" in
  up)
    run_stack
    ;;
  start-service)
    if [[ $# -lt 2 ]]; then
      echo "usage: $0 start-service <service-name>" >&2
      exit 1
    fi
    start_service "$2"
    ;;
  stop-service)
    if [[ $# -lt 2 ]]; then
      echo "usage: $0 stop-service <service-name> [TERM|KILL]" >&2
      exit 1
    fi
    stop_service "$2" "${3:-TERM}"
    ;;
  restart-service)
    if [[ $# -lt 2 ]]; then
      echo "usage: $0 restart-service <service-name> [TERM|KILL]" >&2
      exit 1
    fi
    restart_service "$2" "${3:-TERM}"
    ;;
  *)
    echo "unknown mode: $mode" >&2
    exit 1
    ;;
esac
