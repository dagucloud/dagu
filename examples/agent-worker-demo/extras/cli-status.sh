#!/bin/bash
# Show how the real Claude Code reports an HTTP status of the relay of Spec 094. The test needs
# no credential and makes no model call. A stub server answers each request of the CLI with the
# status and the error body that the relay gives for a reason, and the real CLI of the image
# dagu-aw-agent:real calls the stub.
#
# usage: cli-status.sh <status> <reason> [<message> [<error type>]]
#   cli-status.sh 403 endpoint_forbidden 'the DAG "x" is not in the allow list'
# The error type is the field error.type of the body. The default is dagu_relay_error.
# The variable SHOULD_RETRY, when set, is the value of the header x-should-retry of the answer.
#
# The script prints the result event of the CLI and the number of requests that the stub got.
# The variable CLI_TIMEOUT (default 120) is the time limit of the CLI in seconds: the CLI repeats
# a request that gets a status 5xx, and waits between the tries.
# Build the image dagu-aw-agent:real from deploy/agent-worker/Dockerfile first.
set -uo pipefail

status=${1:?usage: cli-status.sh <status> <reason> [<message>]}
reason=${2:?usage: cli-status.sh <status> <reason> [<message>]}
message=${3:-a test message}
type=${4:-dagu_relay_error}
name=dagu-aw-stub-$status
network=dagu-aw-internal
label=dagu.demo=agent-worker

cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT
cleanup

# The stub answers each POST with the error body of the relay for the protocol anthropic.
docker run -d --name "$name" --label "$label" --network "$network" \
  -e STATUS="$status" -e REASON="$reason" -e MESSAGE="$message" -e TYPE="$type" -e SHOULD_RETRY="${SHOULD_RETRY:-}" python:3-slim python3 -c '
import json, os
from http.server import BaseHTTPRequestHandler, HTTPServer

class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def log_message(self, *_):
        print(self.command, self.path, flush=True)
    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length") or 0))
        body = json.dumps({"type": "error", "error": {"type": os.environ["TYPE"],
                           "message": "%s: %s" % (os.environ["REASON"], os.environ["MESSAGE"])}}).encode()
        self.send_response(int(os.environ["STATUS"]))
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        if os.environ.get("SHOULD_RETRY"):
            self.send_header("x-should-retry", os.environ["SHOULD_RETRY"])
        self.end_headers()
        self.wfile.write(body)
    do_GET = do_POST

HTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
' >/dev/null
sleep 2
docker run --rm --label "$label" --network "$network" --user 10001:10001 \
  -e HOME=/tmp -e ANTHROPIC_BASE_URL="http://$name:8080" -e ANTHROPIC_API_KEY=relay-secret-for-the-test \
  -e CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 -e DISABLE_TELEMETRY=1 -e DISABLE_AUTOUPDATER=1 \
  --entrypoint timeout dagu-aw-agent:real "${CLI_TIMEOUT:-120}" /usr/local/bin/claude -p 'Say pong.' \
  --model claude-haiku-4-5 --output-format stream-json --verbose 2>&1 \
  | python3 -I -c '
import json, sys
for line in sys.stdin:
    line = line.strip()
    if not line.startswith("{"):
        print("stderr/other:", line[:300])
        continue
    event = json.loads(line)
    if event.get("type") == "result":
        print("result:", json.dumps({k: event.get(k) for k in ("subtype", "is_error", "num_turns", "result")}))
    elif event.get("type") == "assistant" and event.get("error"):
        print("assistant error:", event["error"])
'
echo "requests that the stub got: $(docker logs "$name" 2>&1 | grep -c '^POST')"
