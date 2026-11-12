# Agent worker demo

This demo runs the whole chain of [Spec 092](../../specs/092-agent-workers.md) on one host.
All parts are real except the AI. The CLI is a fake `claude`, and the model API is a fake.
The fake CLI does real work and tries hostile actions. The demo records what Dagu allowed.

Each `agent.run` step of the demo names an AI endpoint and a model in the block `endpoint`
([Spec 094](../../specs/094-ai-gateway.md)). An endpoint holds the address and the key of a model API.
The REST API of the server makes it. A DAG with an `agent.run` step and no endpoint name or no model fails at load.
The demo uses the endpoint `demo-upstream`. The script makes the endpoint before the first agent run.

## What the demo proves

Two DAGs run on the agent worker, one after the other. The DAG `agent-dev` does the work. The DAG
`agent-dev-isolation` tries the hostile actions. The DAG `agent-dev` does these actions:

1. A trusted step clones a repository from a git server. The step gets the token of the
   server as a secret.
2. The fake `claude` runs the tests that fail, asks the fake model API for a fix, and edits `calc.py`.
3. It calls the tools of the step. The tool bridge of Dagu serves the DAGs `lookup-ticket` and
   `ops_notify`, and the MCP endpoint of the Dagu server serves the tool `dagu_read`. The
   DAG fixes the param `CUSTOMER_ID` of `lookup-ticket`. The agent gives the param `QUESTION`.
4. It reads the skills `project-rules` and `commit-message` that the DAG names with `skills`. Dagu mounts
   each skill folder as it is for the attempt, under `.claude/skills/` in the agent directory. The agent
   worker gets the folders in the workspace bundle of the run. The fake CLI reads `SKILL.md` of each skill
   and the file `references/format.md` of `commit-message`. It lists both in its summary.
5. A gate, a test step as the restricted user, an evidence step and a path check follow. The
   DAG has an `agent.run` step, so a step with `run` is untrusted by default. The test step has
   no key `untrusted`, and it runs as the restricted user. The gate, the evidence step and the
   path check have `untrusted: false`.
6. A trusted step writes a commit message with a model call, and pushes a branch with the token.

The DAG `agent-dev-isolation` has a trusted clone that holds the secret `GIT_TOKEN`, an `agent.run`
step `code` with one DAG tool (so that a tool bridge exists), and two more steps. The fake `claude`
does these actions in the step `code`:

1. It tries 17 hostile actions and reports the result of each one in the typed output `probes`.
   Two of them are against the tool bridge: a request with no secret, and a search for the
   secret in the environment. One tries to write in the skills directory.
2. It prints the secret of the tool bridge to its stderr, to show that Dagu masks it.
3. It leaves one process behind, to show that Dagu stops it.

An untrusted step `bridge_gone` checks, as the restricted user, that the bridge of the attempt
is closed and that no MCP config of the attempt stays. A trusted step `evidence` fails when a process
of the restricted user, a hook or `core.fsmonitor` is left.

The two runs prove these claims. The script `evidence.py` checks each one and prints PASS or FAIL.
The command `check` prints 30 claims: 24 for `agent-dev`, and 6 for `agent-dev-isolation`.

- The run goes to the agent worker. A general worker never takes it.
- A wrong certificate cannot claim the run. The demo tries three: a CN in no pool, a pool that
  does not allow the label, and a certificate from another CA.
- The agent read the skills that the DAG names, including the file `references/format.md` of a skill, and it
  could not write the skills directory.
- The agent never sees the git token, the real API key, the certificate, or the configuration.
- The agent reaches two hosts: the proxy and the Dagu server. The proxy refuses `example.com` with the
  status 403. The Dagu server is open to the agent, because the tool bridge and the MCP server need it.
  The demo server runs with `DAGU_AUTH_MODE=none`, so the agent can call its REST API with no
  credential. It can list the DAGs and the metadata of the secrets, but it cannot read a secret value.
  In a real deployment, turn on authentication and give the MCP entry a header with a token.
- The agent cannot change `.git`, and Dagu stops the process that the agent leaves behind.
- The agent worker exits after one run. Docker starts a new one.
- The two DAG tools run as child runs through the tool bridge. The child run of
  `lookup-ticket` gets the `CUSTOMER_ID` of the DAG, not the value of the agent, and the
  `QUESTION` of the agent. The MCP tool `dagu_read` of the Dagu server answers the agent.
- The bridge refuses a request with no secret. The secret is not in the environment of the
  agent, Dagu masks it in the stderr of the step, and the next untrusted step finds the bridge
  closed and no MCP config to read. The CLI itself reads the secret in its MCP config: the
  config belongs to the restricted user and has the mode 0400.
- A normal DAG with a `worker_selector` runs on the general worker.

## What is fake

| Part | In the demo | In production |
| --- | --- | --- |
| The CLI agent | `conformance/spec091_agent_node/testdata/bin/claude` with `FAKE_CLAUDE_SCENARIO=demo` or `isolation` | Claude Code |
| The model API | `fake-anthropic/server.py`, behind the endpoint `demo-upstream` | The Anthropic API, behind an endpoint |
| The model of the message step | `dagu-mock-llm` on `172.17.0.1:8099` | A real model |
| The git server | `git-server/server.py`: `git http-backend` with a token | Your git server |

The git server, the proxy, the coordinator, the workers, the certificates and the restricted
user are real.

Note for a later change: the step `message` of `agent-dev` needs the container `dagu-mock-llm`
on the host at `172.17.0.1:8099`. The demo does not start it. A `mock-llm` service in
`compose.yaml` can replace it. The file `examples/workspace-demo/mock-llm/server.py` is a
candidate: it takes the port as its first argument. The service joins the network `egress`.
The proxy then allows `mock-llm:8080` instead of `172.17.0.1:8099`, and the step uses the matching `base_url`.

## Topology

```text
host 127.0.0.1:8097 --> dagu-unified-demo (UI, scheduler, coordinator, mutual TLS)
                              |   alias dagu-coordinator
        network dagu-aw-internal (internal: true, no route out)
          |            |               |
   general-worker  agent-worker   egress-proxy ---- network dagu-aw-egress ---- fake-anthropic
                                                                          \---- git-server
                                                  \--- 172.17.0.1:8099 (mock LLM)
```

The server container is also on `dagu-aw-egress`. The agent worker has no route to `fake-anthropic`: the CLI
calls a relay on the loopback of the worker. The coordinator sends each model request to the endpoint
`demo-upstream`. The proxy carries no model request of an `agent.run` step.

All containers of the demo have the label `dagu.demo=agent-worker`. Their names start with `dagu-aw-`.

## Run the demo

You need Docker with Compose, `python3`, `curl` and `openssl` on the host.

The server container uses the image `dagu-visual:unified`. Build it from the repository root
after you merge the agent worker code. The image must have the code of the agent worker.

```sh
docker build -t dagu-visual:unified .
./examples/agent-worker-demo/run-demo.sh
```

The script does these actions:

1. It makes the keys, the certificates and the configuration in `run/`.
2. It builds a static `dagu` and the images `dagu-aw-agent:demo` and `dagu-aw-git-server:demo`.
3. It replaces the container `dagu-unified-demo`. The port is `127.0.0.1:8097`.
   The script keeps the files in the data directory and adds the demo DAGs to `dags/`.
4. It starts the services of `compose.yaml`. The agent worker stays stopped.
5. It makes the endpoint `demo-upstream` with the REST API. The key comes from `run/secrets/anthropic-key`.
   The list `allow.dags` of the endpoint has `agent-dev`, `agent-dev-isolation`, `agent-dev-loop` and
   `agent-dev-endpoint`. It does not have `agent-dev-endpoint-denied`, because this DAG shows a refusal.
6. It makes the registry secret `git-token` with the REST API.
7. It starts the run of `agent-dev`. The run waits, because no agent worker is on.
8. It runs the three attack services. Then it checks that no worker claimed the run.
9. It starts the agent worker and waits for the end of the run. Then it runs `agent-dev-isolation` on the
   agent worker, and `hello-general`.
10. It writes the evidence to `run/evidence/` and prints the claims.

The commands `run` and `loop` stop at once when the endpoint `demo-upstream` is absent. Run `up` first.

The script is idempotent. Run it again to start a new run. Each run pushes a new branch.
The git server is in memory and starts with a new repository each time.

Run one part of the script with an argument: `build`, `up`, `run`, `loop`, `endpoint` or `down`.
These variables change the defaults: `DEMO_DATA_DIR`, `UNIFIED_IMAGE`, `UNIFIED_NAME`,
`UNIFIED_PORT`, `BUILD_UNIFIED=1`, `GOWT`, `COMPOSE_OVERRIDE` and `SERVER_DAGU_BIN`. See the head of
the script. `SERVER_DAGU_BIN` mounts a `dagu` binary over the binary of the server image. Use it to test a
change of the server without a new image. The UI then has no assets.

## Evidence

The folder `run/evidence/` holds these files:

| File | Content |
| --- | --- |
| `SUMMARY.txt` | PASS or FAIL for each claim. |
| `agent-dev.status.json`, `agent-dev.nodes.txt` | The run status from the REST API and the status of each step. |
| `agent-dev-isolation.status.json`, `agent-dev-isolation.nodes.txt` | The same for the run of `agent-dev-isolation`. |
| `probes.txt` | The result of each hostile action of `agent-dev-isolation`: `allowed` or `denied:<reason>`. |
| `steps/` | The stdout log, the stderr log and the outputs of each step. The names start with the DAG name. |
| `status-before-agent-worker.json`, `status-after-attacks.json` | The run, while the attacks run. It has no worker. |
| `attack-rogue.log`, `attack-general.log`, `attack-other-ca.log` | The output of the three attack services. |
| `docker-*.log` | The logs of the containers: the workers, the proxy, the git server and the server. |
| `agent-worker.summary.txt`, `agent-worker.inspect.json` | The restart count and the security settings of the agent worker. |
| `fake-anthropic.requests.jsonl` | The requests of the fake model API. It stores the SHA-256 prefix of the key, never the key. |
| `git-log.txt` | `git log --all` of the bare repository. |
| `validate-agent-dev.txt`, `validate-agent-dev-isolation.txt` | The output of `dagu validate` for the two DAGs. |

## The attack services

The compose profile `attacks` has three one-shot workers. Each one starts with `--agent`.
Each one must fail. A limit of 40 seconds stops a worker that the coordinator does not refuse.

| Service | Certificate | Expected result |
| --- | --- | --- |
| `attack-rogue` | CN `rogue`, from the demo CA | `PermissionDenied`: the identity is in no pool. |
| `attack-general` | CN `general-worker` | `PermissionDenied`: the pool `general` does not allow `agent.provider.claude`. |
| `attack-other-ca` | CN `agent-claude`, from another CA | The TLS handshake fails. |

Run one by hand:

```sh
cd examples/agent-worker-demo
HOST_UID=$(id -u) HOST_GID=$(id -g) docker compose --profile attacks run --rm attack-rogue
```

## The fake claude demo scenario

The script `claude` runs `demo_agent.py` when `FAKE_CLAUDE_SCENARIO` is `demo` or `isolation`. The DAG sets
this variable in its `env` block. The scenario `demo` does the work of `agent-dev`. The scenario
`isolation` does no work: it runs the hostile probes, prints the secret of the tool bridge to its
stderr, leaves a process, and prints a typed result with `probes` and `bridge_url`. The other
scenarios of the script are for the conformance tests of Spec 091. The demo does not change them.

The helper prints `stream-json` events as the real CLI does. It uses the names `Bash`, `Read`,
and `Edit`. The last event has the typed result in `structured_output`.

## The demo skills

The folder `skills/` holds two skills of the Agent Skills format (Spec 096). The script `run-demo.sh` copies
them to the skills directory of the DAGs directory.

- `project-rules` is one small file. It joins the code rules and the review checklist of the project.
  The body is short, so a reference file would add nothing.
- `commit-message` shows progressive disclosure. `SKILL.md` is short and names the file
  `references/format.md` and the script `scripts/check-message.sh`. The file holds the format of the commit
  message. The script checks a subject line. Dagu never runs it.

On `agent.run`, Dagu mounts each folder as it is, with all its files, under `.claude/skills/<name>/`. The
claims show that the fake CLI read `references/format.md` from the mounted folder. On `chat.completion`, the
model calls the tool `read_skill_file` for the same file. The demo has no `chat.completion` step with
skills, because its model is a mock without tool calls. The Go tests and the conformance tests cover that tool.

## The verify loop with the fake claude

The DAG `agent-dev-loop` has three rounds of `code_<n>` and `verify_<n>` in one DAG. See [the
verify loop of Spec 092](../../specs/092-agent-workers.md#a-verify-loop). The seed project has
two bugs. The tests show `add`. Only `scripts/verify.sh` shows `divide`. The script `claude`
runs `demo_agent.py` with `FAKE_CLAUDE_SCENARIO=loop` or `loop_stuck`. The DAG sets this variable
from its parameter `SCENARIO`.

Run `./run-demo.sh loop` after `up`. It starts two runs on the agent worker, one after the
other, and writes the evidence to `run/evidence/loop/`:

| Case | Scenario | What happens |
| --- | --- | --- |
| `fixed` | `loop` | Round 1 fixes `add`. `verify_1` fails and writes `verify_1.log` in the work directory. Round 2 reads the log with its `Read` tool and fixes `divide`. `verify_2` passes. Dagu skips `code_3` and `verify_3`. The steps after the loop run, and the run pushes one branch. The run ends `partially_succeeded`. |
| `stuck` | `loop_stuck` | The agent reads the log and changes nothing. The three verify steps fail. The run fails, the steps after the loop are `aborted`, and the git server has no branch of this run. |

The DAG has `max_active_runs: 1`. The whole loop is one run on one agent worker, with no sub
run. The steps `verify_<n>` have no key `untrusted`. They run as the restricted user because
the DAG has an `agent.run` step. The script `evidence.py check-loop` checks 14 claims. On
2026-11-03 all 14 held.

## The endpoint path with the fake claude

[Spec 093](../../specs/093-ai-endpoints.md) adds the AI page, and [Spec 094](../../specs/094-ai-gateway.md)
adds the key `endpoint` of `agent.run`.
A step with an endpoint needs no model key in the worker. The CLI calls a relay on the loopback of
the worker. The coordinator adds the key of the endpoint, and calls the model API.

Run `./run-demo.sh endpoint` after `up`. The script does these actions:

1. It makes the endpoint `demo-upstream` again, with the same `allow.dags` as `up`, and the named MCP server
   `vault` with the REST API. The key and the token come from files in `run/secrets/`, so no argument of a
   process holds them.
2. It reads the models list of the endpoint, changes the list at the fake model API, and presses Reload.
3. It runs the DAG `agent-dev-endpoint`. The step `code` names the endpoint, the skills `project-rules` and `commit-message`, the DAG
   tool `lookup-ticket` and the MCP server `vault`. The fake `claude` makes a streamed model call, and calls
   the tool `vault_lookup`, a tool that `allow` does not list, and the DAG tool.
4. It scans the agent worker for the key and for the token each second during the run. The scan reads the
   environment and the arguments of each process, and the files.
5. It runs `agent-dev-endpoint-denied`. The DAG is not in `allow.dags`, so the start check refuses it.

The DAGs `agent-dev` and `agent-dev-loop` use the same endpoint. The model of their steps is `claude-demo`,
because the start check refuses a model that is not in the models list of the endpoint.

The server container joins the network `dagu-aw-egress`, because the coordinator calls the fake model API
(`fake-anthropic`) and the fake MCP server (`fake-mcp`). The agent worker stays on the internal network
and has no route to them. `evidence.py check-endpoint` checks 24 claims. The evidence goes to
`run/evidence/endpoint/`. The endpoint and the MCP server stay, so the AI page shows them.

## Extra tests with the real Claude Code

The folder `extras/` holds four more tests with the real Claude Code. They need an agent worker that runs the
real Claude Code (the image `dagu-aw-agent:real`, built from `deploy/agent-worker/Dockerfile`), and the
endpoint `claude-sub`. The four DAGs name this endpoint. Run one test with `extras/run-extra.sh <test>`.
The evidence goes to `run/real/extras/<test>/`.

| Test | What it does | Result on 2026-11-03 |
| --- | --- | --- |
| `extra-cli-tools` | Claude Code with the DAG tool `lookup-ticket` (`CUSTOMER_ID` fixed, `QUESTION` from the agent) and the DAG tool `ops_notify` through the tool bridge, and the MCP tool `dagu_read` of the Dagu server, and the two skills `project-rules` and `commit-message`. | The CLI called both tools and `dagu_read`, and gave the typed result in 5 turns. The run succeeded. |
| `extra-cli-limits` | `max_tool_iterations: 1` in the step `turns`, and `max_tool_calls: 1` in the step `calls`. | The step `turns`: the CLI stopped after one turn, and the step failed with the reason `max_tool_iterations` and the exit code 1. The step `calls`: the CLI called `lookup-ticket` three times. The first call ran. The bridge refused the two other calls with an error result, and the step succeeded. |
| `extra-retry` | The run fails at a gate after the agent. The script opens the gate and retries the run. | The first run failed at the gate. The retry kept the agent step and ran the gate on a new agent worker. The run then succeeded. |
| `extra-cli-hostile` | Claude Code is asked to print its environment, to read the key, `/proc/1/environ` and the configuration, to reach GitHub and `example.com`, and to find a token. | Each attempt was blocked: `EACCES`, a refusal of the tool Read, 407, 403. The environment had the placeholder only. |

Claude Code gives the variable `CLAUDE_CODE_MESSAGING_TOKEN` to its own child processes. It is a
value of the session of the CLI, not a credential of the deployment.

## Clean up

Run `./run-demo.sh down`. It removes the services, the networks and the images of the demo.
The container `dagu-unified-demo` stays. The folder `run/` holds the keys and the evidence.
It is not in git. Delete it when you finish.

## Files

| File | Purpose |
| --- | --- |
| `run-demo.sh` | Builds, starts and checks the demo. |
| `certs.sh` | Makes the demo CA and the certificates with `openssl`. |
| `compose.yaml` | The proxy, the fake model API, the git server, the two workers and the attack services. |
| `Dockerfile.agent` | The image of the workers and of the proxy. |
| `config/` | The Dagu configuration of the server and of the two workers. |
| `fake-anthropic/` | The fake Anthropic Messages API. |
| `git-server/` | The git server with HTTP Basic authentication. |
| `seed/` | The project with a test that fails, and a second bug that only `scripts/verify.sh` shows. The git server starts with it. |
| `dags/` | The demo DAGs. |
| `skills/` | The skills of the demo DAGs, in the Agent Skills format: `project-rules` (the code rules and the review checklist, one file) and `commit-message` (a short `SKILL.md`, the file `references/format.md` and the script `scripts/check-message.sh`). The script copies them to `skills/` in the DAGs directory. |
| `evidence.py` | Helper of the script: reads the API output and checks the claims. |
| `dags/agent-dev.yaml`, `dags/agent-dev-isolation.yaml` | The DAG of the work with the fake claude, and the DAG of the hostile probes. |
| `dags/agent-dev-loop.yaml` | The verify loop with the fake claude. |
| `extras/` | The extra real tests: the DAGs and `run-extra.sh`. |
| `fake-mcp/` | The fake MCP server with a bearer token, for the named MCP entry. |
| `dags/agent-dev-endpoint.yaml`, `dags/agent-dev-endpoint-denied.yaml` | The endpoint path with the fake claude, and its refusal. |
| `extras/cli-status.sh` | Shows how the real CLI reports a status of the relay. |
| `extras/leak-check.sh` | Counts the hits of a credential in the logs, the inspect data, the images, the data directory and the files. It prints counts only. |
