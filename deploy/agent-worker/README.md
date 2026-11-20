# Agent worker deployment

This folder holds a hardened reference deployment of a Dagu agent worker. It follows
[Spec 092](../../specs/092-agent-workers.md). The worker runs the real Claude Code CLI.

An agent worker takes one run and exits. The deployment starts a new worker for each run.
The worker reaches only the coordinator and the egress proxy. The agent never sees the real
API key of the model or the secrets of the DAG.

Each `agent.run` step names an AI endpoint and a model in the block `with.endpoint`
([Spec 094](../../specs/094-ai-gateway.md)). A step with no endpoint name or no model fails at load. The
endpoint holds the address and the key of the model API. The CLI calls a relay on the loopback of
the worker, and the coordinator adds the key. See [Model access with an endpoint](#model-access-with-an-endpoint).

## Files

| File | Purpose |
| --- | --- |
| `Dockerfile` | Builds the image: a static `dagu`, `git`, `python3`, `jq` and Claude Code. |
| `config/coordinator.yaml` | Configuration of the control plane: mutual TLS and `coordinator.worker_pools`. |
| `config/agent-worker.yaml` | Configuration of the agent worker: certificate, restricted user, `env_passthrough`. |
| `config/general-worker.yaml` | Configuration of a general worker. |
| `compose.yaml` | Docker Compose: coordinator, general worker, agent worker and proxy. |
| `k8s/` | Kubernetes manifests for the agent worker, the proxy and the network rules. |

The repository tests do not build the image, because the build downloads Claude Code.
One manual build succeeded. The image has no setuid binary, and `claude --version` runs in it.
No cluster test of the manifests was done. The manifests pass the schema check of
`kubeconform` for Kubernetes 1.30.

## Image

Build the image from the repository root:

```sh
docker build -f deploy/agent-worker/Dockerfile -t dagu-agent-worker:local .
```

The build installs Claude Code with the native installer. This is the install line:

```sh
curl -fsSL https://claude.ai/install.sh | bash -s "$CLAUDE_CODE_VERSION"
```

Set the build argument `CLAUDE_CODE_VERSION` to a version that you tested. Check the
install line again when you change the version. One manual test installed version 2.1.311 from
the channel `stable`, and `claude --version` ran. The download is large. It took 15 minutes on a
slow link. The tests of the repository do not run this line.

The image has no setuid binary. The system `git` configuration has `safe.directory '*'`,
because the restricted user reads a repository that the worker owns. The entrypoint is:

```text
dagu worker --config /etc/dagu/config.yaml --agent claude=/usr/local/bin/claude
```

A general worker and the proxy use the same image with another entrypoint.

## Certificates

The coordinator uses mutual TLS. Each worker has a client certificate. The Common Name (CN)
of the certificate is the identity of the worker. The pools of `config/coordinator.yaml`
bind each identity to the labels that it can announce.

| Identity | Files | Pool |
| --- | --- | --- |
| `dagu-coordinator` | `certs/coordinator/` | Server and client certificate. The SAN has `dagu-coordinator`. A dispatcher of `coordinator.dispatchers`. |
| `agent-claude` | `certs/agent-claude/` | Pool `agent-claude`. Label `agent.provider.claude`. |
| `general-worker` | `certs/general-worker/` | Pool `general`. Label `zone`. |

Each folder has `ca.crt`, `<identity>.crt` and `<identity>.key`. Use your own CA in production.
The script `examples/agent-worker-demo/certs.sh` makes test certificates with these names.

Set the owner and the mode as follows:

- The keys of the workers have the owner root and the mode `0400`. The restricted user cannot
  read them. The worker reads them with `CAP_DAC_OVERRIDE`.
- The key of the coordinator belongs to the user ID of `PUID` (1000 in `compose.yaml`).

## Start with Docker Compose

1. Make `certs/` as described above.
2. Edit the `--allow` line of the proxy in `compose.yaml`. Each line is one destination of git or of a tool.
3. Run `docker compose build`.
4. Run `docker compose up -d`.
5. Make an endpoint with the REST API. See [Model access with an endpoint](#model-access-with-an-endpoint).

The agent worker has these values:

| Property | Value |
| --- | --- |
| User | `user: "0:0"`. The change to the restricted user needs `CAP_SETUID`. |
| Capabilities | `cap_drop: [ALL]`. `cap_add` has `CHOWN`, `DAC_OVERRIDE`, `FOWNER`, `SETUID`, `SETGID`, `KILL`. |
| No new privileges | `security_opt: [no-new-privileges:true]`. |
| Root file system | `read_only: true`. A `tmpfs` holds `/tmp` and `/var/lib/dagu`. |
| Network | The network `internal` has `internal: true`. It has the coordinator, the workers and the proxy. |
| Restart | `restart: always`. A new container starts after each exit. |

Docker mounts a `tmpfs` with `noexec`. The tests of a run are scripts in the work directory.
So the two `tmpfs` mounts have the option `exec`.

The proxy is on `internal` and on `outer`. It is the only container of the workers with a route to the
outside. The coordinator is on `outer` too, so it reaches the model API. The worker holds a placeholder
and never holds the key of the model.

## Start on Kubernetes

The folder has no Secret, so that `kubectl apply` never sends a placeholder value. Make the Secret
with the client certificate of the identity `agent-claude`:

```sh
kubectl create namespace dagu-agent
kubectl -n dagu-agent create secret generic agent-claude-tls \
  --from-file=agent-claude.crt --from-file=agent-claude.key --from-file=ca.crt
```

The Common Name of the certificate MUST be in a pool of `coordinator.worker_pools`.
Push your image to your own registry and edit the `image` lines. Then run:

```sh
kubectl apply -k deploy/agent-worker/k8s
```

The manifests need these changes for your cluster:

- The name and the namespace of the coordinator Service in `agent-worker.yaml` and
  `networkpolicy.yaml`. The name must be in the SAN of the coordinator certificate.
- The `--allow` lines of the proxy in `egress-proxy.yaml`.
- The line `hostUsers: false`. Remove it when the cluster does not support user namespaces.

The agent worker pod has these values:

| Property | Value |
| --- | --- |
| User | `runAsUser: 0`, and `hostUsers: false`. |
| Capabilities | `drop: [ALL]`. `add` has the same six capabilities. |
| No new privileges | `allowPrivilegeEscalation: false`. |
| Root file system | `readOnlyRootFilesystem: true`. Two `emptyDir` volumes with `medium: Memory`. |
| Seccomp | `seccompProfile: RuntimeDefault`. |
| Account token | `automountServiceAccountToken: false`. |
| Certificate | A `Secret` volume with `defaultMode: 0400`. |
| Network | A `NetworkPolicy` allows the coordinator, the proxy and DNS. It has no ingress. |

The proxy runs in its own pod and Service. A proxy in the pod of the worker would not limit
the worker, because the pod has one network namespace. A `NetworkPolicy` needs a network
plugin that enforces it. An `emptyDir` has the mode `0777`, so an init container sets the
modes of `/var/lib/dagu` and `/tmp`.

## Model access with an endpoint

An administrator makes the endpoint once, after the coordinator starts. The REST API
`POST /ai/endpoints` makes it. The AI page of the UI shows it. This example makes an endpoint for
the Anthropic API:

```sh
curl -fsS -X POST http://127.0.0.1:8080/api/v1/ai/endpoints \
  -H 'Content-Type: application/json' \
  -d @- <<EOF
{"name": "claude-api", "preset": "anthropic", "protocol": "anthropic", "auth": "x-api-key",
 "scope": "global", "allow": {"workspaces": ["*"], "dags": ["fix-bug"]},
 "key": "$(cat ./anthropic-api-key)"}
EOF
```

The body goes on stdin, so no argument of a process holds the key. When authentication is on,
add the header `Authorization: Bearer <token>` with the token of an administrator.

The key goes to the managed secret store of the coordinator. The coordinator never returns it.
The list `allow.dags` holds the DAGs that may use the endpoint. Each step then names it:

```yaml
steps:
  - id: code
    action: agent.run
    with:
      endpoint:
        name: claude-api
        model: claude-haiku-4-5
      provider: claude
      prompt: Fix the failing test.
```

The worker needs the capability `ai_relay.v1`. A current `dagu` announces it at start. The coordinator
needs a route to the base URL of the endpoint. In `compose.yaml` the coordinator is on `outer`. In
Kubernetes the coordinator pod needs its own egress rule: the manifests of this folder do not have it.
The proxy has no model route. The deployment holds no model key: only the coordinator holds it.

## Tests that need the user ID 0

Some conformance tests start a real agent worker. The worker changes to the restricted user, and this
change needs the user ID 0. These tests skip when the test process is not root:

- `TestUntrustedIsolation` in `conformance/spec092_agent_workers/untrusted_test.go` and four tests in
  `review_test.go` call `untrustedRequireRoot`. They need the user ID 0 and the flag `no-new-privileges`.
- `TestWorkerAgentTakesOneRunAndExits` in `conformance/spec092_agent_workers/worker_run_test.go`
  needs the user ID 0.
- The tests of the suites `conformance/spec093_ai_endpoints`, `conformance/spec094_ai_gateway` and
  `conformance/spec095_mcp_servers` start a stack with `StartStackAs` of `conformance/aitest/stack.go`.
  A test with `needsRoot` true needs the user ID 0. The tests of a general worker pass `needsRoot` false.

A skipped test is not a pass. To run them, use a root container with `no-new-privileges`, as the
comments of the tests say:

```sh
docker run --rm --security-opt no-new-privileges:true -e HOME=/tmp -e GOCACHE=/tmp/gocache \
  -v "$PWD":/src -w /src golang:1.27.3 go test ./conformance/spec092_agent_workers/... ./conformance/spec093_ai_endpoints/... \
  ./conformance/spec094_ai_gateway/... ./conformance/spec095_mcp_servers/...
```

## Variables of the agent side

The deployment gives these variables to the agent worker. The key `env_passthrough` of the
configuration passes them to the steps.

| Variable | Value |
| --- | --- |
| `ANTHROPIC_BASE_URL` | The URL of the proxy, such as `http://egress-proxy:8081`. An `agent.run` step gets the address of the relay in its place. |
| `ANTHROPIC_API_KEY` | A placeholder. It is not a secret. An `agent.run` step gets the secret of the relay in its place. |
| `HTTP_PROXY`, `HTTPS_PROXY` | The URL of the proxy, for the other allowed destinations. |
| `http_proxy`, `https_proxy` | The same URL. `git` uses libcurl, which reads `http_proxy` in lower case only. |
| `NO_PROXY`, `no_proxy` | The host of the coordinator, the host of the proxy, `127.0.0.1` and `localhost`. |
| `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`, `DISABLE_TELEMETRY`, `DISABLE_AUTOUPDATER`, `DISABLE_ERROR_REPORTING` | `1`. |

Put the host of the proxy in `NO_PROXY`. Otherwise Claude Code sends the requests for
`ANTHROPIC_BASE_URL` through `HTTP_PROXY` in absolute form, and the proxy refuses them with
the status 403. Put `127.0.0.1` and `localhost` in `NO_PROXY` too. Otherwise Claude Code sends
the requests of the tool bridge to the proxy, and the step fails with `bridge_unavailable`.

A step cannot list a secret for the CLI agent. The CLI agent gets its credential from the
deployment only.

## Open items

- **Pod Security Standard `restricted`.** The restricted level forbids user ID 0. The worker
  needs `CAP_SETUID` to change to the restricted user. The namespace uses the level `baseline`.
  A path with no capability, such as Landlock, is a candidate for a later version.
- **Restart delay.** After each exit of a container, Kubernetes waits before it restarts the
  container, also after the exit code 0. The delay grows up to 5 minutes. A controller that
  starts one Job for each run removes the delay. This controller is a later item.
- **Claude Code with this image.** One run of the demo with the real Claude Code 2.1.311 used
  this image.
  The tests of the repository use a fake CLI and a fake model API.
- **Login of a subscription (OAuth).** The endpoint form `auth: anthropic-oauth` sends an
  OAuth token of a subscription. The worker keeps the placeholder in `ANTHROPIC_API_KEY`. Do
  not put the placeholder in `CLAUDE_CODE_OAUTH_TOKEN`: with it, Claude Code 2.1.311 also opens
  connections to `api.anthropic.com:443` that do not use `ANTHROPIC_BASE_URL`. Make sure that
  the terms of the subscription let you use the token in this way.

## Limits that remain

- An agent can send data to a destination that the proxy allows.
- An agent can return a false result. The typed result limits the form, not the content.
- An untrusted step shares the kernel with the worker. A kernel flaw can break the isolation.
- A name server of the cluster can carry data out in the names that it gets.
