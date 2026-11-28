# Spec 092: Agent Workers

## Status

Not implemented.

No version of Dagu reads the keys of this spec yet. A configuration that sets
`coordinator.worker_pools` or `coordinator.dispatchers` has no effect, and `dagu validate` rejects
the action `agent.run`.

## Scope

This spec defines:

- the agent worker: the option `--agent` of `dagu worker` and the rules it sets
- how a run that needs a CLI agent is routed to an agent worker
- worker pools: the labels that each client certificate may announce
- the path of model traffic from an agent worker
- the network and credential requirements of a deployment of agent workers
- the threat model of an agent run

This spec does not define:

- the keys of `agent.run`, the typed result and the failure kinds. See Spec 091.
- the tools and skills of an `agent.run` step. See Spec 090 and Spec 091.
- the step keys `secrets` and `untrusted`, and the isolation of an untrusted step. See
  [Spec 103](103-untrusted-steps.md).
- the references that a step may make to agent output. See [Spec 104](104-agent-output.md).
- the egress proxy. See [Spec 105](105-egress-proxy.md).
- the secret providers. See [Spec 069](069-secrets-providers.md).
- a controller that starts workers. The deployment starts them.

## Related Specs

| Spec | Defines |
| --- | --- |
| [090: Agent Tools](090-agent-tools.md) | The tools that an `agent.run` step selects. |
| [091: Agent Node](091-agent-node.md) | The action `agent.run` and the tool bridge that gives Dagu tools to the CLI agent. |
| 092: Agent Workers | This spec. |
| [093: Model Connections](093-model-connections.md) | Model API connections and the step keys that name them. |
| [094: AI Gateway](094-ai-gateway.md) | The relay that sends model requests through the coordinator. |
| [096: Skills](096-skills.md) | Skills and the workspace bundle that carries them. |
| [103: Untrusted Steps and Step Secrets](103-untrusted-steps.md) | Per-step secrets and the restricted user. |
| [104: References to Agent Output](104-agent-output.md) | Where a step may reference agent output. |
| [105: Egress Proxy](105-egress-proxy.md) | `dagu egress-proxy serve`. |

## Goal

A workflow author chains a CLI agent, a test script and a git push in one DAG and one run. The
agent never sees the push token or the model API key. The author writes no image, no network rule
and no container option in a step. The deployment fixes the image, the network and the
credentials.

## Architecture

```text
  dispatchers                  coordinator                         outside
 +-------------+   mTLS    +--------------------+   HTTPS   +-------------------+
 | server      |---------->| worker pools       |---------->| model provider    |
 | scheduler   | dispatch  | registry secrets   |   (key)   | API               |
 | CLI         |           | AI gateway         |           +-------------------+
 +-------------+           +--------------------+
                                     ^
                                     | gRPC over mTLS, pool certificate
                                     |
 +-----------------------------------+--------------------------------+
 | agent worker: one container or pod, one run, then exit             |
 |                                                                    |
 |  dagu worker --agent claude=/usr/local/bin/claude                  |
 |  user 0 with six capabilities                                      |
 |  holds the certificate, the configuration and the step secrets     |
 |  runs trusted steps                                                |
 |  serves the relay (Spec 094) and the tool bridge (Spec 091)        |
 |        ^                                                           |
 |        | 127.0.0.1 only                                            |
 |  +-----+--------------------------------------------------------+  |
 |  | restricted user (untrusted.uid, untrusted.gid)               |  |
 |  | the CLI agent, every process it starts, untrusted steps      |  |
 |  +--------------------------------------------------------------+  |
 +-----------------------------------+--------------------------------+
                                     |
                                     | HTTP_PROXY, HTTPS_PROXY
                                     v
                           +--------------------+   allowed     +-------------+
                           | egress proxy       |-------------->| git server, |
                           | (Spec 105)         | destinations  | other hosts |
                           +--------------------+               +-------------+
```

The worker has two network paths: the coordinator and the egress proxy. The model requests of a
`claude` step go through the relay and the coordinator, never through the proxy. See [Model
traffic](#model-traffic).

A run on an agent worker:

```text
 scheduler           coordinator                 agent worker              egress proxy
    |                     |                            |                         |
    |-- dispatch run ---->|  selector gets             |                         |
    |                     |  agent.provider.claude     |                         |
    |                     |<---- poll with labels -----|                         |
    |                     |----- task ---------------->|                         |
    |                     |<---- acknowledge claim ----|  polls no more          |
    |                     |                            |                         |
    |                     |<---- secret for "clone" ---|  trusted step           |
    |                     |                            |---- git clone --------->|
    |                     |<---- model requests -------|  agent step, via relay  |
    |                     |                            |  verify step, as the    |
    |                     |                            |  restricted user        |
    |                     |<---- secret for "push" ----|  trusted step           |
    |                     |                            |---- git push ---------->|
    |                     |<---- final status, logs ---|                         |
    |                     |<---- offline heartbeat ----|                         |
    |                     |                            X  exit 0; the deployment |
    |                     |                               starts a new worker    |
```

## Terms

**Agent worker.** A `dagu worker` process started with the option `--agent`.

**General worker.** A `dagu worker` process without the option `--agent`.

**CLI provider.** A provider of Spec 091, such as `claude`.

**Needs a provider.** A DAG needs a provider when it has an `agent.run` step with that CLI
provider. Dagu counts the steps of the DAG, the steps in the body of each `foreach` step and the
handler steps (init, success, failure, abort, exit and wait). A step of a sub-DAG does not count
for the parent.

**Agent run.** A run of a DAG that needs a provider.

**Worker pool.** A named set of certificate identities and the labels they may announce.

**Identity.** The Common Name (CN) of the subject of a client certificate.

**Dispatcher.** An identity that sends runs to the coordinator, such as the server, the scheduler
or the command line.

**Untrusted step, trusted step, restricted user, work directory.** Defined in
[Spec 103](103-untrusted-steps.md#terms).

**Agent data.** Defined in [Spec 104](104-agent-output.md#terms).

**Proxy.** The egress proxy of the deployment ([Spec 105](105-egress-proxy.md)). It limits the
destinations and adds the real credential of the model API.

## Threat model

### Assets

- the secrets of the DAG, such as a git token
- the model API key
- the client certificate, the private key and the configuration of the worker
- the repository history that a trusted step writes, such as a push
- the runs of other DAGs

### Trust

| Party | Trust |
| --- | --- |
| The DAG author and the code of a trusted step | Trusted. |
| The Dagu worker process, the coordinator, the server, the scheduler and the proxy | Trusted. |
| The deployment: the image, the certificates, the network, the restart policy | Trusted. |
| The CLI agent, every process it starts, and every untrusted step | Not trusted. A prompt, a repository file or a tool result can control it. |
| A process that holds a client certificate | Trusted for the pool of its certificate only. |

### Threats and controls

| Threat | Control | What remains |
| --- | --- | --- |
| The agent reads a DAG secret. | The agent step gets no secret. The restricted user cannot read the memory, the environment or the files of the worker. The coordinator gives a registry secret only to the step that lists it. ([103](103-untrusted-steps.md)) | Nothing for a secret that the agent step does not list. An agent step cannot list one. |
| The agent reads or misuses the model API key. | A step of a CLI with an adapter names a model connection ([093](093-model-connections.md)): the key stays in the coordinator, and the agent holds only the relay secret (Spec 094). A CLI with no adapter holds a placeholder, and the proxy adds the real key ([105](105-egress-proxy.md#key-injection)). | The agent can send model requests billed to the key owner. |
| The agent sends data out. | The worker reaches only the coordinator and the proxy, and no worker DNS query leaves the deployment ([Deployment](#deployment-requirements)). The proxy allows a list of destinations. A destination that needs the proxy credential is closed to the agent ([105](105-egress-proxy.md)). | Data can go to an allowed destination, such as the model API or the git server. |
| The agent changes `.git` so that a later trusted step runs its code: hooks, `core.fsmonitor`, a new `.git`. | The restricted user cannot write `.git` and cannot rename or replace it ([103](103-untrusted-steps.md#isolation)). | A trusted step that runs a file of the work directory runs agent code. Authors do not do this. |
| Agent output becomes code that Dagu or a trusted step runs. | A step may reference agent data in two forms only ([104](104-agent-output.md)). | A program that runs a value or reads a variable by name. Agent data that reaches a step without a reference, such as a file of the work directory. |
| The agent leaves a process behind to watch a later step. | At the end of each untrusted step, Dagu kills every process of the restricted user. Untrusted steps run one at a time ([103](103-untrusted-steps.md#isolation)). | Nothing. |
| The agent connects to the coordinator. | The coordinator requires a client certificate, and the restricted user cannot read it. | Nothing. |
| The agent calls a tool of the tool bridge with bad arguments. | The tools are DAGs of the author. The bridge needs the secret of the attempt. The agent cannot set a fixed input of `params`, or an input that the tool does not expose. The child run gets the inputs as one JSON object, and each input that the agent sets is agent data in the child run. | A tool that trusts its arguments. Authors design each tool to accept any argument. |
| A general worker claims an agent run, or an agent worker claims another run. | The coordinator takes the labels from the pool of the certificate. An agent pool claims only runs of its provider ([Worker pools](#worker-pools)). | A stolen pool certificate acts as that pool. |
| Files or processes of one run reach the next run. | An agent worker takes one run, then exits. On Docker, each container start gets a new `tmpfs`. | On Kubernetes, an `emptyDir` lives as long as the pod. A container that restarts in the same pod keeps the files of `/tmp` and of the Dagu home, and the next run on the pod can read them. See [Open items](#open-items). A deployment that keeps another volume keeps its files too. |
| The agent returns a false result. | The typed result fixes the shape. A gate that Dagu runs and `human.task` protect an action. | The content of a result can be false. |
| The agent escapes the restricted user through a kernel flaw. | Out of scope. A user namespace or a runtime such as gVisor reduces the risk. | The flaw. |

With an OAuth token, the agent uses the subscription of the token owner. The owner makes sure that
the terms of the subscription allow this use.

Specs 103, 104 and 105 list the limits that remain for each of their controls.

## The agent worker

### Start

```text
dagu worker --agent <provider>=<path>
```

`<provider>` is a CLI provider of Spec 091. `<path>` is the absolute path of its binary in the
worker image. The image holds the binary and every package that the agent and the tests need. Dagu
installs nothing at run time.

The worker runs these checks before it connects to the coordinator. A failed check stops the worker
with exit code 1. The error names the key or the option, the expected value and the actual value.

1. The option has the form `<provider>=<path>`, and the worker has exactly one option `--agent`.
2. The provider is a CLI provider, and the path is absolute.
3. The path is a regular file that the restricted user can run.
4. The configuration does not set `worker.max_active_runs`. The flag `--worker.max-active-runs`,
   the variable `DAGU_WORKER_MAX_ACTIVE_RUNS` and the configuration file key are each an error:

   ```text
   --agent: key "worker.max_active_runs" is set to "4": an agent worker always runs one run
   ```

5. The connection to the coordinator uses mutual TLS: `peer.insecure` is `false`,
   `peer.skip_tls_verify` is `false`, and `peer.cert_file`, `peer.key_file` and
   `peer.client_ca_file` are set.
6. The configuration sets `untrusted.uid` and `untrusted.gid`, and `untrusted.isolation` is not
   `off`. See [Spec 103](103-untrusted-steps.md#the-restricted-user).
7. The process can switch to the restricted user: its effective user ID is 0, and its effective
   capabilities include `CAP_SETUID`, `CAP_SETGID`, `CAP_CHOWN`, `CAP_FOWNER`, `CAP_DAC_OVERRIDE`
   and `CAP_KILL`. The error names the first missing capability.
8. The process has the no-new-privileges flag (`NoNewPrivs: 1` in `/proc/self/status`).

### Rules that `--agent` sets

No other option or key changes these rules.

- The worker runs one run at a time, with one poller.
- The worker takes one run. After it acknowledges one claim, it stops polling.
- The worker announces the label `agent.provider.<provider>` with the value `true`, next to the
  labels `os` and `arch` and the labels of `worker.labels`.
- The worker takes only runs that need its provider. See [Routing](#routing).

### Lifecycle

```text
  start
    |
  checks of Start ---- fail ---------------------------------> exit 1
    |
  connect, poll ------ stop signal before a claim ------------------+
    |                                                               |
    | claim acknowledged; no more polling                           |
    v                                                               |
  run the DAG          a stop signal stops the run as on a          |
    |                  general worker                               |
    v                                                               |
  send final status, logs and artifacts                             |
    |                                                               |
    v                                                               v
  offline heartbeat                                         offline heartbeat
    |                                                               |
  exit 0, or exit 1 when the final status was not sent            exit 0

  After each exit, the deployment starts a new worker.
```

### End

The run ends with a final status. The worker sends the final status, the logs and the artifacts to
the coordinator, then exits with code 0. The status of the run does not change the exit code. A
failure to send the final status gives exit code 1.

A stop signal before a claim stops the worker with code 0. A stop signal during the run stops the
run as on a general worker. Then the worker sends the status and exits.

Before it exits, the worker sends a last heartbeat that marks it offline. This heartbeat goes
through the same checks as any heartbeat, so only the pool of the worker ID can mark the worker
offline. The coordinator then stops counting the worker as available, and a new run waits for the
next worker. A worker that cannot send this heartbeat stays available for 30 seconds after its last
heartbeat.

The deployment starts a new worker after each exit. Dagu does not start it. A run that a worker
claimed and did not finish follows the coordinator rules for a lost worker.

A retry of a step stays in the run, on the same worker. A retry of a run is a new task for any
agent worker of the provider.

## Routing

### Implied selector

A DAG that needs the provider `<P>` has the implied label `agent.provider.<P>: "true"`. Dagu adds it
to the `worker_selector` of the DAG.

A DAG that needs two providers is a validation error, because one agent worker runs the whole run:

```text
the DAG needs the providers "claude" and "codex": a run goes to one agent worker
```

A DAG that needs a provider cannot have `worker_selector: local` and cannot have a step with
`approval`. An agent worker takes one run and exits, so it cannot wait for a person. Put the
approval in a parent DAG.

### Dispatch

```text
  run of a DAG that needs a provider
    |
    +-- coordinator.enabled: true (default) --> coordinator --> agent worker of the provider
    |
    +-- coordinator.enabled: false -----------> the local process runs it, as the host user;
                                                needs untrusted.isolation: off
```

A process has a coordinator when its key `coordinator.enabled` is true. This is the default. The
process does not check that the coordinator answers, so an outage never moves an agent onto the
scheduler host.

When the process has a coordinator, a run of a DAG that needs a provider always goes to the
coordinator, on the same path as a DAG with a `worker_selector`. A sub-DAG run that needs a
provider also goes to the coordinator, with its own implied selector. `dagu start`, `dagu retry`
and `dagu restart` place a run as the API and the scheduler do, so they send such a run to the
coordinator too. A start or retry that a Dagu process launches on its host for a run it placed there
runs there and is not placed again.

The dispatch of a DAG that uses skills carries them. The coordinator adds every file of each skill
of the DAG to the workspace bundle of the run, and the agent worker reads the skills from the
bundle, not from the skills directory of the coordinator. The coordinator checks the hash of each
skill after it builds the bundle. A skill that changed in that time fails the dispatch, and the
error names the skill ([Spec 096](096-skills.md), "The bundle"). A skill outside the working
directory of the DAG fails the dispatch, and the error names the file.

A process with `coordinator.enabled: false` runs the DAG itself. The CLI agent then runs on the
host, as a process of the host user. This is the mode for development. It needs
`untrusted.isolation: off` ([Spec 103](103-untrusted-steps.md#without-isolation)).

### Match

A task matches a worker when each label of the task selector has the same value on the worker. This
rule does not change. This spec adds one rule: a worker with a label `agent.provider.<P>` matches
only a task whose selector has the label `agent.provider.<P>`.

| Worker | Task selector | Match |
| --- | --- | --- |
| Agent worker, `agent.provider.claude: true` | `{agent.provider.claude: "true"}` | Yes. |
| Agent worker, `agent.provider.claude: true` | `{agent.provider.codex: "true"}` | No. Another provider. |
| Agent worker, `agent.provider.claude: true`, `zone: eu` | `{zone: eu}` | No. An agent worker takes only runs of its provider. |
| General worker, `zone: eu` | `{agent.provider.claude: "true"}` | No. The worker lacks the label. |

So an agent run goes only to an agent worker of its provider, and a run that needs no provider never
goes to an agent worker. The coordinator, the scheduler and the check of available workers use the
same rules.

### No match

A run with no matching worker waits for `coordinator.worker_wait_timeout`, like a run with an
unmatched `worker_selector`. The condition of the run and the final error name the provider:

```text
no agent worker serves the provider "claude": the selector is {agent.provider.claude: "true"}
```

A worker can stop after its last heartbeat and before the coordinator sees it as stale. A run that
the coordinator accepts in that window also waits for `coordinator.worker_wait_timeout`, or for 90
seconds when this key is 0. The next matching worker takes it. If no worker takes it, the run fails,
and the error names the provider. The start of a run does not fail when no worker takes the run
during the start request.

An agent worker takes one run and restarts. With `coordinator.worker_wait_timeout` at 0, the
coordinator refuses a run that arrives while the worker restarts. So a coordinator with an agent
pool and this key at 0 logs a warning at start. Set a wait above 0.

### Reserved labels

Dagu sets the labels that start with `agent.`. These cases are errors:

| Where | Result |
| --- | --- |
| A key of `worker_selector` in a DAG or a step | `dagu validate` rejects it. |
| A key of the selector of a run, from the API or the option `--worker-label` | The start of the run fails. |
| A key of `worker.labels` | The worker stops at start. |

## Worker pools

### Configuration

The coordinator binds the labels of a worker to its client certificate. The key
`coordinator.worker_pools` is a list of pools.

```yaml
coordinator:
  worker_pools:
    - name: agent-claude
      identities: [agent-claude]
      labels:
        agent.provider.claude: "true"
    - name: general
      identities: [general-worker]
      labels:
        zone: "*"
  dispatchers: [dagu-server, dagu-scheduler]
```

| Key | Type | Rule |
| --- | --- | --- |
| `name` | string | Required. Matches `^[a-z][a-z0-9-]*$`. Unique. |
| `identities` | list | Required. Not empty. Each item is the CN of a client certificate. An identity is in one pool only. |
| `labels` | map | Optional. A label key and its allowed value. The value `*` allows any value. |

The key `coordinator.dispatchers` is a list of dispatcher identities.

| Key | Type | Rule |
| --- | --- | --- |
| `dispatchers` | list | Required with `worker_pools`. Not valid without `worker_pools`. Not empty. Each item is the CN of a client certificate, listed once, and not an identity of a pool. |

A pool with a label `agent.provider.<P>` is an agent pool.

The schema is closed. An unknown key, a duplicate key or a wrong type stops the coordinator at
start. These cases also stop it at start:

- `worker_pools` without `peer.cert_file`, `peer.key_file` and `peer.client_ca_file` on the
  coordinator. The error names `coordinator.worker_pools` and the missing key.
- `worker_pools` without `dispatchers`, or `dispatchers` without `worker_pools`.
- an identity that is both a dispatcher and a pool member. An identity has one role.
- a label `agent.provider.<P>` with a value other than `true`, or a `<P>` that is not a CLI
  provider.
- a label key `os` or `arch`. Dagu sets these labels, and every pool allows them with any value.
- two labels `agent.provider.<P>` in one pool. An agent worker serves one provider.
- a label key that starts with `agent.` and is not `agent.provider.<P>`.

No environment variable sets these keys. `dagu config` prints the pools (name, identities and
labels) and the dispatchers.

### Behavior

With `worker_pools`, the coordinator authorizes each call by the identity of its client
certificate:

```text
  client certificate identity
    |
    +-- listed in coordinator.dispatchers --> may dispatch root runs and make the
    |                                         other calls
    |
    +-- listed in a pool ------------------> may make worker calls:
    |                                          - each announced label is allowed by the pool
    |                                          - calls about an attempt come from the pool
    |                                            that claimed it
    |                                          - other calls only with a fresh lease
    |
    +-- neither ----------------------------> PermissionDenied
```

**Worker calls.** Each worker call MUST come from an identity in a pool. The worker calls are:

- the poll, the heartbeat, the acknowledgment of a claim and the heartbeat of a run
- the status report, and the log and artifact streams
- the resolution of a secret or of a runtime profile
- the claim and the completion of an agent session cleanup
- the AI gateway calls `CheckAIEndpoint` and `RelayAIRequest`

A call from another identity fails with the gRPC code `PermissionDenied`:

```text
certificate identity "rogue" is in no worker pool
```

**Labels.** Each label that a worker announces in a poll or a heartbeat MUST be allowed by its pool.
A label that the pool does not allow fails the call with `PermissionDenied`, and the error names the
label, its value and the pool. The coordinator keeps no label of a refused call. A worker of an
agent pool MUST announce the label `agent.provider.<P>` of its pool with the value `true`. A poll
or a heartbeat without it fails with `PermissionDenied`.

**Attempts.** The coordinator records the pool of the claiming worker in the claim and in the lease
of the attempt. Each later call for this attempt MUST come from a certificate of the same pool. A
call from another pool fails with `PermissionDenied`. A status report for an attempt with no lease,
such as a report after the final status, also fails. A worker ID belongs to the pool of its last
heartbeat, and a heartbeat or a cleanup call for that worker ID from another pool fails.

**Selectors.** The coordinator sets the labels that start with `agent.` in the selector of each
task. It removes such labels from the request and adds the implied selector of the DAG.

**Other calls.** Every other call MUST come from a dispatcher, or from a worker for its own runs. A
worker makes such a call only while its pool holds a fresh lease:

| Call | A pool may make it while it holds |
| --- | --- |
| Dispatch of a root run | Never. A dispatcher only. |
| Dispatch of a child run; status or cancel of a run | A lease of the same root run. |
| Read of a workspace bundle | A lease of that bundle. |
| DAG, state, check or write of a bundle, list of workers | Any lease. |

A call that breaks these rules fails with `PermissionDenied`, and the error names the identity:

```text
certificate identity "rogue" is not a dispatcher and is in no worker pool
```

**Without `worker_pools`.** The coordinator accepts the labels that a worker announces, except the
labels that start with `agent.`. A poll or a heartbeat with such a label fails with
`PermissionDenied`:

```text
label "agent.provider.claude" needs coordinator.worker_pools
```

So without pools, no worker serves an agent run, and an agent run waits, then fails.

## Model traffic

```text
  a CLI with an adapter: the step names a model connection (required)

    +-----------+  relay base URL      +-----------+  gRPC  +-------------+  key  +----------+
    | CLI agent |--------------------->| relay     |------->| coordinator |------>| model    |
    |           |  relay secret        | 127.0.0.1 |        | (Spec 094)  |       | provider |
    +-----------+                      +-----------+        +-------------+       +----------+

  a CLI with no adapter

    +-----------+  placeholder key     +--------------+  real key                 +----------+
    | CLI agent |--------------------->| egress proxy |-------------------------->| model    |
    |           |                      | --upstream   |                           | provider |
    +-----------+                      | (Spec 105)   |                           +----------+
                                       +--------------+
```

**A CLI with an adapter.** The step MUST name a model connection: its `connection` and `model`,
or the DAG's `llm` ([Spec 093](093-model-connections.md#keys)). The adapter points the CLI at the
relay address of the attempt, with the relay secret as its key, and removes every other credential
source of the CLI ([Adapters](093-model-connections.md#adapters)). For `claude`, Dagu sets
`ANTHROPIC_BASE_URL` and `ANTHROPIC_API_KEY`, and the CLI gets no other variable that starts with
`ANTHROPIC_` or `CLAUDE_CODE_USE_`, and no `CLAUDE_CODE_OAUTH_TOKEN`, from the environment. Its
model traffic does not go through the proxy, so the proxy needs no model route for it.

A long-lived subscription token, such as the token of `claude setup-token`, is the key of a model
connection with the auth `anthropic-oauth` (Spec 093). The worker never holds it.

**A CLI with no adapter.** A step cannot list a secret for a CLI agent
([Spec 103](103-untrusted-steps.md)). The provider gets its model credential from the deployment:
a placeholder in its variables, which the proxy replaces with the real key on the upstream route
([Spec 105](105-egress-proxy.md#key-injection)).

**The relay on an agent worker.** [Spec 094](094-ai-gateway.md) defines the gateway. On an agent
worker:

- The relay listens on `127.0.0.1` and a random port, for one attempt, like the tool bridge of
  Spec 091. The CLI gets the relay secret in a variable. For `mcp_servers`, the MCP config of the
  attempt holds the gateway token of RFC 026. The restricted user owns that file, with mode `0400`.
- Dagu adds `127.0.0.1` and `localhost` to `NO_PROXY` of a step that uses the relay. The deployment
  does not need to set them for this step.
- The worker announces the capability `ai_relay.v1`. A run with a step that names a model
  connection goes only to a worker with this capability.
- The gateway needs the mutual TLS that an agent worker already has. It uses the pool and lease
  checks of [Worker pools](#worker-pools) and of [Spec 103](103-untrusted-steps.md#resolution).
- The coordinator needs outbound access to the provider. The worker still reaches only the
  coordinator and the proxy.

The proxy stays in use for other traffic. A trusted step uses it for the git clone and push, for
example to `github.com`.

## Deployment requirements

### Network

```text
              internal network                    :          outside
                                                  :
    +--------------+        +-------------+       :
    | agent worker |------->| coordinator |---------------->  model provider API
    +--------------+        +-------------+       :
           |                                      :
           |                +--------------+      :
           +--------------->| egress proxy |---------------->  allowed destinations
                            +--------------+      :
                                                  :
  The worker has no route across the line, and no DNS query of the worker crosses it.
```

The container or pod of an agent worker reaches only the coordinator and the proxy. It has no other
route. The proxy reaches the allowed destinations. The proxy is a separate container on Docker and a
separate pod on Kubernetes. A proxy in the same pod as the worker does not limit the worker, because
a pod has one network namespace.

No DNS query of the worker leaves the deployment. A resolver forwards the query for a name to the
server of that name, so the agent could send data out in the labels of names, past the proxy. The
worker needs only the names of the coordinator and the proxy, and the proxy resolves every other
name.

- On Docker, the DNS of an internal network answers for its containers and, from Docker Engine 26.0,
  forwards no other name (CVE-2024-29018).
- On Kubernetes, the `NetworkPolicy` allows no DNS, and `hostAliases` give the worker the Service
  IPs of the coordinator and the proxy.

### Security settings

| Property | Docker Compose | Kubernetes |
| --- | --- | --- |
| User | `user: "0:0"` | `runAsUser: 0`, and `hostUsers: false` when the cluster supports user namespaces |
| Capabilities | `cap_drop: [ALL]`, `cap_add` of the six capabilities of [Start](#start) | `drop: [ALL]`, `add` of the same six |
| No new privileges | `security_opt: [no-new-privileges:true]` | `allowPrivilegeEscalation: false` |
| Root file system | `read_only: true`, `tmpfs` with the option `exec` for `/tmp` and the Dagu home | `readOnlyRootFilesystem: true`, `emptyDir` with `medium: Memory` |
| Seccomp | The default profile | `seccompProfile: RuntimeDefault` |
| Account token | None | `automountServiceAccountToken: false` |
| Network | An internal network with the coordinator and the proxy, on Docker Engine 26.0 or later | A `NetworkPolicy` that allows the coordinator and the proxy, and no DNS; `hostAliases` with their Service IPs |
| Certificate | A read-only file, owner 0, mode `0400` | A `Secret` volume with `defaultMode: 0400` |
| Restart | `restart: always` | A `Deployment` |

The worker starts as user ID 0 because switching to the restricted user needs `CAP_SETUID`. The Pod
Security Standard `restricted` forbids this, so a deployment uses the level `baseline`. See [Open
items](#open-items).

### Environment of the worker

The deployment gives these variables to the agent worker, and the configuration option
`env_passthrough` passes them to the steps.

| Variable | Value |
| --- | --- |
| `HTTP_PROXY`, `HTTPS_PROXY`, `http_proxy`, `https_proxy` | The URL of the proxy. `git` reads only the lower-case `http_proxy`. |
| `NO_PROXY`, `no_proxy` | The host of the coordinator, the host of the proxy, `127.0.0.1` and `localhost`. |
| `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`, `DISABLE_TELEMETRY`, `DISABLE_AUTOUPDATER`, `DISABLE_ERROR_REPORTING` | `1`. |

`NO_PROXY` names `127.0.0.1` and `localhost` because Claude Code sends tool bridge requests through
`HTTP_PROXY` otherwise. The proxy refuses them, and the step fails with the reason
`bridge_unavailable`.

The host of an inline `mcp` entry goes through the proxy unless `NO_PROXY` names it, so the proxy
must allow it. A server of `mcp_servers` goes to the MCP gateway of the step on `127.0.0.1`. The
gateway, in the worker process, reaches the server through the proxy with the proxy credential, so
the proxy must allow that host too. See [Spec 095](095-mcp-servers.md#on-an-agent-worker).

## Errors

| Case | Result |
| --- | --- |
| A DAG that needs two CLI providers | `dagu validate` rejects it. |
| `worker_selector: local`, or a step with `approval`, in a DAG that needs a provider | `dagu validate` rejects it. |
| A key of `worker_selector` that starts with `agent.` | `dagu validate` rejects it. |
| A key of a step `worker_selector` with a value reference, such as `${K}` | `dagu validate` rejects it. |
| A failed check of [Start](#start) | The agent worker exits with code 1. |
| A key of `worker.labels` that starts with `agent.` | The worker exits with code 1 at start. |
| An invalid `coordinator.worker_pools` or `coordinator.dispatchers`, or one without the other | The coordinator exits with code 1 at start. |
| `dagu retry` or `dagu restart` of a DAG that needs a provider | The command refuses the DAG. |
| A key that starts with `agent.` in the selector of a run, from the API or `--worker-label` | The start of the run fails. |
| A key of a step selector that starts with `agent.` after the run resolves it | The step fails. |
| A worker call from an identity in no pool, or with a label that the pool does not allow | `PermissionDenied`. |
| A worker call for a lease of another pool | `PermissionDenied`. |
| A status report with no lease, with `worker_pools` | The report is refused. |
| Another call from an identity that is not a dispatcher, when its pool holds no fresh lease for the call | `PermissionDenied`. |
| A dispatch of a root run from an identity that is not a dispatcher | `PermissionDenied`. |
| A label that starts with `agent.` without `worker_pools` | `PermissionDenied`. |
| No agent worker for the provider within `coordinator.worker_wait_timeout` | The run fails. |

## Examples

### Complete DAG

The file `dev-with-agent.yaml` sets each key of `agent.run` that the provider `claude` accepts. It
is one DAG and one run, with three rounds of a verify loop: Claude changes the code, a script
verifies it, and a failed verify gives Claude its log for another round. After a passing verify,
trusted steps check the changed paths and push a branch. The DAG needs the tools
`lookup-ticket.yaml` and `ops_notify.yaml` that follow it. `dagu validate` accepts it.

```text
  clone
    |
    v
  code_1 --> verify_1 --- pass -----------------------------+
                |                                           |
              exit 1                                        |
                v                                           |
             code_2 --> verify_2 --- pass ------------------+
                           |                                |
                         exit 1                             |
                           v                                v
                        code_3 --> verify_3 --- pass --> check_paths --> message --> push
                                      |
                                    fails --> the run fails; nothing is pushed

  After a pass, Dagu skips the later code and verify steps. verify_3 has
  continue_on skipped, so check_paths still runs. A verify step that fails
  without exit code 1 fails the run.
```

| Step | Kind | Trust | Secrets |
| --- | --- | --- | --- |
| `clone` | `git.checkout` | Not a process step; the key `untrusted` does not apply. | `GIT_TOKEN` |
| `code_1`, `code_2`, `code_3` | `agent.run` with `claude` | Untrusted: a CLI provider. | None |
| `verify_1`, `verify_2`, `verify_3` | `run` | Untrusted by default: the DAG has an `agent.run` step. Runs as the restricted user. | None |
| `check_paths` | `run` | Trusted: `untrusted: false`. | None |
| `message` | `chat.completion` | Not a process step. | None |
| `push` | `run` | Trusted: `untrusted: false`. | `GIT_TOKEN` |

```yaml
description: Claude develops, a script verifies, the code pushes to a branch. A failed verify gives Claude its log for a new round, with 3 rounds at most. One DAG, one run.
type: graph
params:
  - name: TASK
    type: string
    default: Fix the failing test of the calculator.
  - name: REPO
    type: string
    default: http://git-server:8080/app.git
  - name: BRANCH
    type: string
    default: agent/fix-calculator
  - name: MCP_URL
    type: string
    default: http://docs.internal:8080/mcp
secrets:
  - name: GIT_TOKEN
    ref: git-token
steps:
  - id: clone
    action: git.checkout
    secrets: [GIT_TOKEN]
    with: { repository: "${REPO}", path: repo, token: "${GIT_TOKEN}" }

  # Round 1.
  - id: code_1
    depends: [clone]
    working_dir: repo
    action: agent.run
    timeout_sec: 1800
    retry_policy: { limit: 12, interval_sec: 300, exit_code: [75] }
    preconditions:
      - condition: "${TASK}"
        expected: "re:.+"
    output_schema:
      type: object
      additionalProperties: false
      properties:
        verdict: { type: string, enum: [done, blocked] }
        summary: { type: string }
      required: [verdict, summary]
    with:
      prompt: |
        ${TASK}
        Run ./scripts/test.sh and fix the code until it passes.
      system: Write small, tested changes. Never edit the files of the CI.
      provider: claude
      connection: cle1
      model: sonnet
      max_tool_iterations: 40
      max_tool_calls: 50
      dags:
        - name: lookup-ticket
          params:
            CUSTOMER_ID:
              from_ai:
                description: The ID of the customer of the ticket
        - name: ops_notify
          params:
            text:
              from_ai:
                description: The message to post on the ops channel
      tools:
        mcp:
          - name: docs
            url: ${MCP_URL}
            headers: { X-Team: dev }
            allow: [search]
        builtin:
          read: true
          write: true
          edit: true
          search: true
          shell:
            allow: ["./scripts/test.sh", "git diff *", "git status *", "git log *"]
            deny: ["git push *", "git remote *"]

  # The verify runs a stricter script than the one that Claude knows. This step is untrusted
  # by default, so it runs as the restricted user. Its exit code is the verdict: each failure
  # of the script exits 1, and only 1 starts the next round. Any other failure of the step
  # fails the run. The log goes to a file in the work directory, for the next round.
  - id: verify_1
    depends: [code_1]
    working_dir: repo
    continue_on: { exit_code: [1] }
    run: |
      rc=0
      ./scripts/verify.sh > "${DAG_RUN_WORK_DIR}/${DAG_RUN_STEP_NAME}.log" 2>&1 || rc=1
      cat "${DAG_RUN_WORK_DIR}/${DAG_RUN_STEP_NAME}.log"
      exit $rc

  # Round 2. The step runs only when verify_1 failed. Dagu skips it, and the steps of the
  # last round, when verify_1 passed.
  - id: code_2
    depends: [verify_1]
    working_dir: repo
    action: agent.run
    timeout_sec: 1800
    retry_policy: { limit: 12, interval_sec: 300, exit_code: [75] }
    preconditions:
      - condition: "${verify_1.exit_code}"
        expected: "0"
        negate: true
    with:
      prompt: |
        ${TASK}
        The check of the project failed after your last change. The log is in the file
        ${DAG_RUN_WORK_DIR}/verify_1.log
        Read the log, fix the code, and run ./scripts/test.sh again.
      system: Write small, tested changes. Never edit the files of the CI.
      provider: claude
      connection: cle1
      model: sonnet
      max_tool_iterations: 40
      tools:
        builtin:
          read: true
          write: true
          edit: true
          search: true
          shell:
            allow: ["./scripts/test.sh", "git diff *", "git status *", "git log *"]
            deny: ["git push *", "git remote *"]

  - id: verify_2
    depends: [code_2]
    working_dir: repo
    continue_on: { exit_code: [1] }
    run: |
      rc=0
      ./scripts/verify.sh > "${DAG_RUN_WORK_DIR}/${DAG_RUN_STEP_NAME}.log" 2>&1 || rc=1
      cat "${DAG_RUN_WORK_DIR}/${DAG_RUN_STEP_NAME}.log"
      exit $rc

  # Round 3, the last. The step verify_3 does not continue after a failure: a failed
  # verify_3 fails the run, and the steps after the loop do not run.
  - id: code_3
    depends: [verify_2]
    working_dir: repo
    action: agent.run
    timeout_sec: 1800
    retry_policy: { limit: 12, interval_sec: 300, exit_code: [75] }
    preconditions:
      - condition: "${verify_2.exit_code}"
        expected: "0"
        negate: true
    with:
      prompt: |
        ${TASK}
        The check of the project failed after your last change. The log is in the file
        ${DAG_RUN_WORK_DIR}/verify_2.log
        Read the log, fix the code, and run ./scripts/test.sh again.
      system: Write small, tested changes. Never edit the files of the CI.
      provider: claude
      connection: cle1
      model: sonnet
      max_tool_iterations: 40
      tools:
        builtin:
          read: true
          write: true
          edit: true
          search: true
          shell:
            allow: ["./scripts/test.sh", "git diff *", "git status *", "git log *"]
            deny: ["git push *", "git remote *"]

  # Dagu skips this step when a round before it passed, because it skipped code_3. With
  # continue_on skipped, the steps after the loop still run.
  - id: verify_3
    depends: [code_3]
    working_dir: repo
    continue_on: { skipped: true }
    run: |
      rc=0
      ./scripts/verify.sh > "${DAG_RUN_WORK_DIR}/${DAG_RUN_STEP_NAME}.log" 2>&1 || rc=1
      cat "${DAG_RUN_WORK_DIR}/${DAG_RUN_STEP_NAME}.log"
      exit $rc

  # The steps after the loop run on the last verify. They are trusted: untrusted: false.
  # check_paths keeps a change to a test, a script or the CI out of the push.
  - id: check_paths
    depends: [verify_3]
    untrusted: false
    working_dir: repo
    run: |
      # The globs match a test file or a folder tests/ at any depth. A repository that keeps
      # its tests in other paths adds them.
      changed=$(git status --porcelain -- scripts/ .github/ \
        ':(glob)**/test_*' ':(glob)**/*_test.*' ':(glob)**/tests/**')
      if [ -n "$changed" ]; then
        echo "the agent changed a test, a script or the CI" >&2; exit 1
      fi

  - id: message
    depends: [check_paths]
    action: chat.completion
    with:
      provider: local
      model: mock
      base_url: http://mock-llm:8099/v1
      stream: false
      prompt: "Write a one-line commit message for: ${TASK}"
    output: COMMIT_MSG

  - id: push
    depends: [message]
    working_dir: repo
    untrusted: false
    secrets: [GIT_TOKEN]
    with: { shell: bash }
    run: |
      git checkout -b "${BRANCH}"
      git add -A
      git -c user.name=dagu -c user.email=dagu@localhost commit -m "$COMMIT_MSG"
      git -c credential.helper='!f() { echo username=git; echo "password=$GIT_TOKEN"; }; f' push origin "${BRANCH}"
```

The run goes to an agent worker with the label `agent.provider.claude`. The steps `clone` and
`push` are the only steps that get `GIT_TOKEN`.

The step `message` writes the commit message from the text of the task, not from an agent result.
A model can still write any text, so the step `push` reads the reply the way it reads agent data:
only as `"$COMMIT_MSG"`, in a `run` that `bash` runs ([Spec 104](104-agent-output.md)). Dagu does
not put the reply in the script and does not expand a `$NAME` in it, so the reply cannot run as
code or pull `GIT_TOKEN` into the message.

The step `code_1` gives Claude Code the DAGs `lookup-ticket` and `ops_notify` through the tool
bridge of Spec 091, and the MCP server `docs` at `MCP_URL`. A call of a Dagu tool runs a trusted
child run on the agent worker. The agent sets `CUSTOMER_ID` and `text`, so each tool reads its
input only as `"$NAME"` in a `run` that `bash` runs. Rounds 2 and 3 have the built-in tools only.

The tool `lookup-ticket.yaml`:

```yaml
description: Look up a ticket by the ID of its customer
params:
  - name: CUSTOMER_ID
    type: string
    description: Customer ID
    required: true
steps:
  - id: lookup
    with: { shell: bash }
    run: jq -nc --arg id "$CUSTOMER_ID" '{id: $id, plan: "gold", open_tickets: 2}'
    output: CUSTOMER
```

The tool `ops_notify.yaml`:

```yaml
description: Post a message on the ops channel
params:
  - name: text
    type: string
    description: Message to post
    required: true
steps:
  - id: post
    with: { shell: bash }
    run: printf '%s\n' "$text"
```

### How the verify loop works

The loop needs no sub-DAG and no new key. It is a chain of rounds in one DAG:

- `repeat_policy` repeats one step, and a group of steps does not repeat. The agent step and the
  verify step are two steps.
- A sub-DAG is another run. An agent worker takes one run and exits, so a sub-DAG run that needs a
  provider goes to another agent worker, which does not have the repository.

So each round is a pair of steps, and the number of rounds is fixed. Use 2 or 3. The whole loop is
one run, so it stays on one agent worker, with the repository that the first step cloned.

Round `n` has the step `code_<n>` (an `agent.run` step) and the step `verify_<n>` (a step that runs
the verify script). These keys link the rounds:

| Step | Keys | Effect |
| --- | --- | --- |
| `verify_<n>`, not the last | `depends: [code_<n>]`, `continue_on: {exit_code: [1]}`, and a `run` that exits 1 for each failure of the script | A failed verify script does not stop the run. A verify step that fails in another way stops it. |
| `code_<n+1>` | `depends: [verify_<n>]`, and a precondition with `condition: "${verify_<n>.exit_code}"`, `expected: "0"` and `negate: true` | The step runs only when `verify_<n>` failed. Otherwise Dagu skips it. |
| `verify_<last>` | `depends: [code_<last>]`, `continue_on: {skipped: true}` | A failed verify fails the run. A skipped verify does not stop the steps after the loop. |
| The steps after the loop | `depends: [verify_<last>]` | They run once, after the last verify. |

Dagu records the exit code of a step. The step cannot write it as an output.

A verify step continues only on exit code 1, not on every failure. `${verify_<n>.exit_code}` is `0`
for a step that fails before its process starts, and can be `0` for a step whose script passed and
that then fails a check of [Spec 103](103-untrusted-steps.md#isolation). With
`continue_on: {failure: true}`, the precondition would skip the next round after such a failure,
and the steps after the loop would push a change that no verify passed.

The outcomes:

| What happens | Result |
| --- | --- |
| A verify passes in round `n`. | Dagu skips each later `code` and `verify` step. The steps after the loop run. |
| A verify script fails, and a round remains. | The step exits 1, so the next `code` step runs, then its verify. |
| A verify step fails, and its script did not exit 1: it fails before its script starts, fails a check of Spec 103 after its script passed, or times out. | The next steps do not run, and the run fails. Nothing is pushed. |
| The last verify fails. | The run fails. The steps after the loop do not run, so nothing is pushed. |
| A `code` step fails, also after its retries. | The next steps do not run, and the run fails. The `verify` step of this round does not run. |

| Run | Final status |
| --- | --- |
| No verify failed. | `succeeded` |
| A verify failed, and a later verify passed. The push is done. | `partially succeeded` |
| The last verify failed. | `failed` |

`dagu start` returns exit code 1 for a `partially succeeded` run, because a step failed.

**The log.** A `verify` step writes the output of the script to
`${DAG_RUN_WORK_DIR}/verify_<n>.log` and prints it to its own log. The prompt of `code_<n+1>` holds
the path of this file, not its text, and the agent reads the file with its `read` tool. The
restricted user writes the file in the work directory of the run. The log is agent data, because
an untrusted step wrote it, so no trusted step puts it in a command. The agent
starts the next round from the files that the previous round left. It has no other memory of the
earlier round.

**What the verdict proves.** The verdict is the exit code of the verify script. The verify runs the
script, the tests and the code they test, and the agent can change each of them. An untrusted step
can also change a file and put it back before a later step checks it. So no check of the files
after the verify shows that the verdict is true. The verdict tells whether an agent that works as
asked fixed the code. An agent that a prompt controls can make a verify pass.

The step `check_paths` refuses a change to the verify script, the tests or the CI that is still in
the files when it runs, so the push carries no such change. A review of the pushed branch decides
whether the change is used.

**Limits of the loop.**

- To add a round, copy a pair, change `continue_on` of the old last verify to `{exit_code: [1]}`,
  and change the `depends` of the steps after the loop.
- Each round is a full agent run. The cost in tokens and time is up to the number of rounds times
  the cost of one round. `timeout_sec` and `max_tool_iterations` apply to each round.
- `max_active_runs: 1` and the lock of untrusted steps cause no problem. The loop is one run, and
  its steps run one after the other.

**Existing behavior that the loop relies on.** Dagu has this behavior without this spec:

- `repeat_policy` repeats one whole step, and each repetition is a new execution of the step. A
  group of steps does not repeat.
- A parent step reads the outputs that a sub-DAG publishes (`outputs.write` or `stdout.outputs`) as
  `${steps.<id>.outputs.<name>}`. A plain `output:` variable of the sub-DAG is not an output of the
  parent step.
- The output limit is `max_output_size` of the DAG, 1048576 bytes by default. An `output:` capture
  or a published output above the limit fails the step. Dagu does not cut the value. Keep a diff in
  a file of the work directory, or in an artifact, and publish its path.
- A `repeat_policy` condition cannot read `${steps.<id>.outputs.<name>}` of its own step (Spec 007,
  reason `self_reference`). The reference stays as text. Use a command condition that reads the
  stdout file of the step.
- A step with `continue_on: {exit_code: [1]}` that exits 1 stays `failed` in the run view, and the
  next step runs. A step that fails before its process starts has no exit code, so it never matches
  `exit_code`, and `${<id>.exit_code}` is `0` for it.
- A precondition on `${verify_1.exit_code}` with `expected: "0"` and `negate: true` skips the next
  step when the exit code is 0, and runs it for every other exit code.
- A skipped step makes the steps that depend on it skip, until a step has
  `continue_on: {skipped: true}`. A failed step makes the steps that depend on it `aborted`.
- `${DAG_RUN_STEP_NAME}` is the name of the current step. A step with an `id` and no `name` has its
  `id` as its name. A `run` can use it in a file name in `${DAG_RUN_WORK_DIR}`, so the same text
  serves each `verify` step.

### Configuration of an agent worker

```yaml
peer:
  insecure: false
  cert_file: /etc/dagu/tls/agent-claude.crt
  key_file: /etc/dagu/tls/agent-claude.key
  client_ca_file: /etc/dagu/tls/ca.crt
worker:
  coordinators: [dagu-coordinator:50055]
untrusted:
  uid: 10001
  gid: 10001
env_passthrough:
  - HTTP_PROXY
  - HTTPS_PROXY
  - NO_PROXY
  - http_proxy
  - https_proxy
  - no_proxy
  - CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC
  - DISABLE_TELEMETRY
  - DISABLE_AUTOUPDATER
  - DISABLE_ERROR_REPORTING
```

```text
dagu worker --config /etc/dagu/config.yaml --agent claude=/usr/local/bin/claude
```

### Errors at start

```text
$ dagu worker --agent claude=/usr/local/bin/claude --worker.max-active-runs 4
--agent: key "worker.max_active_runs" is set to "4": an agent worker always runs one run

$ dagu worker --agent claude=/opt/none/claude
--agent: provider "claude": binary /opt/none/claude does not exist

$ dagu worker --agent claude=/usr/local/bin/claude --peer.insecure=true
--agent: key "peer.insecure" is true: an agent worker needs mutual TLS to the coordinator
```

## Open items

Each item has a default. The default applies until the owner decides.

| Item | Default |
| --- | --- |
| Switching user in a pod of the level `restricted`, which forbids `CAP_SETUID`. A path with no capability, such as Landlock, is a candidate. | Level `baseline`, user ID 0 with six capabilities, `hostUsers: false` when the cluster supports it. A worker that cannot switch user stops at start. |
| The Kubernetes restart delay after each container exit, also with code 0. | A `Deployment`. A controller that starts one Job per run is a later item. |
| The files of an earlier run on a Kubernetes pod. A container that restarts in the same pod keeps its `emptyDir` volumes. | The files stay. A worker that empties `/tmp` and its run folders before it polls, or one Job per run, is a later item. |
| A real rate limit with `retry_policy` and `exit_code: [75]`. | Not verified. |
| The name of the label `agent.provider.<provider>`. | As in this spec. |

## Conformance

Black-box tests go in `conformance/spec092_agent_workers/`. They use static fixtures, test
certificates, a fake CLI and a fake model API. No test is there yet.

A test that starts an agent worker needs Linux, user ID 0 and the no-new-privileges flag, like the
tests of the restricted user in [Spec 103](103-untrusted-steps.md#conformance). It skips on other
hosts.
