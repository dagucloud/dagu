# Spec 092: Agent Workers

## Status

Partially implemented.

Implemented:

- the option `--agent` of `dagu worker`, its checks at start, one run and the exit
- the implied selector, the dispatch of a run that needs a provider, the match rules and the reserved
  labels
- the worker pools and the dispatchers of the coordinator
- the step key `secrets`, the step scope and the registry secrets of a step
- the step key `untrusted` and its default in a DAG that has an `agent.run` step
- the rules of an untrusted step in each process, and the isolation with the restricted user
- the verify loop: rounds of `code_<n>` and `verify_<n>` in one DAG
- the key injection of `dagu egress-proxy serve`, with an API key or an OAuth token
- the destinations of the proxy that need the proxy credential
- the examples in `deploy/agent-worker/` and the demo in `examples/agent-worker-demo/`

Verified with real runs on 2026-11-03, with the model `claude-haiku-4-5` and an OAuth token
behind the proxy:

- the real mode of the demo (`run-real.sh`): Claude Code 2.1.311 fixed a test, and a trusted
  step pushed a branch to GitHub
- the extra tests of the demo (`extras/`): `max_tool_iterations`, a retry of a run, and a request to
  read the credentials
- the verify loop of the demo (`run-real.sh loop`), in two runs. The verify failed after round 1.
  Claude Code read the log of the verify in round 2 and fixed the second bug. A trusted step pushed
  one branch.
- the tool bridge of Spec 091, in a separate stack. The stack had a local `dagu start` with the
  restricted user, the egress proxy, and a Dagu server as the MCP server. Claude Code 2.1.311 reported
  the servers `dagu` and `docs` as `connected`. It called a DAG tool with a fixed param and a param
  `from_ai`, and the MCP tool `dagu_read`. Dagu ran the DAG as a child run. Without `127.0.0.1` in
  `NO_PROXY`, the proxy refused the requests to the bridge, and the attempt failed with the reason
  `bridge_unavailable`.

The AI gateway of [Spec 094](094-ai-gateway.md) is implemented. See [The AI
gateway](#the-ai-gateway).

Not verified:

- the manifests for Kubernetes on a cluster. They have a schema check only.
- a real rate limit with `retry_policy` and `exit_code: [75]`
- a verify loop in which each round fails with the real CLI. The fake CLI covers such a loop.
- the tool bridge on the agent worker of the demo (`run-demo.sh`, `run-real.sh`, and the extras
  `extra-cli-tools` and `extra-cli-limits`)

The tests and the main demo use a fake CLI and a fake model API.

This spec is the companion of [Spec 091: Agent Node](091-agent-node.md) and [Spec 090: Agent
Tools](090-agent-tools.md). Spec 090 defines the tools that an `agent.run` step selects. Spec 091
defines the tool bridge that gives the Dagu tools to the CLI agent.

## Scope

This spec defines:

- the agent worker: the option `--agent` of `dagu worker` and the rules that it sets
- the routing of a run that needs a CLI agent to an agent worker
- the worker pools of the coordinator: the labels that a client certificate allows
- the step key `secrets`: the secrets that one step gets
- the step key `untrusted` and the restricted user that runs an untrusted step
- the key injection of the egress proxy
- the network and credential rules that a deployment of agent workers follows
- the threat model of an agent run

This spec does not define:

- the keys of `agent.run`, the typed result and the failure kinds. See Spec 091.
- the tools and the skills of an `agent.run` step. See Spec 090 and Spec 091.
- the secret providers. See [Spec 069](069-secrets-providers.md).
- a controller that starts workers. The deployment starts them.

## Goal

A workflow author chains a CLI agent, a test script and a git push in one DAG and one run. The
agent never sees the token of the push or the API key of the model. The author writes no image, no
network rule and no container option in a step. The deployment fixes the image, the network and
the credentials.

## Terms

An agent worker is a `dagu worker` process that has the option `--agent`.

A general worker is a `dagu worker` process that has no option `--agent`.

A CLI provider is a provider of Spec 091, such as `claude`.

A DAG needs a provider when the DAG has an `agent.run` step with that CLI provider. An agent run is
a run of a DAG that needs a provider.

A worker pool is a named set of certificate identities and of the labels that they allow. The
identity of a client certificate is the Common Name (CN) of its subject.

An untrusted step is a step that runs code that an agent wrote or controls. A trusted step is any
other step of the run.

A step that runs a process is a step with `run`, `command` or `script`, or with the action
`harness.run`.

The restricted user is the Unix user and group that run an untrusted step.

The work directory of a run is the directory that `DAG_RUN_WORK_DIR` names.

The proxy is the egress proxy of the deployment. It limits the destinations and adds the real
credential of the model API.

## Threat model

### Assets

- the secrets of the DAG, such as a git token
- the API key of the model
- the client certificate, the key and the configuration of the worker
- the history of the repository that a trusted step writes, such as a push
- the runs of other DAGs

### Trust

| Party | Trust |
| --- | --- |
| The author of the DAG and the code of a trusted step | Trusted. |
| The Dagu worker process, the coordinator, the server, the scheduler and the proxy | Trusted. |
| The deployment: the image, the certificates, the network, the restart policy | Trusted. |
| The CLI agent, each process that it starts, and an untrusted step | Not trusted. A prompt, a file of the repository or a tool result can control it. |
| A process that has a client certificate | Trusted for the pool of its certificate only. |

### Threats and controls

| Threat | Control | What remains |
| --- | --- | --- |
| The agent reads a secret of the DAG. | The step gets no secret. The restricted user cannot read the memory, the environment or the files of the worker. The coordinator gives a registry secret only to the step that lists it. | None for a secret that the agent step does not list. A step cannot list one. |
| The agent reads or misuses the API key of the model. | A step of the provider `claude` names an endpoint. The key stays in the coordinator, and the agent has the secret of the relay only (Spec 094). For another CLI provider, the agent has a placeholder, and the proxy adds the real key. | The agent can send requests to the model API at the cost of the owner of the key. |
| The agent sends data out. | The worker reaches only the coordinator and the proxy. The proxy allows a list of destinations. A destination that needs the proxy credential is closed to the agent. | Data can go to an allowed destination, such as the model API or the git server. |
| The agent changes `.git` so that a later trusted step runs its code (hooks, `core.fsmonitor`, a new `.git`). | The restricted user cannot write `.git` and cannot rename or replace it. | A trusted step that runs a file of the work directory runs code of the agent. The author does not do this. |
| The agent leaves a process that watches a later step. | At the end of an untrusted step, Dagu stops each process of the restricted user. Untrusted steps run one at a time. | None. |
| The agent connects to the coordinator. | The coordinator needs a client certificate. The restricted user cannot read it. | None. |
| The agent calls a tool of the tool bridge with wrong arguments. | The tools are DAGs of the author. The bridge needs the secret of the attempt. The agent cannot set a fixed input of `params`, or an input that the tool does not show. The child run gets the inputs as one JSON object. | A tool that trusts its arguments. The author designs each tool for any argument. |
| A general worker claims an agent run, or an agent worker claims another run. | The coordinator takes the labels from the pool of the certificate. An agent pool claims only runs of its provider. | A stolen certificate of a pool acts as that pool. |
| Files or processes of one run reach the next run. | An agent worker takes one run, then exits. The deployment gives a new file system for each start. | None in the examples. A deployment that keeps a volume keeps its files. |
| The agent returns a false result. | The typed result fixes the form. A gate that Dagu runs and `human.task` protect an action. | The content of a result can be false. |
| The agent breaks out of the restricted user with a kernel flaw. | Out of scope. A user namespace or a runtime such as gVisor reduces the risk. | The flaw. |

## The agent worker

### Start

```text
dagu worker --agent <provider>=<path>
```

`<provider>` is a CLI provider of Spec 091. `<path>` is the absolute path of its binary in the image
of the worker. The image holds the binary and each package that the agent and the tests need. Dagu
installs nothing at run time.

The worker checks these items before it connects to the coordinator. A failed check stops the
worker with the exit code 1. The error names the key or the option, the expected value and the
value.

1. The option has the form `<provider>=<path>`. The worker has one option `--agent`.
2. The provider is a CLI provider. The path is absolute.
3. The path is a regular file. The restricted user can run it.
4. The configuration does not set `worker.max_active_runs`. The flag `--worker.max-active-runs`,
   the variable `DAGU_WORKER_MAX_ACTIVE_RUNS` and the key of the configuration file are each an
   error:

   ```text
   --agent: key "worker.max_active_runs" is set to "4": an agent worker always runs one run
   ```

5. The connection to the coordinator uses mutual TLS. `peer.insecure` is `false`,
   `peer.skip_tls_verify` is `false`, and `peer.cert_file`, `peer.key_file` and
   `peer.client_ca_file` are set.
6. The configuration sets `untrusted.uid` and `untrusted.gid`. See [The restricted
   user](#the-restricted-user).
7. The process can change to the restricted user. Its effective user ID is 0, and its effective
   capabilities include `CAP_SETUID`, `CAP_SETGID`, `CAP_CHOWN`, `CAP_FOWNER`, `CAP_DAC_OVERRIDE`
   and `CAP_KILL`. The error names the first capability that is absent.
8. The process has the flag no-new-privileges (`NoNewPrivs: 1` in `/proc/self/status`).

### Rules that the option sets

The option `--agent` sets these rules. No other option or key changes them.

- The worker runs one run at a time. It has one poller.
- The worker takes one run. After it acknowledges one claim, it polls no more.
- The worker announces the label `agent.provider.<provider>` with the value `true`, next to the
  labels `os` and `arch` and the labels of `worker.labels`.
- The worker takes only runs that need its provider. See [Routing](#routing).

### End

The run ends with a final status. The worker sends the final status, the logs and the artifacts to
the coordinator. Then the worker exits with the code 0. The status of the run does not change the exit
code. A failure to send the final status gives the exit code 1.

A stop signal before a claim stops the worker with the code 0. A stop signal during the run stops
the run as on a general worker. Then the worker sends the status and exits.

Before it exits, the worker tells the coordinator that it is gone. Its last heartbeat marks it
offline. This heartbeat has the checks of a heartbeat, so only the pool of the worker ID can mark
the worker offline. The coordinator then does not count the worker as available, and a new run
waits for the next worker. A worker that cannot send this heartbeat stays available for 30 seconds
after its last heartbeat.

The deployment starts a new worker after each exit. Dagu does not start it. A run that a worker
claimed and did not end follows the rules of a lost worker of the coordinator.

A retry of a step stays in the run, on the same worker. A retry of a run is a new task for any agent
worker of the provider.

## Routing

### Implied selector

A DAG that needs the provider `<P>` has the implied label `agent.provider.<P>: "true"`. Dagu adds it
to the `worker_selector` of the DAG. Dagu takes the steps of the DAG and the steps of the body of
each `foreach` step. It also takes the handler steps: init, success, failure, abort, exit and wait. A step of a
sub-DAG does not count for the parent.

A DAG that needs two providers is a validation error, because one agent worker runs the whole run:

```text
the DAG needs the providers "claude" and "codex": a run goes to one agent worker
```

A DAG that needs a provider cannot have `worker_selector: local` and cannot have a step with
`approval`. An agent worker takes one run and exits, so it cannot wait for a person. Put the
approval in a parent DAG.

### Dispatch

A process has a coordinator when its key `coordinator.enabled` is true. This is the default. The
process does not check that the coordinator answers, so an outage never moves an agent to the host
of the scheduler.

When the process has a coordinator, a run of a DAG that needs a provider always goes to the
coordinator. This is the same path as for a DAG with a `worker_selector`. A sub-DAG run that needs a
provider goes to the coordinator too, with its own implied selector. `dagu retry` and `dagu restart`
run a DAG in the local process, so they refuse a DAG that needs a provider.

The dispatch of a DAG that uses skills carries them. The coordinator adds every file of each skill
of the DAG to the workspace bundle of the run. The agent worker reads the skills from the bundle. It
does not read the skills directory of the coordinator. The coordinator checks the hash of each skill
after it builds the bundle. A skill that changed in that time fails the dispatch, and the error names
the skill ([Spec 096](096-skills.md), "The bundle"). A skill that lies outside the working
directory of the DAG fails the dispatch, and the error names the file.

A process with `coordinator.enabled: false` runs the DAG itself. The CLI agent then runs on the
host, as a process of the host user. This is the mode for development. It needs `untrusted.isolation:
off`. See [Without isolation](#without-isolation).

### Match

A task matches a worker when each label of the selector of the task has the same value on the
worker. This rule does not change. This spec adds one rule: a worker that has a label
`agent.provider.<P>` matches only a task whose selector has the label `agent.provider.<P>`.

The result:

- An agent run goes only to an agent worker of its provider.
- A run that needs no provider never goes to an agent worker.

The coordinator, the scheduler and the check of the available workers use the same rules.

### No match

A run with no worker that matches waits, as a run with an unmatched `worker_selector`, for
`coordinator.worker_wait_timeout`. The condition of the run and the final error name the provider:

```text
no agent worker serves the provider "claude": the selector is {agent.provider.claude: "true"}
```

A worker can stop after its last heartbeat and before the coordinator sees it as stale. A run that
the coordinator accepts in that time also waits for `coordinator.worker_wait_timeout`, or for 90
seconds when this key is 0. The next matching worker takes it. If no worker takes it, the run
fails, and the error names the provider. The start of a run does not fail when no worker takes the
run during the start request.

An agent worker takes one run and restarts. With `coordinator.worker_wait_timeout` at 0, the
coordinator refuses a run that comes while the worker restarts. So a coordinator with an agent pool
and this key at 0 writes a warning to its log at start. Set a wait above 0.

### Reserved labels

Dagu sets the labels that start with `agent.`. These cases are errors:

- a key of `worker_selector` that starts with `agent.` in a DAG or a step. `dagu validate`
  rejects it.
- a key that starts with `agent.` in a selector of a run, from the API or the option
  `--worker-label`. The start of the run fails.
- a key of `worker.labels` that starts with `agent.`. The worker stops at start.

## Worker pools

### Configuration

The coordinator binds the labels of a worker to its client certificate. The key
`coordinator.worker_pools` of the Dagu configuration is a list of pools.

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

The key `coordinator.dispatchers` is a list of identities. A dispatcher is a process that sends
runs to the coordinator, such as the server, the scheduler or the command line.

| Key | Type | Rule |
| --- | --- | --- |
| `dispatchers` | list | Required with `worker_pools`. Not valid without `worker_pools`. Not empty. Each item is the CN of a client certificate, once. No identity of a pool. |

The schema is closed. An unknown key, a duplicate key or a wrong type stops the coordinator at
start. These cases also stop it at start:

- `worker_pools` without `peer.cert_file`, `peer.key_file` and `peer.client_ca_file` on the
  coordinator. The error names `coordinator.worker_pools` and the key that is absent.
- `worker_pools` without `dispatchers`, or `dispatchers` without `worker_pools`.
- an identity that is a dispatcher and a member of a pool. An identity has one role.
- a label `agent.provider.<P>` with a value other than `true`, or a `<P>` that is not a CLI
  provider.
- a label key `os` or `arch`. Every pool allows these labels with any value. Dagu sets them.
- two labels `agent.provider.<P>` in one pool. An agent worker serves one provider.
- a label key that starts with `agent.` and is not `agent.provider.<P>`.

The keys have no variable of the environment.

A pool with a label `agent.provider.<P>` is an agent pool.

`dagu config` prints the pools (the name, the identities and the labels) and the dispatchers.

### Behavior

With `worker_pools`, each call of a worker to the coordinator MUST come from a certificate whose
identity is in a pool. These are the calls of a worker:

- the poll, the heartbeat, the acknowledgment of a claim and the heartbeat of a run
- the status report, and the streams of logs and of artifacts
- the resolution of a secret or of a runtime profile
- the claim and the end of the cleanup of an agent session
- the calls of the AI gateway, `CheckAIEndpoint` and `RelayAIRequest`

A call from another identity fails with the gRPC code `PermissionDenied`:

```text
certificate identity "rogue" is in no worker pool
```

Each label that a worker announces in a poll or a heartbeat MUST be allowed by its pool. A label
that the pool does not allow fails the call with `PermissionDenied`. The error names the label, its
value and the pool. The coordinator keeps no label of a refused call. A worker of an agent pool MUST
announce the label `agent.provider.<P>` of its pool with the value `true`. A poll or a heartbeat
without it fails with `PermissionDenied`.

The coordinator records the pool of the worker that claims a task in the claim and in the lease of
the attempt. Each later call for this attempt MUST come from a certificate of the same pool. A call
from another pool fails with `PermissionDenied`. A status report for an attempt with no lease,
such as a report after the final status, also fails. A worker ID belongs to the pool of its last
heartbeat. A heartbeat or a cleanup call for that worker ID from another pool fails.

The coordinator sets the labels that start with `agent.` in the selector of each task. It removes
such labels that the caller sends and adds the implied selector of the DAG.

Each other call of the coordinator MUST come from a dispatcher, or from a worker for its own runs.
The other calls are the dispatch, the status and the cancel of a run. They also include the calls of
the DAG, the state, the workspace bundles and the list of the workers. A worker of a pool makes these calls only when its
pool holds a fresh lease:

- the dispatch of a child, and the status and the cancel of a run: a lease of the same root run
- the read of a workspace bundle: a lease of that bundle
- the DAG, the state, the check and the write of a bundle, and the list of the workers: any lease

The dispatch of a root run needs a dispatcher. A call that does not obey these rules fails with
`PermissionDenied`. The error names the identity:

```text
certificate identity "rogue" is not a dispatcher and is in no worker pool
```

Without `worker_pools`, the coordinator accepts the labels that a worker announces, except the
labels that start with `agent.`. A poll or a heartbeat with such a label fails with
`PermissionDenied`:

```text
label "agent.provider.claude" needs coordinator.worker_pools
```

So with no pools, no worker serves an agent run, and an agent run waits, then fails.

## Secrets of a step

### The key

A step can have the key `secrets`. It is a list of names of the `secrets:` block of the DAG.

```yaml
secrets:
  - name: GIT_TOKEN
    ref: git-token
steps:
  - id: push
    secrets: [GIT_TOKEN]
    run: git push origin HEAD
```

`dagu validate` rejects these cases:

- a name that the DAG does not declare. The error names the step and the name.
- a name that occurs two times in one list
- `secrets` on an untrusted step. The error names the step.
- `secrets` on a step that runs a process and has no key `untrusted`, in a DAG that has an
  `agent.run` step. The step is untrusted by default, and the author must write `untrusted: false`.
- a `ref` that starts with `ai/`. The AI feature of Spec 093 owns this prefix, and no DAG can read it.
- in a DAG with step scope, a reference `${NAME}` to a secret in a field of a step that does not
  list `NAME`. The error names the step, the field and the name.

### Step scope

A DAG has step scope when it has an untrusted step or a step with the key `secrets`. In a DAG with
step scope, a step gets only the secrets that its list names. A step with no list gets no secret.
A DAG that has an `agent.run` step always has step scope, because the agent step is untrusted.

In a DAG without step scope, each step gets each secret of the DAG, as in Spec 069.

A secret that a step gets is a variable of the step process and a value for `${NAME}` in the fields
of the step. A secret that the step does not get is absent from both. Dagu masks the values of all
secrets of the run in each log and each record, as in Spec 069.

### Resolution

Dagu resolves the secrets that use a provider (`provider` and `key`) when the run starts, as in Spec
069. A secret that fails stops the run before the first step.

On a worker, a registry secret (`ref`) has two phases:

1. When the run starts, the worker asks the coordinator to check each registry secret, with no
   value. A secret that fails stops the run before the first step.
2. When a step that lists the secret starts, the worker asks the coordinator for the value. The
   request names the step.

The coordinator gives the value only when all these conditions are true:

- the lease of the attempt is valid, as for any secret
- the step is a step of the DAG of the attempt, and its `secrets` list names the secret
- the attempt has no final status, and the step has no final status in the reports of the worker

Otherwise the request fails with `PermissionDenied`, and the step fails. The error names the step
and the secret. It has no value.

When a step ends, the worker removes its secrets from the variables of each later process. The
worker keeps the values in its memory until the run ends, only to mask them.

A process with no coordinator resolves a registry secret when the run starts.

## Untrusted steps

### The key

`untrusted: true` marks a step as untrusted. `untrusted: false` marks it as trusted. The key is valid
on a step that runs a process: a step with `run`, `command` or `script`, and a step with the action
`harness.run`.

An `agent.run` step with a CLI provider is always untrusted. The key `untrusted` on it is
redundant, and `untrusted: false` on it is a validation error.

`dagu validate` also rejects `untrusted` on a step with another action, on a step with `container`,
and in `defaults`. The key `secrets` is not valid in `defaults` either. The error names the key on the
path `defaults.untrusted` or `defaults.secrets`, and tells the author to set it on each step.

### Default of the key

A step that runs a process can omit the key `untrusted`. The DAG decides the value that applies:

| DAG | A step that runs a process and omits `untrusted` |
| --- | --- |
| Has an `agent.run` step | Untrusted. |
| Has no `agent.run` step | Trusted. |

Dagu looks for an `agent.run` step in the steps, in the handler steps and in the bodies of the
`foreach` steps of the DAG. A step of a sub-DAG does not count for the parent. A DAG that has no `agent.run` step does not change: a step is
untrusted only with `untrusted: true`.

In a DAG that has an `agent.run` step, a step that runs a process is untrusted by default. The
reason is the repository. The agent can change the files of the repository, and a step that runs
a process runs them. A step that the author marked as trusted by mistake runs this code with the
secrets and the rights of the worker. With this default, a step that the author forgot runs as the
restricted user and gets no secret.

The author makes a step trusted with `untrusted: false` on the step. The step runs as the process
user, and it keeps its `DAGU_` variables.

The key `secrets` does not change the default. In such a DAG, a step that runs a process and lists
`secrets` must also have `untrusted: false`. Without it, the step is untrusted by default and
cannot have secrets, so the DAG does not load. Dagu gives this error on the path `secrets` of the
step:

```text
step lists secrets but is untrusted by default in a DAG that has an agent.run step; add untrusted: false to run it as the process user, or remove the secrets
```

There are two ways to fix the error. Add `untrusted: false`, and the step runs as the process
user and gets its secrets. Or remove the `secrets`, and the step runs as the restricted user. The
key `untrusted: true` with `secrets` is also a validation error. A DAG that has no `agent.run` step
does not change: the key `untrusted` decides alone.

A trusted step of such a DAG checks or publishes. Examples are the check of the changed files and
the push. It does not run a file of the repository. See [Trusted steps after an untrusted
step](#trusted-steps-after-an-untrusted-step).

The default does not apply to a step with another action, such as `git.checkout`, `http.request` or
`chat.completion`. These steps run no process of the work directory. The key `untrusted` stays
invalid on them. A step in the body of a `foreach` step cannot have the key `untrusted` or the key
`secrets`. The error has the path `<step>.foreach.steps[<i>].untrusted`, or the same path with
`secrets`. Such a step is untrusted by default in such a DAG.

Untrusted steps run one at a time, and alone in their run. In a DAG that has an `agent.run` step,
most steps are untrusted by default, so the steps of the run go one after the other.

Dagu computes the effective value one time, when it loads the DAG. The runtime, the validation of
the output references and the editor read this value. The editor shows it in the section Security
of the step.

### Rules in each process

These rules apply to each untrusted step, in each process:

- The step gets no secret.
- The process of the step does not get the variables whose names start with `DAGU_`, except
  `DAGU_OUTPUT_FILE`.

Preconditions, command substitutions (`$(...)`), the capture of `output` and the handlers of the
step are trusted. Dagu runs them as the process user. The author does not run a file of the work
directory in them.

### Isolation

A process whose configuration has `untrusted.uid` and `untrusted.gid` runs each untrusted step with
isolation. An agent worker always has isolation. A process with neither these keys nor
`untrusted.isolation: off` fails each untrusted step. See [Without isolation](#without-isolation).

1. **One at a time.** The process runs one untrusted step at a time. A step waits for the end of the
   previous untrusted step. It has the status `running` and the log line `untrusted: waiting for the
   restricted user`. While an untrusted step runs, no other step of the same run runs:
   - The untrusted step waits for the end of the running steps of its run. Its log has the line
     `untrusted: waiting for the end of the other steps of the run`.
   - Another step of the run waits for the end of the untrusted step. This includes the
     preconditions and the repeat conditions of a step. Its log has the line `untrusted: waiting
     for the end of the untrusted step`.
2. **Place.** The working directory of the step MUST be in the work directory of the run.
   Otherwise the step fails:

   ```text
   untrusted step "verify": the working directory /srv is not in the work directory of the run
   ```

3. **Files.** Before the process starts, Dagu changes the owner of each file and folder of the work
   directory to the restricted user. These are the exceptions:
   - Each entry named `.git`, and each file and folder in it, keeps the owner of the worker. The
     restricted user can read it and cannot write it.
   - The same rule applies to each git folder. A git folder is the folder that a `.git` file or a
     `.git` link names (`gitdir:`), or the folder that its file `commondir` names. A folder that git
     opens as a repository, with `HEAD`, `objects` and `refs`, is a git folder too. A git folder outside the work
     directory fails the step.
   - The work directory and each folder on the way to a git folder or to an entry `.git` keep the
     owner of the worker. They get the mode `1777`. The restricted user can make and remove its own
     entries in them, and cannot rename or remove an entry of another owner.
   Dagu changes each owner and each mode through a file descriptor that it opens with `O_NOFOLLOW`.
   A link that replaces a path during the change cannot redirect it.
4. **Home.** Dagu makes a new folder for the step, owned by the restricted user, with the mode
   `0700`. `HOME` and `TMPDIR` name it. Dagu removes it when the step ends.

   The restricted user can have write access to the `HOME` of the worker or to a folder of `XDG_`.
   Then a trusted step of the run gets a private `HOME` and private `XDG_` folders. They have the
   mode `0700`, and the user of the worker owns them. The steps of a child run that an untrusted step started
   get them too, and the git variables of [Trusted steps after an untrusted
   step](#trusted-steps-after-an-untrusted-step). So the agent cannot plant a file that a later
   trusted step reads, such as `.gitconfig`.
5. **Process.** The process of the step runs with the user ID and the group ID of the restricted
   user. It has no supplementary group and no capability. It is in a new session, with no
   controlling terminal.
6. **Files of the attempt.** The MCP config of the attempt (Spec 091) holds the secret of the tool
   bridge. It belongs to the restricted user and has the mode `0400`. The folder of the skills
   (Spec 091) and the script of the step are readable by the restricted user and are not
   writable. The file of `DAGU_OUTPUT_FILE` is writable by the restricted user.
7. **End.** The step ends after a success, a failure, a timeout or a stop. Then Dagu sends
   `SIGKILL` to each process of the restricted user. A helper calls `kill(-1, SIGKILL)` as the
   restricted user, when that user can run the binary of Dagu. A scan of each task of `/proc`
   (`/proc/<pid>/task/<tid>`) stops each thread group with a live task of that user. This includes a
   group whose leader is a zombie. Dagu waits until
   no live task of that user is left. Then Dagu changes the owner of each file and folder of the work
   directory back to the user of the worker. Then it reads the outputs of the step.
8. **Checks at the end.** Dagu checks the work directory before it gives it back. For each of these
   entries of the restricted user, it fails the step and removes the entry:
   - a new entry `.git`, or a new entry in a git folder
   - a new folder that git opens as a repository: Dagu removes its files `HEAD`, `commondir` and
     `gitdir`
   - a link to a path outside the work directory or into a git folder
   - a file with more than one link

   A file of the restricted user loses the bits set-user-ID and set-group-ID.

A failure of an action of this list fails the step. The error names the action and the path.

These failures also stop the run, because Dagu cannot give a safe work directory to a later step:

- A task of the restricted user is still alive 30 seconds after `SIGKILL`.
- An entry of a git folder or an entry `.git` changed or is gone.

After such a failure, each later step of a run with an untrusted step fails in the same process.

### The restricted user

The keys `untrusted.uid` and `untrusted.gid` of the Dagu configuration set the restricted user.
The key `untrusted.isolation` turns the isolation off.

| Key | Type | Rule |
| --- | --- | --- |
| `untrusted.isolation` | `on` or `off` | Default `on`. With `on`, an untrusted step needs `untrusted.uid` and `untrusted.gid`. With `off`, it runs without isolation. `off` with `untrusted.uid` or `untrusted.gid` is an error. |
| `untrusted.uid` | integer | Required with `untrusted.gid`. From 1 to 4294967294. Not the user ID of the process. |
| `untrusted.gid` | integer | Required with `untrusted.uid`. From 1 to 4294967294. Not the group ID of the process. |

A value out of the range, one key without the other, a value of the wrong type, or
`untrusted.isolation: off` with a restricted user stops Dagu at start. The error names the key, the
expected range and the value. The user needs no entry in `/etc/passwd`. Only untrusted steps use this
user ID.

The block `untrusted` is file only. If the variable `DAGU_UNTRUSTED` exists in the environment, Dagu
stops at start. The error names the variable and the key, and never shows the value:

```text
config: the environment variable DAGU_UNTRUSTED is not read: set the block untrusted in the configuration file
```

`dagu config` prints the three values.

### Without isolation

Isolation fails closed. Without `untrusted.uid` and `untrusted.gid`, Dagu does not run an untrusted
step as the process user, because the agent controls the code of the step. The step fails before it
starts, with the reason `isolation_required`. The message names the keys:

```text
untrusted step "verify": isolation_required: the configuration sets no untrusted.uid and no untrusted.gid, and untrusted.isolation is on: set untrusted.uid and untrusted.gid, or set untrusted.isolation to off for development
```

The failure is an error of the step, not a failure kind of an attempt. It applies to each untrusted
step, also a step that is untrusted by default and an `agent.run` step.

A process with `untrusted.isolation: off` runs an untrusted step as the process user. The rules in
each process still apply. At start, the process logs a warning that names the key
`untrusted.isolation`. The step log has the line:

```text
untrusted: isolation is off: the key untrusted.isolation is off
```

This is the mode for development on a host, and the mode of the conformance tests that run without a
restricted user. An agent worker (`--agent`) does not start with `untrusted.isolation: off`:

```text
--agent: key "untrusted.isolation" is "off", not "on": an agent worker always has isolation
```

### Trusted steps after an untrusted step

A trusted step reads the files of an untrusted step as data. Each output of an untrusted step is
also data of the agent. This includes the typed result of an `agent.run` step, its `tool_calls`, and
the outputs of each other untrusted step. This includes a step that is untrusted by default. The agent
controls each value. An output of an untrusted step is not a gate. A gate is a check that Dagu
decides, such as the exit code of the tests of the repository.

Dagu puts the value of a reference `${steps.<id>.outputs.<name>}` in the text of a command. A
reference to an output of an untrusted step is a validation error in these fields of a trusted step:
`run`, `command`, `script`, `shell` and `shell_args`. The error names the step, the field and the reference:

```text
step "gate": the field steps[2].run refers to ${steps.code.outputs.tool_calls}, an output of the untrusted step "code"; Dagu would put the value of the agent in the command: pass it through env and use "$VAR" in the command
```

The reference is valid in the `env`, the `preconditions` and the `repeat_policy` of a step, and in
the `with` of an action. Pass the value through `env`, and use `"$VAR"` in the command:

```yaml
  - id: gate
    depends: [code]
    untrusted: false
    env:
      - TOOL_CALLS: ${steps.code.outputs.tool_calls}
    run: printf '%s' "$TOOL_CALLS" | jq -e 'index("shell") != null'
```

The step `gate` has `untrusted: false`. In a DAG that has an `agent.run` step, a step with `run` is
untrusted by default.

In a run with an untrusted step, each process gets `safe.bareRepository=explicit` for git. Dagu adds it
through `GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_<n>` and `GIT_CONFIG_VALUE_<n>`, after the entries that
these variables already have. So git opens a bare repository only when the command names it.

The author checks the changed files in a trusted step before a push. For example, a step refuses a
change below `.github/`:

```yaml
  - id: check_paths
    depends: [verify]
    untrusted: false
    working_dir: repo
    run: |
      if git status --porcelain | cut -c4- | grep -q '^\.github/'; then
        echo "the agent changed .github/" >&2; exit 1
      fi
```

## A verify loop

A DAG can run the agent again when the verify fails. The loop is a chain of rounds in one DAG. It
needs no sub-DAG and no new key of Dagu. The whole loop is one run, so it stays on one agent
worker, with the repository that the first step cloned.

### Why a chain

- `repeat_policy` repeats one step. A group of steps does not repeat. The agent step and the
  verify step are two steps. See [Verified points](#verified-points).
- A sub-DAG is another run. An agent worker takes one run and exits, so a run of a sub-DAG that
  needs a provider goes to another agent worker. That worker does not have the repository.

So the author writes each round as a pair of steps. The number of rounds is fixed. Use 2 or 3.

### The chain

A round `n` has the step `code_<n>` (an `agent.run` step) and the step `verify_<n>` (a step that
runs the verify script). The keys below link the rounds. The complete DAG in [Examples](#complete-dag)
has three rounds.

| Step | Keys | Effect |
| --- | --- | --- |
| `verify_<n>`, not the last | `depends: [code_<n>]`, `continue_on: {failure: true}` | A failed verify does not stop the run. |
| `code_<n+1>` | `depends: [verify_<n>]`, a precondition on `${verify_<n>.exit_code}` | The step runs only when `verify_<n>` failed. Otherwise Dagu skips it. |
| `verify_<last>` | `depends: [code_<last>]`, `continue_on: {skipped: true}` | A failed verify fails the run. A skipped verify does not stop the steps after the loop. |
| The steps after the loop | `depends: [verify_<last>]` | They run after the last verify, once. |

The precondition has `condition: "${verify_<n>.exit_code}"`, `expected: "0"` and `negate: true`.
Dagu records the exit code of the step. The step cannot write it as an output. Each exit code that
is not 0 starts the next round.

Dagu then does this:

- A verify passes in round `n`. Dagu skips each later `code` step and each later `verify` step.
  The steps after the loop run.
- A verify fails and a round remains. The next `code` step runs, and then its verify.
- The last verify fails. The run fails. The steps after the loop do not run, so nothing is pushed.
- A `code` step fails, also after its retries. The next steps do not run, and the run fails. The
  `verify` step of this round does not run.

A run in which a verify failed and a later verify passed ends with the status `partially
succeeded`. The steps after the loop ran, so the push is done. `dagu start` returns the exit code 1
for this status. A run in which no verify failed ends with `succeeded`. A run in which the last
verify failed ends with `failed`.

### The log

A `verify` step writes the output of the script to `${DAG_RUN_WORK_DIR}/verify_<n>.log`, and prints
it to its own log. The prompt of `code_<n+1>` gives this path. The agent reads the file with its
`read` tool. The prompt holds the path, not the text of the log.

The log is data of the agent: an untrusted step wrote it. No trusted step puts it in a command.
The restricted user writes the file in the work directory of the run. The agent starts the next
round from the files that the previous round left.

The verdict is the exit code of the verify script. The script can choose its exit code, and it is
in the repository. So the step `check_paths` refuses a change to the verify script, to the tests
and to the CI.

### Limits of the loop

- The number of rounds is fixed. To add a round, do these steps:
  1. Copy a pair.
  2. Change `continue_on` of the old last verify to `{failure: true}`.
  3. Change the `depends` of the steps after the loop.
- Each round is a full run of the agent. The cost in tokens and in time is up to the number of
  rounds times the cost of one round. `timeout_sec` and `max_tool_iterations` apply to each round.
- The agent gets only the log and the files of the repository. It has no other memory of the
  earlier round.
- `max_active_runs: 1` and the lock of untrusted steps are no problem. The loop is one run, and its
  steps go one after the other.

## The egress proxy

### Key injection

`dagu egress-proxy serve` forwards the requests of a client to the destinations of `--allow` and
refuses the others with the status 403. A name that only a wildcard entry allows, such as
`*.github.io`, gets public addresses only. An entry that names the host or the address reaches any
address. A `CONNECT` tunnel in which no byte moves for 10 minutes ends.

These options add a route to one upstream, with the real key of the model API. A step of the
provider `claude` does not use this route: it names an endpoint (Spec 094). The route serves a CLI
provider other than `claude`.

| Option | Rule |
| --- | --- |
| `--upstream <url>` | The scheme is `http` or `https`. No query and no fragment. The host and port of the URL MUST match an entry of `--allow` or of `--allow-with-credential`. |
| `--upstream-auth <form>` | `x-api-key` (default), `bearer` or `anthropic-oauth`. |
| `--upstream-key-env <name>` | The variable that holds the key. |
| `--upstream-key-file <path>` | The file that holds the key. Dagu removes the last line end. The file has at most 16384 bytes. |

With `--upstream`, give exactly one of `--upstream-key-env` and `--upstream-key-file`. Without
`--upstream`, the three other options are errors. A variable that does not exist, an empty value, a
file that the proxy cannot read, or an empty file stops the proxy at start. The error names the
option and the variable or the path. It never holds the key.

A request in origin form (a path, such as `POST /v1/messages`) goes to the upstream. The proxy:

1. joins the path of the upstream URL and the path of the request, and keeps the query
2. removes the headers `x-api-key`, `authorization` and `proxy-authorization` of the client
3. sets `x-api-key: <key>`, or `authorization: Bearer <key>` with the forms `bearer` and
   `anthropic-oauth`
4. with the form `anthropic-oauth`, adds `oauth-2025-04-20` to the list of `anthropic-beta`
   when the list does not have it, and keeps the betas of the client
5. sends the request through the same rules as the other destinations

The form `anthropic-oauth` is for an OAuth token of a subscription.
Claude Code sends the beta `oauth-2025-04-20` with such a token. A client with a placeholder API
key does not send it.

A request in absolute form and a `CONNECT` request keep their rules. A destination that `--allow`
does not list gets the status 403. Without `--upstream`, a request in origin form gets the status
400.

The proxy never writes the key in a log, an error or a response. The key is never an argument of a
command.

The proxy writes one line for each request to the upstream. The line has the method, the host
and the port, the header of the credential of the client and the form. For the credential of
the client and for the key, it has the first 12 hexadecimal digits of the SHA-256, not the
value. The line has no path, because a path can hold a secret.

```text
egress proxy: allow POST api.anthropic.com:443 (upstream): client credential x-api-key sha256:4097889236a2 replaced by the key sha256:0123456789ab (anthropic-oauth)
```

`4097889236a2` is the start of the SHA-256 of `placeholder`.

### Destinations with a proxy credential

These options open a destination only for a client that sends the proxy credential. A trusted
step, such as a clone or a push, gets the credential as a secret of the step. The agent does
not get it.

| Option | Rule |
| --- | --- |
| `--allow-with-credential <host[:port]>` | A destination, in the form of `--allow`. Repeat the option for each destination. |
| `--proxy-credential-env <name>` | The variable that holds the proxy credential. |
| `--proxy-credential-file <path>` | The file that holds the proxy credential. Dagu removes the last line end. |

With `--allow-with-credential`, give exactly one of `--proxy-credential-env` and
`--proxy-credential-file`. Without `--allow-with-credential`, these two options are errors.
The credential has at least 16 characters and no control character. A destination that can
match an entry of `--allow` and an entry of `--allow-with-credential` is an error, because the
two entries contradict each other. Each error stops the proxy at start. It names the option and
never holds the credential.

A request in absolute form or a `CONNECT` request to such a destination MUST have one header
`Proxy-Authorization: Basic <base64 of user:credential>`. The user name can be any name. A
request without the credential gets the status 407 and the header `Proxy-Authenticate: Basic
realm="dagu egress proxy"`. The proxy dials nothing for it. The proxy removes the header
before it sends a request in absolute form to the destination. A log line tells the result,
without the credential:

```text
egress proxy: deny CONNECT github.com:443: no valid proxy credential
egress proxy: allow CONNECT github.com:443 (proxy credential)
```

The upstream can be a destination of `--allow-with-credential`. The route of the upstream
needs no credential, because the proxy itself opens the connection. A client cannot open a
tunnel to the same host without the credential, so it cannot send a key of its own there.

A trusted step gives the credential to git in the URL of the proxy, in a variable of the step.
The token and the credential are never in an argument:

```yaml
  - id: clone
    untrusted: false
    secrets: [GIT_TOKEN, PROXY_CREDENTIAL]
    env:
      - https_proxy: http://git:${PROXY_CREDENTIAL}@egress-proxy:8081
      - GIT_HTTP_PROXY_AUTHMETHOD: basic
    run: |
      git -c credential.helper='!f() { echo username=x-access-token; echo "password=$GIT_TOKEN"; }; f' clone "$REPO" repo
```

### The agent side

A step with the provider `claude` MUST give the block `endpoint` ([Spec 091](091-agent-node.md#endpoint)),
with the name of an endpoint and a model ([Spec 091](091-agent-node.md#model)). For such a step,
`ANTHROPIC_BASE_URL` is the address of the relay of the attempt, and `ANTHROPIC_API_KEY` is the secret
of the relay. Dagu sets them. The CLI gets no other variable that starts with `ANTHROPIC_` or
`CLAUDE_CODE_USE_`, and no `CLAUDE_CODE_OAUTH_TOKEN`, from the environment. The model traffic does not go through the proxy.

The deployment gives these variables to the agent worker. The option `env_passthrough` of the Dagu
configuration passes them to the steps.

| Variable | Value |
| --- | --- |
| `HTTP_PROXY`, `HTTPS_PROXY`, `http_proxy`, `https_proxy` | The URL of the proxy, for the allowed destinations. `git` reads only the lower-case `http_proxy`. |
| `NO_PROXY`, `no_proxy` | The host of the coordinator, the host of the proxy, `127.0.0.1` and `localhost`. |
| `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`, `DISABLE_TELEMETRY`, `DISABLE_AUTOUPDATER`, `DISABLE_ERROR_REPORTING` | `1`. |

A step cannot list a secret for a CLI agent. A CLI provider other than `claude` gets its model
credential from the deployment. It has a placeholder in its variables, and the proxy adds the real key
on the route of the upstream. See [Key injection](#key-injection).

Claude Code sends the requests of the tool bridge through `HTTP_PROXY` when `NO_PROXY` does
not name `127.0.0.1`. The proxy refuses them, and the step fails with the reason
`bridge_unavailable`. So `NO_PROXY` names `127.0.0.1` and `localhost`. The host of an inline `mcp`
entry goes through the proxy unless `NO_PROXY` names it too, so the proxy must allow it. A named
`mcp` entry goes to the relay on `127.0.0.1`, and the coordinator calls its server. The proxy never
sees that call. See [Spec 095](095-mcp-servers.md#mcp-servers-with-credentials).

An OAuth token of a subscription goes in an endpoint of the form `anthropic-oauth` (Spec 093). The
worker never holds it.

## The AI gateway

[Spec 094](094-ai-gateway.md) defines the gateway. This section links it to this spec.

The proxy above keeps the real key of the model API in the proxy. The gateway of Spec 094 is a second
way. A step that has the block `with.endpoint` calls a relay in the worker. The relay sends the request through
the gRPC channel of the worker to the coordinator. The coordinator adds the key and calls the
provider. The key stays in the coordinator.

These rules apply to an agent worker:

- The relay listens on `127.0.0.1` and a random port, for one attempt. The CLI gets its secret in a
  variable. For a named MCP entry, the MCP config of the attempt holds the secret, with the mode `0400`,
  and the restricted user owns it. The relay is like the tool bridge of Spec 091.
- Dagu adds `127.0.0.1` and `localhost` to `NO_PROXY` of a step that uses the relay. The deployment
  does not need to set them for this step.
- The worker announces the capability `ai_relay.v1`. A run with a step that names an endpoint goes
  only to a worker that has it. A worker that lacks it never takes such a run.
- The gateway needs the mutual TLS that an agent worker already has. It uses the pool and the lease
  checks of [Worker pools](#worker-pools) and of [Resolution](#resolution).
- The coordinator needs outbound access to the provider. The worker still reaches only the
  coordinator and the proxy.

A step with the provider `claude` MUST use an endpoint, so its model traffic needs no model route in
the proxy. The options `--upstream`, `--upstream-auth` and `--upstream-key-*` stay valid for the other
CLI providers.

The proxy stays in use for non-model traffic. A trusted step uses it for the clone and the push of
git, for example to `github.com`. A step of a CLI provider other than `claude` uses the proxy as described above.

## Deployment

### Network

The container or the pod of an agent worker reaches only the coordinator and the proxy. It has no
other route. The proxy reaches the allowed destinations. The proxy is a separate container on
Docker and a separate pod on Kubernetes. A proxy in the same pod as the worker does not limit the
worker, because the pod has one network namespace.

### Security settings

| Property | Docker Compose | Kubernetes |
| --- | --- | --- |
| User | `user: "0:0"` | `runAsUser: 0`, and `hostUsers: false` when the cluster supports user namespaces |
| Capabilities | `cap_drop: [ALL]`, `cap_add` of the six capabilities of [Start](#start) | `drop: [ALL]`, `add` of the same six |
| No new privileges | `security_opt: [no-new-privileges:true]` | `allowPrivilegeEscalation: false` |
| Root file system | `read_only: true`, `tmpfs` with the option `exec` for `/tmp` and the Dagu home | `readOnlyRootFilesystem: true`, `emptyDir` with `medium: Memory` |
| Seccomp | The default profile | `seccompProfile: RuntimeDefault` |
| Account token | None | `automountServiceAccountToken: false` |
| Network | An internal network with the coordinator and the proxy | A `NetworkPolicy` that allows the coordinator, the proxy and DNS |
| Certificate | A read-only file, owner 0, mode `0400` | A `Secret` volume with `defaultMode: 0400` |
| Restart | `restart: always` | A `Deployment` |

The worker starts as user ID 0 because the change to the restricted user needs `CAP_SETUID`. The
Pod Security Standard `restricted` forbids it. The examples use the level `baseline`. See [Open
items](#open-items).

The repository has the examples:

- `deploy/agent-worker/Dockerfile`: the image of an agent worker, with a static `dagu`, `git`,
  `python3` and the CLI of the agent
- `deploy/agent-worker/compose.yaml`: the coordinator, a general worker, an agent worker and the
  proxy
- `deploy/agent-worker/k8s/`: the manifests for Kubernetes
- `examples/agent-worker-demo/`: a demo for one host, with a fake CLI and a fake model API. Its real
  mode `run-real.sh` uses Claude Code, the Anthropic API and a GitHub repository. Its DAG
  `agent-dev-isolation` probes the isolation of an untrusted step.

## Verified points

- `repeat_policy` on an `agent.run` step repeats the whole step. Each repetition is a new execution,
  with new attempts. A group of steps does not repeat.
- A parent step reads the outputs that a sub-DAG publishes (`outputs.write` or `stdout.outputs`)
  as `${steps.<id>.outputs.<name>}`. A plain `output:` variable of the sub-DAG is not an output of
  the parent step.
- The limit of an output is `max_output_size` of the DAG, 1048576 bytes by default. An `output:`
  capture above the limit fails the step, and a published output above the limit fails the step
  too. Dagu does not cut the value. Keep a diff in a file of the work directory, or in an artifact,
  and publish its path.
- A `repeat_policy` condition cannot read an output of its own step (Spec 007). The reference stays
  as text. Use a command condition that reads the stdout file of the step.
- A step with `continue_on: {failure: true}` stays `failed` in the run view, and the next step
  runs. A precondition on `${verify_1.exit_code}` with `expected: "0"` and `negate: true` skips the
  next step when the exit code is 0. It runs the next step for each other exit code. A skipped step makes
  the steps that depend on it skip, until a step has `continue_on: {skipped: true}`. A failed
  step makes the steps that depend on it `aborted`. The conformance tests of the loop check these
  statuses.
- `${DAG_RUN_STEP_NAME}` is the name of the current step. A `run` can use it in the name of a file
  in `${DAG_RUN_WORK_DIR}`, so the same text serves each `verify` step.

## Errors

`dagu validate` rejects these cases with a nonzero exit code:

- a DAG that needs two CLI providers
- `worker_selector: local`, or a step with `approval`, in a DAG that needs a provider
- a key of `worker_selector` that starts with `agent.`
- a key of a step `worker_selector` with a value reference, such as `${K}`
- a name in a step `secrets` list that the DAG does not declare, or that occurs two times
- `secrets` on an untrusted step, also `untrusted: true` with `secrets`
- `secrets` on a step that runs a process and has no key `untrusted`, in a DAG that has an
  `agent.run` step
- a reference `${NAME}` to a secret that the step does not get, in a DAG with step scope
- `untrusted` on a step that does not run a process, on a step with `container`, or in `defaults`
- `untrusted: false` on an `agent.run` step with a CLI provider
- a reference to an output of an untrusted step in the `run`, the `command` or the `script` of a
  trusted step. A step that is untrusted by default is an untrusted step.

These cases stop a process at start with the exit code 1:

- each failed check of [Start](#start) for an agent worker
- a key of `worker.labels` that starts with `agent.`
- an invalid `coordinator.worker_pools` or `coordinator.dispatchers`, or one of these keys
  without the other
- an invalid `untrusted.uid`, `untrusted.gid` or `untrusted.isolation`, or `untrusted.isolation: off`
  with a restricted user
- the variable `DAGU_UNTRUSTED` in the environment
- an invalid option of `dagu egress-proxy serve`, or a key that the proxy cannot read

These cases fail a call or a step at run time:

- a call of a worker with an identity in no pool, or with a label that the pool does not allow:
  `PermissionDenied`
- a call of a worker for a lease of another pool: `PermissionDenied`
- a status report with no lease, with `worker_pools`: the report is refused
- another call from an identity that is not a dispatcher, when its pool holds no fresh lease of the
  call: `PermissionDenied`
- a dispatch of a root run from an identity that is not a dispatcher: `PermissionDenied`
- a label `agent.` without `worker_pools`: `PermissionDenied`
- a key of a step selector that starts with `agent.` after a run resolves it: the step fails
- a registry secret for a step that does not list it, or after the step ends: the step fails
- an untrusted step outside the work directory of the run, or a failed change of owner: the step
  fails
- an untrusted step in a process with no restricted user and no `untrusted.isolation: off`: the step
  fails with the reason `isolation_required`
- no agent worker for the provider within `coordinator.worker_wait_timeout`: the run fails

## Examples

### Complete DAG

The file `dev-with-agent.yaml` sets each key of `agent.run` that the provider `claude` accepts. It
is one DAG and one run, with three rounds of the [verify loop](#a-verify-loop). It needs the tools
`lookup-ticket.yaml` and `ops_notify.yaml` that follow it. The conformance fixture `conformance/spec092_agent_workers/testdata/dev-with-agent.yaml` holds the same text, and
`dagu validate` accepts it.

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
      endpoint:
        name: cle1
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
  # by default, so it runs as the restricted user. Its exit code is the verdict. The log goes
  # to a file in the work directory, for the next round.
  - id: verify_1
    depends: [code_1]
    working_dir: repo
    continue_on: { failure: true }
    run: |
      rc=0
      ./scripts/verify.sh > "${DAG_RUN_WORK_DIR}/${DAG_RUN_STEP_NAME}.log" 2>&1 || rc=$?
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
      endpoint:
        name: cle1
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
    continue_on: { failure: true }
    run: |
      rc=0
      ./scripts/verify.sh > "${DAG_RUN_WORK_DIR}/${DAG_RUN_STEP_NAME}.log" 2>&1 || rc=$?
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
      endpoint:
        name: cle1
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

  # This step also runs when a round before it passed. Then Dagu skipped code_3.
  - id: verify_3
    depends: [code_3]
    working_dir: repo
    continue_on: { skipped: true }
    run: |
      rc=0
      ./scripts/verify.sh > "${DAG_RUN_WORK_DIR}/${DAG_RUN_STEP_NAME}.log" 2>&1 || rc=$?
      cat "${DAG_RUN_WORK_DIR}/${DAG_RUN_STEP_NAME}.log"
      exit $rc

  # The steps after the loop run on the last verify. They are trusted: untrusted: false.
  # The verify script is a file of the repository, so the agent must not change it.
  - id: check_paths
    depends: [verify_3]
    untrusted: false
    working_dir: repo
    run: |
      if git status --porcelain | cut -c4- | grep -Eq '^(scripts/|test_|\.github/)'; then
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
    env:
      - MESSAGE: ${COMMIT_MSG}
    run: |
      git checkout -b "${BRANCH}"
      git add -A
      git -c user.name=dagu -c user.email=dagu@localhost commit -m "$MESSAGE"
      git -c credential.helper='!f() { echo username=git; echo "password=$GIT_TOKEN"; }; f' push origin "${BRANCH}"
```

The steps `code_1`, `code_2` and `code_3` are untrusted because their provider is a CLI provider.
The steps `verify_1`, `verify_2` and `verify_3` run the verify script as the restricted user. They
are untrusted by default, because the DAG has an `agent.run` step and the steps have no
`untrusted: false`. The step `check_paths` has `untrusted: false`, so it is trusted. The step
`push` has `untrusted: false` and `secrets`, so it is trusted and gets its secrets. The steps
`clone` and `push` are the only steps that get `GIT_TOKEN`. The run goes to an agent worker with the label
`agent.provider.claude`.

If `verify_1` passes, Dagu skips `code_2`, `verify_2`, `code_3` and `verify_3`, and the run goes to
`check_paths`. If `verify_1` fails, `code_2` runs. Its prompt gives the file `verify_1.log`. If
`verify_3` fails, the run fails and nothing is pushed.

The commit message of the step `message` comes from the text of the task, not from a result of the
agent. The step `push` takes it through `env` and uses `"$MESSAGE"` in the script. So no value of
the agent becomes shell code of a trusted step.

The step `code_1` gives Claude Code the DAGs `lookup-ticket` and `ops_notify` through the
tool bridge of Spec 091, and the MCP server `docs` of `MCP_URL`. A call of a Dagu tool runs a
trusted child run on the agent worker. The rounds 2 and 3 have the built-in tools only.

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
    run: echo "{\"id\":\"$CUSTOMER_ID\",\"plan\":\"gold\",\"open_tickets\":2}"
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
    action: log.write
    with:
      message: ${text}
```

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

### The proxy

A proxy for a run of the provider `claude`. The model traffic goes through the relay, so the proxy
has no upstream. It opens GitHub only to a client that sends the proxy credential: the steps clone and
push. The agent reaches no destination:

```text
dagu egress-proxy serve --listen 0.0.0.0:8081 \
  --allow-with-credential github.com:443 \
  --proxy-credential-file /run/secrets/proxy-credential
```

A request to `http://example.com/` through the proxy gets the status 403.

A proxy with key injection, for a CLI provider other than `claude` that uses the OpenAI API:

```text
dagu egress-proxy serve --listen 0.0.0.0:8081 \
  --allow api.openai.com:443 --allow git-server:8080 \
  --upstream https://api.openai.com/v1 --upstream-auth bearer \
  --upstream-key-file /run/secrets/openai-api-key
```

The agent sends `POST http://egress-proxy:8081/chat/completions` with a placeholder key. The proxy
sends `POST https://api.openai.com/v1/chat/completions` with the real key.

## Limits that remain

- An agent can send data to a destination that the proxy allows.
- An agent can return a false result. The typed result limits the form, not the content.
- An untrusted step shares the kernel with the worker. A kernel flaw can break the isolation.
- With an OAuth token, the agent uses the subscription of the owner of the token. The owner
  makes sure that the terms of the subscription let this use.
- For a CLI provider other than `claude`, the proxy sends each path of a request in origin form to
  the upstream, with the key. The agent can call each endpoint of the upstream that the key opens.
- A destination of `--allow` is open to the agent. Put a destination that only trusted steps
  need in `--allow-with-credential`.
- A group of steps cannot repeat in one run. The verify loop writes each round as a pair of steps.
  The number of rounds is fixed, and each round costs a full run of the agent. See [Limits of the
  loop](#limits-of-the-loop).
- The verdict of a verify step is the exit code of a script of the repository. The agent can change
  the script. The step `check_paths` must refuse a change to it.
- A trusted step that runs a file of the work directory runs code of the agent.
- Each user can read the arguments of a process in `/proc/<pid>/cmdline`. No process of the
  restricted user runs at the same time as a trusted step of its run. But a process of another run
  can read them. Pass a secret in the environment, never in an argument.

## Open items

Each item has a default. The default applies until the owner decides.

| Item | Default |
| --- | --- |
| The change of user in a pod of the level `restricted`, which forbids `CAP_SETUID`. A path with no capability, such as Landlock, is a candidate. | Level `baseline`, user ID 0 with six capabilities, `hostUsers: false` when the cluster supports it. A worker that cannot change user stops at start. |
| The restart delay of Kubernetes after each exit of a container, also with the code 0. | A `Deployment`. A controller that starts one Job for each run is a later item. |
| The paths that the route of the upstream allows. | Each path. A list of allowed paths is a later item. |
| A real rate limit with `retry_policy` and `exit_code: [75]`. | Not verified. The tests of Spec 091 use a fake CLI. |
| The name of the label `agent.provider.<provider>` and of the key `untrusted`. | As in this spec. |

## Conformance

Black-box tests are in `conformance/spec092_agent_workers/`. They use static fixtures, test
certificates, a fake CLI and a fake model API. The demo in `examples/agent-worker-demo/` covers the
whole chain on one host, with a fake CLI or, in its real mode, with Claude Code.

A test that changes to the restricted user needs Linux, the user ID 0 and the flag no-new-privileges.
The helper `harness.RequireRestrictedUserHost` skips it on another host. Run these tests in a root
container with the flag no-new-privileges. The table marks them "root".

| Section | Tests |
| --- | --- |
| The agent worker: Start | `TestWorkerAgentStartChecks`. The checks 7 and 8: not covered, because a test cannot remove a capability of its own container. Go tests of `internal/runtime/untrusted` check them. |
| The agent worker: one run and the end | `TestWorkerAgentTakesOneRunAndExits` (root). |
| Routing: implied selector, dispatch, reserved labels | `TestRoutingValidateRejects`, `TestRoutingValidateAccepts`, `TestWorkerReservedLabelStopsTheWorker`. The match of a task with an agent worker: `TestWorkerAgentTakesOneRunAndExits` (root). No match within `coordinator.worker_wait_timeout`: not covered here. |
| Worker pools: configuration | `TestPoolsInvalidConfigurationStopsTheCoordinator`, `TestPoolsConfigPrintsThePools`. |
| Worker pools: behavior | `TestWorkerAgentTakesOneRunAndExits` (root) for an allowed pool. A call from another identity or pool: not covered here. Go tests of `internal/service/coordinator` check it with mutual TLS. |
| Secrets of a step | `TestSecretsAndUntrustedValidation`, `TestSecretsStepScope`. The registry secret of a step on a worker: Spec 094, `TestRelayOnAnAgentWorker` (root) runs it. The refusal of a registry secret for another step: Go tests of the coordinator. |
| Untrusted steps: the key and its default | `TestSecretsAndUntrustedValidation`, `TestUntrustedDefaultInAgentDAG`, `TestUntrustedDefaultWithoutAgentDAG`, `TestUntrustedDefaultGateWithEnvValidates`. |
| Untrusted steps: no isolation | `TestUntrustedStepFailsWithoutIsolation`, `TestIsolationOffRunsWithAWarning`, `TestIsolationKeyErrorsStopTheProcess`, `TestFileOnlyBlockVariablesStopTheProcess`. The conformance tests that run without a restricted user set `untrusted.isolation: off` with the helper `harness.WriteIsolationOff`. |
| Untrusted steps: isolation | `TestUntrustedIsolation` (root), `TestReviewSecretInArguments` (root), `TestReviewLeftoverThreadWatchesLaterStep` (root), `TestReviewBareRepository` (root). |
| Trusted steps after an untrusted step | `TestReviewOutputInjection` (the validation part runs for each user; the run part is root), `TestUntrustedDefaultGateWithEnvValidates`. |
| A verify loop | `TestLoopFixesInRoundTwo`, `TestLoopFailsWhenAllRoundsFail`, `TestLoopPassesInRoundOne`, `TestLoopFailsWhenARoundFails`. |
| The egress proxy | `TestProxyInjectsTheKeyFromAFile`, `TestProxyInjectsTheKeyFromTheEnvironment`, `TestProxyInjectsTheKeyOverTLS`, `TestProxyAnthropicOAuthAddsTheBeta`, `TestProxyCredentialDestinations`, `TestProxyWithoutUpstreamRefusesOriginForm`, `TestProxyStartErrors`. |
| The AI gateway | Spec 094. |
| Verified points | `TestVerifyRepeatPolicyUntilCommand`, `TestVerifyRepeatPolicyUntilStdoutFile`, `TestVerifyRepeatPolicyLimit`, `TestVerifyRepeatPolicyWhileExitCode`, `TestVerifyRepeatPolicyOwnOutputIsNotReadable`, `TestVerifySubDAGOutputs`, `TestVerifyMaxOutputSize*`. |
| Examples | `TestCompleteExampleValidates`, `TestCompleteExampleMatchesSpec`. |
| Deployment | Not covered. The files of `deploy/agent-worker/` have a schema check only. |
