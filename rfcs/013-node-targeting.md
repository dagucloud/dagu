# RFC 013: Node Targeting

- **Status:** Proposed
- **Date:** 2026-11-12
- **Affected areas:** `spec`, `ir`, `runtime` (node fan-out, retry, bound
  runs), `runtime/builtin/ssh`, `runtime/builtin/dag`, `service/coordinator`,
  `service/worker`, `service/frontend` (API, UI), `service/mcp`, `cmd`,
  `api/v1`, `proto`, `ui`, `specs`, and `docs`

## Summary

Let a step run on every node a filter selects from the inventory of
[RFC 012](012-node-inventory.md):

```yaml
steps:
  - id: uptime
    run: uptime
    nodes: "labels: web+prod"
```

The step runs once per matched node, over SSH or on a worker, with a log and a
result per node. The step declares order, concurrency, and what happens when a
node fails, with the same settings as Rundeck's node dispatch.

A `dag.run` step with `nodes` runs a whole child DAG on one node at a time.
This is a rolling deploy:

```yaml
params:
  - name: env
    type: string
    enum: [staging, prod]

steps:
  - id: deploy
    action: dag.run
    with:
      dag: deploy-one
    nodes:
      filter: "labels: web+${params.env}"
      max_concurrent: 2

---
name: deploy-one
node: required
steps:
  - id: drain
    run: curl -fsS -X POST https://lb.internal/pools/web/${node.name}/drain
    nodes: local
  - id: restart
    run: sudo systemctl restart app
  - id: health
    run: curl -fsS http://localhost:8080/health
    retry_policy: { limit: 5, interval_sec: 3 }
  - id: enable
    run: curl -fsS -X POST https://lb.internal/pools/web/${node.name}/enable
    nodes: local
```

`node: required` marks `deploy-one` as a DAG that runs bound to a node.
`restart` and `health` run on the node. `drain` and `enable` run on the Dagu
host, because they say `nodes: local`.

## Problem statement

A Rundeck job names a node filter once, and its node steps run on every match.
Dagu can run a command on many hosts only through `parallel`, which works for
`dag.run` steps alone:

| Gap | What happens today |
| --- | --- |
| No fan-out over hosts | A DAG takes a host list as a parameter and runs `parallel` over a child DAG with one `ssh.run` step. Every DAG repeats this |
| No per-host view | The parent step lists child runs. There is no grid of hosts by steps and no log with host names on each line |
| No failure policy | `parallel` runs every item and fails at the end, and `foreach` ends partially succeeded. Rundeck's default stops at the first failed node, which is what a deploy needs |
| No order | Items start in list order. Rundeck sorts nodes by an attribute |
| No narrowing | An operator cannot run a job on two of its twenty hosts without editing it. Rundeck calls this "Change the Target Nodes". Ansible has `--limit` |
| No node-first runs | Running several steps on one host before moving to the next host needs a hand-built child DAG and host parameter |

## Goals

1. One field makes a step run on every node a filter matches.
2. Order, concurrency, and failure policy are set on the step, and map field
   by field from Rundeck's dispatch settings.
3. Steps can run step-first (each step on every node, then the next step) or
   node-first (every step on a node, then the next node).
4. Each node's status, log, and output can be seen on their own, and only the
   failed nodes are retried.
5. An operator can narrow a run's targets and can never widen them.
6. Nothing changes for DAGs that do not use `nodes` or `node: required`.

## Non-goals

- Limiting which nodes a user may target. A later access RFC can add it.
- Transports other than SSH and workers.
- Percentage-based concurrency or failure thresholds. See
  [Open questions](#open-questions).
- Ad-hoc commands typed into the UI. A later RFC covers them.
- Reusing results across separate runs.

## Design

### The `nodes` field

`nodes` is an optional step field. The string form is a filter:

```yaml
nodes: "labels: web !name: web03"
```

A filter with `: ` must be quoted. An unquoted `nodes: labels: web` is a YAML
error. `dagu validate` and the editor report it with the fix:
`nodes: "labels: web"`.

The object form adds dispatch settings:

```yaml
nodes:
  filter: "labels: web+${params.env}"
  max_concurrent: 5
  keep_going: true
  rank_by: rack
  rank_order: desc
  allow_empty: false
  retry: failed
```

| Field | Default | Meaning |
| --- | --- | --- |
| `filter` | Required | An RFC 012 filter. Value-resolved, so it can use `${params.*}`, `${consts.*}`, and step outputs, as [values](#references-in-a-filter) |
| `max_concurrent` | `1` | Nodes running at once, `1` through `1000` |
| `keep_going` | `false` | Whether other nodes start after one fails |
| `rank_by` | `name` | Field or label key that orders the nodes |
| `rank_order` | `asc` | `asc` or `desc` |
| `allow_empty` | `false` | Whether matching no nodes succeeds |
| `retry` | `failed` | `failed` reruns failed nodes on retry, `all` reruns every node |

`nodes: local` is a reserved string. It is described under
[Node-first runs](#node-first-runs).

The default `max_concurrent` is `1`, as in Rundeck. A command that restarts a
service should not reach every host at once unless the author says so.
`defaults.max_concurrent` from Spec 018 does not apply to `nodes`.

### References in a filter

A reference in a filter inserts values, never filter syntax:

- A scalar is literal text. It is regex-escaped, and its `,`, `+`, `!`, `:`,
  quotes, and spaces add no item, label term, or clause. With `host` set to
  `.*`, `name: ${params.host}` matches only a node named `.*`, and none can
  exist. With `env` set to `prod,db`, `labels: web+${params.env}` matches no
  node. `name: web-${params.env}-.*` stays a regex around the literal value.
- An RFC 015 `type: array` or `type: nodes` param that makes up a whole item
  inserts one literal item per element, with or without `delimiter`.
  `name: ${params.hosts}` with `[web01, web02]` matches both nodes.
- In an exclude clause, a scalar may hold only node-name characters:
  letters, digits, `_`, `.`, `@`, `:`, and `-`. Any other character, such as
  `,`, `+`, a space, or `*`, fails the step before any node starts, with "use
  an array param". Rundeck substitutes option text before it parses a filter,
  so `web01,web02` typed into `!name: ${option.skip}` skips both hosts there.
  As one literal item it would skip neither. An include needs no such check:
  a literal item can only match fewer nodes, and a step that matches none
  fails.
- No reference supplies a whole filter or a regular expression. An operator
  who needs other nodes narrows the run.

A free-text param can still name any node exactly. An `enum` or enforced
choices (RFC 015) limit which.

### Where a node item runs

| Step | On an `ssh` node | On a `worker` node |
| --- | --- | --- |
| `run`, `command`, or `script` with no `action` | Over SSH | As a one-step child run on that worker |
| `ssh.run` | Over SSH | Fails: the node has no SSH connection |
| `sftp.upload`, `sftp.download` | Over SFTP | Fails: the node has no SSH connection |
| `dag.run` | A child run [bound](#node-first-runs) to the node | A child run bound to the node, dispatched to that worker |

Node items ignore DAG-level `ssh` and `container`, whether the DAG writes them
or inherits them from base config. The DAG's other steps keep using them.

For SSH:

- The connection opens from the host that runs the step: the Dagu server, or
  the worker that runs the run. On a worker, `ResolveNodes` also returns the
  project's trusted host keys for the matched nodes. They are public.
- The node supplies the connection.
- On `ssh.run` and `sftp.*`, connection fields under `with`, such as `user`
  or `key`, override the node's, field by field. `with.key` also drops the
  node's `passphrase`, as issue #3769 defines for base config, and
  `with.bastion` replaces the node's bastion as a whole. Spec 043's rule,
  that step settings replace the DAG-level `ssh` as a whole, still holds for
  steps without `nodes`.
- On `run`, `command`, and `script`, `with` takes only `shell` and
  `shell_args`, as Spec 013 defines. The connection comes from the node alone.
- The shell is the step's `with.shell`, then the node's `ssh.shell`, then
  `/bin/sh`. The root `shell`, `DAGU_DEFAULT_SHELL`, and platform discovery
  describe the Dagu host and do not apply.
- Node items of a step that share a bastion share one connection to it, with
  one channel per node. With `max_concurrent: 50`, the bastion sees one
  handshake, not fifty, so its `MaxStartups` limit does not drop healthy
  nodes.
- Only a `working_dir` set on the step itself applies on the node. An
  inherited `working_dir` names a directory on the Dagu host and is ignored.
- Step `env` is not sent to the node, as for `ssh.run` today.
- In the `run`, `command`, and `script` text of a node item, and of a bound
  run's steps that run on the node, unqualified `$NAME` and `${NAME}` belong
  to the remote shell, as Spec 003 defines `run` text for a local shell.
  Dagu resolves only its own references before sending, such as
  `${params.x}`, `${env.NAME}`, `${consts.x}`, and `${node.name}`. A remote
  script's own `$host` keeps its value when the DAG has a param `host`.
  `dagu validate` gives a passive notice for an unqualified name that
  matches a param, env entry, or secret, with the `${env.NAME}` fix.
- `ssh.run`, and `run` steps under DAG-level `ssh`, keep Spec 006's rule:
  Dagu expands unqualified names from the env scope before sending.

On a worker node, an item's text follows the same rules, so one script means
the same on both transports:

- The resolved `run`, `command`, or `script` text goes to the shell's
  standard input, as over SSH, never as a `sh -c` argument. The
  shell is the step's `with.shell`, then `/bin/sh`. A bound run's command
  steps on a worker node run the same way.
- Step `env` is not set, and unqualified `$NAME` belongs to the shell.

How a step reads a secret depends on where it runs. RFC 015's secret params
and RFC 018's import follow this rule:

- A local shell step, such as a step without `nodes`, a `nodes: local` step,
  or a bound run's handler, reads `$NAME`. The shell expands it, so the value
  stays out of the command text and argv (Spec 006). `${env.NAME}` there
  would write it into the `sh -c` argument.
- A node item, or a bound run's step that runs on the node, reads
  `${env.NAME}`, since `$NAME` belongs to the node's shell and step `env` is
  not sent. The value is part of the script, which Dagu sends on the shell's
  standard input, over SSH or on a worker, never in argv. The run's masker
  hides it in logs.

### Validation

`dagu validate` fails when:

- `nodes` is on a step other than those in the table above.
- `nodes` is on a step with `parallel`, `foreach`, `worker_selector`,
  `container`, or `repeat_policy`.
- `with.host` or `with.ip` is set on an `ssh.run` or `sftp.*` step with
  `nodes`.
- `with.shell_packages` is set on a step with `nodes`.
- An `sftp.download` step with `nodes` has a `with.destination` that does not
  use `${node.name}`. Every node would write the same local path.
- A DAG with `node: required` writes the DAG-level `ssh` or `container` in its
  own document. Values inherited from base config are allowed.
- A DAG with `node: required` has a `schedule`, or a `restart_policy` of
  `always` or `unless-stopped`. A scheduled start and a service restart have
  no node.
- A `dag.run` step names a child DAG in the same file that cannot be started
  that way: the step sets `nodes` and the child lacks `node: required`, or
  the step sets no `nodes`, its DAG lacks `node: required`, and the child
  declares it. Other children are checked when the step starts.
- A literal filter does not parse, or has an item that uses a Java regex
  construct RE2 lacks (RFC 012).
- The object form has an unknown field, a missing `filter`, or an
  out-of-range value.
- `local` is used in the object form.
- `node` is neither `required` nor an object with `required: true` and an
  optional literal `filter`.

Validation does not read the inventory, so it works offline and in CI.

### Matching

- The filter is resolved and matched when the step starts, after its
  dependencies and preconditions.
- The run's [target](#narrowing-a-run), if any, is applied.
- Nodes are ordered by `rank_by`, then by name. Two values compare as numbers
  when both are numbers, and as strings otherwise. Nodes without the label
  come last.
- The ordered list of node names is recorded in the step's status. Retries
  use it.
- A resolution warning from RFC 012, such as a stale source, is written to
  the step log.
- A resolution error fails the step before any node starts.
- A step that matches no nodes fails with "filter matched no nodes", unless
  `allow_empty` is set, in which case it succeeds.
- A matched node with `disabled: true` (RFC 012) is not run. It is `disabled`
  in the aggregate and the Nodes tab, with the others, and does not fail the
  step. A step whose matched nodes are all disabled fails with "every matched
  node is disabled", unless `allow_empty` is set.
- A step that matches nodes, but none after the run's target, is skipped.
  The skip does not block dependents, as a skip kept by a retry does not.
  Narrowing a run to the web hosts should neither fail its database step nor
  skip the web steps after it. When the target empties every node step that
  matched nodes, the run fails. See [Narrowing a run](#narrowing-a-run).
- After the target, a step can have at most 1,000 nodes, as Spec 018 limits
  items. A target can bring a larger match under the limit.

### Concurrency and failure

```text
max_concurrent: 2, keep_going: false

web01 ████████✓
web02 ██████████████✗
web03          ███████████✓      started before web02 failed, so it finishes
web04                      ·     not started
step: failed
```

- Nodes start in order, at most `max_concurrent` at a time. Completion order
  is not guaranteed.
- With `keep_going: false`, no node starts after a node fails. Running nodes
  finish. The rest are `not_started`. The step fails.
- With `keep_going: true`, every node runs. The step fails if any node failed.
- The step succeeds only when every matched node succeeds.
- `continue_on` applies to the step's result, as for any step.
- When a child run of a `dag.run` item waits for approval or input, the step
  waits, as `parallel` does.
- Aborting the step stops pending nodes, closes SSH sessions, and aborts child
  runs. Their items are `aborted`.

Node items do not count against `max_active_steps`. The step counts as one.

### Per-node records

The run status is rewritten in full on every change, so per-node detail stays
out of it:

- Each node item has a record in the step's directory, as `foreach` items do:
  status, exit code, error, timings, and for `dag.run`, the child run ID and
  each child step's status. A running item's record is rewritten at most once
  per second.
- A summary record in the step's directory holds counts by status and the
  names of failed and running nodes. It is rewritten with the item records,
  at most once per second.
- When a node finishes, a line with its name, status, and exit code is
  appended to the step's outcome file and fsynced. Unlike the records, it is
  durable, so a retry after a killed run process, a reboot, or a lost worker
  knows which nodes succeeded.
- The step's status holds the matched node list and, when the step ends, a
  compact entry per node: name, status, and exit code. It holds no stdout.

On a worker, the run's log directory is temporary and removed when the run
ends. The worker sends the server everything the server shows:

- The step's stdout and stderr are the combined node logs (see
  [Logs](#logs)), streamed as today.
- Each node's stdout and stderr stream with the node name, in a new
  `target_name` field of `LogChunk`.
- A new coordinator RPC carries the summary and changed item records, at most
  once per second, and each outcome line as soon as the node finishes. The
  coordinator writes them into the server-side step directory, where the API
  reads them, and fsyncs the outcome lines.
- The coordinator adds the outcome lines to the previous status it sends with
  a retry.

### Step fields on a node step

| Field | Applies to |
| --- | --- |
| `timeout_sec` | Each node. One hung host does not use up the others' time |
| `retry_policy` | Each node. A failed node retries alone |
| `preconditions` | The step, once, before matching |
| `continue_on` | The step |
| `output`, `outputs` | The step's aggregate. See [Outputs](#outputs) |

### The `node` namespace

The Spec 003 namespace registry gains `node`:

| Reference | Value |
| --- | --- |
| `${node.name}` | Node name |
| `${node.hostname}`, `${node.port}`, `${node.user}` | Connection fields |
| `${node.description}`, `${node.transport}`, `${node.source}` | Other built-in fields |
| `${node.labels}` | Labels, comma-separated, sorted, as `key` or `key=value` |
| `${node.<key>}` | The value of the label with that key |
| `${node."<key>"}` | The same, for a key that is not an identifier, such as `gpu.type` or `region-1`, as Spec 018 quotes item fields |

Rules:

- `node` is available in every value-resolved field of a step with `nodes`,
  including `with`, except `nodes.filter` itself.
- `node` is available in every field of a [bound run](#node-first-runs).
- Where `node` is available, a reference to a label the node lacks fails
  that node's item, with the node name and the label key. Elsewhere a
  `${node.*}` reference is preserved with a passive notice. This is the
  missing behavior of the `node` row in the Spec 003 registry. It differs on
  purpose from `foreach`, which preserves a missing item field: the text goes
  to a remote shell, where it would fail later and less clearly.
- For processes that run on the Dagu host or a worker, Spec 017 gains
  `DAG_NODE_NAME` and `DAG_NODE_HOSTNAME`.

### Node-first runs

Only a DAG that declares `node: required` at the top level can be bound to a
node, and it always is. Its command steps are written for a remote host, so
running it unbound would run them on the Dagu host. A DAG without the
declaration never sends a step to a node unless the step sets `nodes`.

The object form also limits which nodes the DAG can be bound to:

```yaml
node:
  required: true
  filter: "labels: web"
```

- `filter` is a literal RFC 012 filter, checked by `dagu validate`.
- Admission rejects a binding to a node outside it, on every surface, naming
  the node.
- A `dag.run` step with `nodes` over the DAG targets only nodes that also
  match it, each filter evaluated on its own, as a
  [target](#narrowing-a-run) is.
- A bound parent that passes its binding to the DAG fails that step when its
  node is outside the filter.

A `dag.run` step with `nodes` starts one child run per matched node. Each child
run is bound to its node. The child must declare `node: required`.

- The child run ID is derived from the parent run ID, the step, the child DAG,
  and the node name, as `parallel` derives its child run IDs. A retry finds
  the same children.
- `with.params` is resolved once per node, with `node` available.

A bound run:

- records its node, with its labels and without secrets, in its status.
  The run header shows it, and the start's audit entry names it.
- resolves `${node.*}` in every field.
- looks its node up by exact name (RFC 012), never as a filter, so
  `web01.prod` cannot also match `web01Xprod`.
- runs its `run`, `command`, and `script` steps, and its `ssh.run` and
  `sftp.*` steps that set no host, on the bound node.
- runs other actions, such as `http.request` or `docker.run`, and steps with
  `container`, where the run runs, as before.
- runs a step with `nodes: local` where the run runs.
- runs its `handler_on` steps where the run runs, so notifications keep
  working.
- fans out a step with its own `nodes` filter as usual.
- passes its binding to `dag.run` children that declare `node: required`,
  unless the call step sets `nodes`. Other children run unbound.

A `node: required` DAG cannot write the DAG-level `ssh` or `container`, since
both would claim the same command steps. A bound run ignores both when base
config sets them, so a project whose base config sets `container` can still
hold `node: required` DAGs. For DAGs that deploy over DAG-level `ssh` today,
the binding replaces it.

A run bound to a worker node is dispatched to that worker through the
existing `target_worker_id`, so its command steps run there, with their text
on the shell's standard input, as [worker node items](#where-a-node-item-runs)
run.

Started on its own, a `node: required` DAG takes a node, so it can be tested
alone: `dagu start deploy-one --bind-node web01`, or `bindNode` in the API
and MCP. Without one, the start fails with "deploy-one must be bound to a
node". On any other DAG, `--bind-node` fails with "deploy-one does not
declare node: required". A webhook carries no node, so enabling a webhook on
a `node: required` DAG fails. The run dialog asks for the node with a node
picker, built with node-first runs, that RFC 015's node parameter (issue
#3773) reuses. The picker lists the nodes `node.filter` matches, from the
DAG-scoped lookup of [Narrowing a run](#narrowing-a-run), with its search and
paging. Without a filter, it lists the
project's nodes to users who may list them, and a user whose only right on
the DAG is a DAG-scoped grant (issue #3772) cannot start it on its own. The
binding is not a parameter: it decides where steps run, so it is recorded and
audited on its own.

```text
step-first: nodes on each step          node-first: dag.run + nodes
           web01 web02 web03                     drain restart health enable
drain        ✓     ✓     ✓              web01      ✓      ✓       ✓      ✓
restart      ✓     ✓     ✓              web02      ✓      ✓       ✓      ✓
health       ✓     ✓     ✓              web03      ✓      ▶
each step runs on every node            each node runs every step before
before the next step starts             the next node starts
```

### Narrowing a run

A run can carry a target with three optional parts: a filter, node names to
keep, and node names to exclude. It applies to every `nodes` step in the run
and in its child runs. A node is targeted only when it matches the step's
filter and each part of the target, each evaluated on its own. Filters are
never joined into one string, since RFC 012 merges clauses on the same
key into an OR list. `nodes: local` is not affected.

```text
step filter   labels: web        → web01 web02 web03 canary01
target        labels: canary     → canary01 canary02
targeted      both               → canary01
```

| Surface | Target | Binding |
| --- | --- | --- |
| CLI `dagu start`, `dagu enqueue` | `--target-nodes '<filter>'` | `--bind-node <name>` |
| API start, start-sync, and enqueue bodies | `targetNodes: { filter, names, exclude }` | `bindNode` |
| MCP `dagu_execute` start and enqueue | `targetNodes` | `bindNode` |

- `names` and `exclude` are not bound by RFC 012's 4 KiB filter limit. Each
  holds at most 10,000 names.
- Admission rejects a target that names a node not in the inventory, in
  `names` or `exclude`, or whose filter matches no inventory node. The error
  names the offender, so a mistyped `name: web1` never starts a run.
- Admission rejects a target on a DAG whose document has no `nodes` step and
  no `dag.run` step, with "hello has no node steps".
- A run fails with "target selected no nodes" when at least one node step
  matched nodes and the target emptied every such step, in the run and the
  child runs it waits for. A child run does not apply this check on its own.
  A run whose preconditions skip every node step does not fail.
- The target and binding are stored in the run's status when the run is
  admitted, so a queued run keeps them. The dialog enqueues by default.
- Retries, edit-retries, reschedules, `dagu restart`, and service restarts
  (Spec 080) keep them, without running the admission checks again. A node
  named in the stored target may have left the inventory since.
- The target is shown in the run header and written to the audit entry of
  the start.

The run dialog:

- Lists the distinct nodes the DAG's node steps match, once each, with the
  steps that reach it, such as `web01 (deploy, smoke)`. All are checked, as
  Rundeck does by default. The list comes from
  `POST /dags/{fileName}/target-nodes`, which needs run permission on the
  DAG, not from `GET /nodes`. The server resolves each step's own filter with
  the form's param values, so a run-only user sees only the DAG's nodes. For
  a `node: required` DAG, it also returns the nodes `node.filter` matches,
  for the binding picker. MCP uses the same lookup.
- The lookup is paged, as `GET /nodes` is. Each response holds each step's
  match count and its count after the target, the number of distinct nodes,
  and one page of them, with a name search run on the server. Above 200
  distinct nodes, the dialog opens on the target field and the counts, and
  the checklist shows one searched page at a time. Unchecked nodes from any
  page join `exclude`. A step over the 1,000-node limit is marked until the
  target narrows it.
- Lookups after a param or target edit are debounced, and a stale lookup is
  cancelled.
- Shows "matched when the step starts" for a filter that reads a step
  output or other run-time value.
- Sends unchecked nodes as `targetNodes.exclude`. Unchecking a node removes
  it from every node step in the run and its child runs. A step the dialog
  could not list, or one in a child DAG, loses only the unchecked nodes.
- Has a text field that sets `targetNodes.filter`. The lookup also takes the
  target and returns what each step keeps, and the dialog shows the count
  before submit.

A target can only remove nodes, so a user who may run a DAG cannot use
narrowing to reach a node the DAG would not reach.

### Retry

Node retry needs nothing from [RFC 011](011-hpc-batch-steps-and-sweeps.md).
It shares the `retry: failed` and `retry: all` values that RFC 011 Part 2
proposes for `foreach`. A node item is identified by its node name.

- Retrying a node step, through `dagu retry` or the UI, reruns its `failed`,
  `aborted`, and `not_started` nodes. Succeeded nodes keep their recorded
  results: the step status's per-node entries, or the outcome file when the
  step never ended. `retry: all` reruns every node.
- The retry uses the node list recorded by the first attempt. A node added to
  the inventory since then is not added. A node removed since then is not
  run. It is `removed` in the aggregate and the Nodes tab, and does not fail
  the step. Hosts often leave the inventory because they failed, so the
  retry still finishes the others.
- Connection settings are read again from the current inventory, so a rotated
  key works on retry.
- For a `dag.run` step, the failed children resume as retries of their own
  runs, and succeeded children return their recorded result, as `parallel`
  children do today.

The UI shows **Retry failed nodes**, with a count, on a node step and on a
run that has one.

### Outputs

The step's `output` variable (Spec 012) holds JSON:

```json
{
  "summary": { "total": 3, "succeeded": 1, "failed": 2, "aborted": 0, "not_started": 1, "removed": 0, "disabled": 0 },
  "nodes": [
    { "name": "web01", "status": "succeeded", "exit_code": 0, "output": "..." },
    { "name": "web02", "status": "failed", "exit_code": 1, "error": "..." },
    { "name": "web03", "status": "not_started" }
  ]
}
```

- `summary.total`, `succeeded`, and `failed` mean what Spec 018 defines:
  `failed` counts every node not counted as successful. `aborted` and
  `not_started` count those subsets of `failed`.
- A node's `status` is `succeeded`, `failed`, `aborted`, or `not_started`.
  For `dag.run` it can also be `partially_succeeded`, which counts as
  successful, as in Spec 018. After a retry it can be `removed`. Removed
  nodes count in `total` and `summary.removed` only, not in `failed`. A
  disabled node is `disabled`, and counts in `total` and `summary.disabled`
  only.
- A node kept from an earlier attempt by a retry has `"reused": true`.
- `nodes` follows the matched order.
- `output` is the node's captured stdout. The whole aggregate is limited by
  `max_output_size`, and each node gets an equal share of it. A cut value has
  `"output_truncated": true`. The node's log keeps the full output.
- Declared `outputs` appear per node as `nodes[].outputs`. For `dag.run`, each
  entry has the child's outputs and its `dagRunId`.
- The aggregate is written whether or not every node succeeded.
- `${steps.<id>.outputs.<name>}` on a node step is not defined. Steps read the
  aggregate.

### Logs

- Each node item has its own stdout and stderr.
- The step's own stdout and stderr hold every node's lines, each prefixed by
  its node name, in arrival order. They are written as the nodes run, so
  **All nodes** reads one file, on the server and from a worker alike.
- The step log shows **All nodes** and one tab per node. A toggle shows
  failed nodes only.
- The step log API and MCP's `step_log` target take `targetName`, a node
  name. In the API, `nodes` in run details still means steps, so a step's
  per-node entries are its `targets`.
- `dagu status` lists each node step with its per-node counts.

### Run view

```text
┌ deploy ── run 2026-11-12 09:14 ── target: labels: web+prod ───────┐
│ Graph  Nodes  Logs                                                │
│                                                                   │
│ Nodes          drain  restart  health  enable                     │
│ web01            ✓       ✓        ✓       ✓                       │
│ web02            ✓       ✓        ✗       ·     ▸ log             │
│ web03            ·       ·        ·       ·     not started       │
│                                                                   │
│ [Retry failed nodes (2)]   [Show failed only]                     │
└───────────────────────────────────────────────────────────────────┘
```

- A node step in the graph shows a count badge, such as `12 ✓ 1 ✗ 1 ⋯`.
- The **Nodes** tab shows nodes by steps. For step-first runs, the columns are
  the node steps. For node-first runs, the columns are the child DAG's steps.
- The badge and the grid read the step's summary record, then only the item
  records of the visible page ([per-node records](#per-node-records)). For
  node-first runs, item records hold each child's step statuses, so the grid
  never reads child status files per request. It is paged, lists failed
  nodes first, and updates over SSE.
- Each cell opens that node's log for that step.

### Editor

- The DAG editor shows the match count next to each literal `nodes` filter,
  from `GET /nodes`. Hovering lists the first 20 nodes.
- Filter syntax errors are marked in place.
- A filter with references shows "matched when the step starts".

### Rundeck mapping

| Rundeck job | Dagu |
| --- | --- |
| `nodefilters.filter` | `nodes.filter`, with keywords rewritten as RFC 012 lists |
| `nodefilters.dispatch.threadcount` | `nodes.max_concurrent` |
| `nodefilters.dispatch.keepgoing` | `nodes.keep_going` |
| `nodefilters.dispatch.rankAttribute` | `nodes.rank_by` |
| `nodefilters.dispatch.rankOrder` `ascending` or `descending` | `nodes.rank_order` `asc` or `desc` |
| `excludePrecedence: true` | The default |
| `excludePrecedence: false` | Not supported. Reported by the import tool |
| `successOnEmptyNodeFilter` | `nodes.allow_empty` |
| `nodeFilterEditable` | Narrowing is always available |
| `sequence.strategy: step-first` | `nodes` on each node step |
| `sequence.strategy: node-first` | `dag.run` with `nodes`, over an inline child DAG with `node: required` holding the node steps |
| `sequence.strategy: parallel` | Steps with no `depends` in a `type: graph` DAG |
| Orchestrators, Ruleset, and other strategy plugins | Not supported. Reported by the import tool |
| `jobref` with `nodeStep: true` | `dag.run` with `nodes`, over a bound variant of the referenced job: a copy with `node: required`. The job's own DAG stays unbound |
| `${node.name}`, `${node.hostname}`, `${node.username}` | `${node.name}`, `${node.hostname}`, `${node.user}` |

### Ansible mapping

| Ansible | Dagu |
| --- | --- |
| `hosts:` pattern | `nodes.filter` |
| `forks` | `nodes.max_concurrent` |
| `serial: N` | `dag.run` with `nodes` and `max_concurrent: N`. Dagu starts the next host as soon as one finishes, where Ansible waits for the whole batch |
| `any_errors_fatal: true` | `keep_going: false` |
| `--limit` | `--target-nodes` |
| `run_once`, `delegate_to: localhost` | A step without `nodes`, or `nodes: local` in a bound run |

## Security

- A step can match only nodes in its run's project inventory.
- A reference in a filter inserts values only, so a run-time value cannot add
  clauses or regular expressions. In an exclude clause, a scalar with a
  character outside node names fails the step, so an exclusion never widens
  a run silently.
- The target can only remove nodes. It is recorded and audited.
- The run dialog's node lists need run permission on the DAG and hold only
  nodes the DAG's own filters match.
- Only a DAG that declares `node: required` can be bound, so a user who may
  run a DAG cannot send a local DAG's commands to a node. `node.filter`
  limits which nodes it can be bound to. The binding is recorded and audited.
- Each distinct credential ref resolves once per run, as RFC 012 defines, is
  kept in memory, and is masked in every log and status. Values never enter
  run status.
- Host keys are checked as RFC 012 defines, on a worker too. A run never
  trusts a new key.
- Node fields and label values follow RFC 012's character rules and are
  safe to insert unquoted. Labels can come from scripts, so the docs still
  quote them in shell commands: `"${node.role}"`.
- On a worker node, the item runs as the worker's OS user, as worker runs do
  today. Its text goes to the shell's standard input, so a secret in it
  never reaches a process argv on the worker.

## Failure behavior

| Event | Behavior |
| --- | --- |
| The inventory cannot be read | The step fails before any node starts, with RFC 012's error |
| The filter does not parse at run time | The step fails, with the position and the error |
| A scalar in an exclude clause holds a character outside node names | The step fails before any node starts, with "use an array param" |
| SSH connection or authentication fails | That node fails, with the host, port, error, and the hop that failed: the bastion or the node |
| The host key is unknown or has changed | That node fails, with the fingerprint. The Nodes page can trust a new key. A host that only a worker reaches pins its key with `ssh.host_key` |
| A node's credential ref does not resolve | That node fails, naming the field and ref. Other nodes follow `keep_going`. The failure is kept for the rest of the attempt, so nodes that share the ref do not resolve it again |
| A node has an RFC 012 problem, such as no hostname | That node fails, with the problem |
| A worker node disconnects during its item | That node fails, as a worker loss fails a run today |
| A matched node is disabled | That node is `disabled` and not run. The step does not fail for it. A binding to it is rejected at admission, naming the node |
| A node is gone from the inventory at retry | That node is `removed` and not run. A run bound to it fails, with "no longer in the inventory" |
| The run process is killed, or its worker is lost, during a node step | The run fails. A retry reads the outcome file and reruns only nodes that did not succeed |
| `${node.<key>}` names a label the node lacks | That node fails, with the node name and the label key |
| A `dag.run` step with `nodes` calls a child without `node: required` | The step fails before any node starts |
| A binding names a node outside the DAG's `node.filter` | A start is rejected at admission, naming the node. A bound parent's step that passes it fails |
| A target names an unknown node, or its filter matches no node | The run is rejected at admission, naming the offender |
| A target emptied every node step that matched nodes | The run fails with "target selected no nodes" |
| A target is given for a DAG with no `nodes` or `dag.run` step | The run is rejected at admission |

## Compatibility and rollout

- `nodes` is a new step field, and `node` a new DAG field. The `node`
  namespace is new.
- `${node.*}` was ordinary text before. Inside node steps and bound runs it
  now resolves. Elsewhere it is still preserved, with a notice.
- No existing step changes behavior.
- A DAG that moves from DAG-level `ssh` to `node: required` writes Dagu values
  as `${params.x}` or `${env.NAME}`, since its `run` text now belongs to the
  remote shell. The docs show this.

Rollout:

1. **Step-first over SSH**: `nodes` on `run`, `command`, `script`, `ssh.run`,
   and `sftp.*`, with matching, order, concurrency, failure policy, the `node`
   namespace, per-node records, outcomes, and logs, also from runs on
   workers, the aggregate output, the Nodes tab, and the editor's match
   count. A new spec, with conformance tests against an SSH server the test
   harness starts.
2. **Narrowing** on every start and enqueue path: CLI, API, run dialog, and
   MCP, with the admission checks and the dialog's target lookup.
3. **Node-first**: `node: required` with its optional `filter`, `dag.run`
   with `nodes`, bound runs, `--bind-node`, and the node picker that
   RFC 015's node parameter reuses.
4. **Worker nodes**, for command items and bound runs, with text on the
   shell's standard input.
5. **Retry failed nodes**, for step-first and node-first steps, locally and
   on workers.
6. **Docs**: "Coming from Rundeck" and "Coming from Ansible", with the mapping
   tables above.

## Verification and acceptance criteria

1. A `run` step with `nodes: "labels: web"` over three SSH nodes runs on each,
   and its aggregate lists three nodes in name order.
2. `max_concurrent: 1` runs nodes one at a time in rank order, and
   `rank_order: desc` reverses it.
3. With `keep_going: false`, a failure on the second of four nodes leaves the
   third and fourth `not_started`, and the step fails.
4. With `keep_going: true`, all four run and the step fails.
5. A filter matching nothing fails the step, and `allow_empty: true` makes it
   succeed.
6. A target that removes every node of a step skips that step. In a chain
   DAG, the step after it still runs.
7. `dagu retry` reruns only failed and not-started nodes. A node added to the
   inventory between attempts is not added. A failed node removed from the
   inventory before the retry is `removed`, and the retry succeeds when the
   other nodes do. Killing the run process after two of four nodes succeed,
   then retrying, runs only the other two.
8. `--target-nodes 'name: web01'` limits every node step and child run to
   `web01`. A step with `name: web01,web02` and target `name: db01` targets
   no node. A step with `labels: web` and target `labels: canary` targets only
   nodes with both labels.
9. A narrowed run admitted through the queue, by `dagu enqueue`, the enqueue
   API, or the run dialog's default, runs only on the selected nodes. So does
   the new run that `dagu restart` starts from it.
10. On a DAG with a `labels: web` step and a step whose filter reads
    `${steps.pick_db.output}`, unchecking `web03` in the run dialog removes
    only `web03`. The second step still runs on every node it matches.
11. `--target-nodes 'name: web1'` and `targetNodes.names: [web1]`, with no
    node `web1`, are rejected at admission, naming `web1`. A target that
    matches inventory nodes but none of the run's node steps fails the run
    with "target selected no nodes". A target on a DAG with no `nodes` or
    `dag.run` step is rejected at admission. A target on a run whose
    preconditions skip every node step does not fail the run.
12. A step whose filter matches 1,200 nodes fails. With a target that leaves
    5, it runs on those 5.
13. A `dag.run` step with `nodes` starts one bound child per node. In the
    child, `${node.hostname}` resolves, a `run` step runs on the node, and a
    `nodes: local` step and a `handler_on.failure` step run on the Dagu host.
14. `dagu start deploy-one --bind-node web01` runs the child alone, bound to
    `web01`. Starting it without a node fails, enabling a webhook on it
    fails, and `--bind-node` on a DAG without `node: required` fails. With
    `node: { required: true, filter: "labels: web" }`, `--bind-node db01` is
    rejected, the picker lists only `labels: web` nodes, and a parent step
    with `nodes: "labels: prod"` runs it only on nodes with both labels.
15. With a base config that sets `container`, a `run` step with `nodes` runs
    on each node over SSH, and a `node: required` DAG validates and runs its
    `run` steps on the bound node.
16. A `run` step on a worker node runs on that worker. A run on a worker
    connects to an SSH node whose host key was trusted only on the Nodes
    page. For a node step in a run on a worker, the server shows the Nodes
    tab, the count badge, **All nodes**, and each node's log.
17. `timeout_sec` and `retry_policy` apply to each node separately.
18. Fifty concurrent node items behind one bastion open one connection to it.
19. A step over 1,000 nodes with `output` set keeps its aggregate within
    `max_output_size`, and the run status holds no per-node stdout. The count
    badge reads one summary record per step. The Nodes tab of a node-first
    run over 1,000 nodes reads no child status file, and only the item
    records of the visible page, per request.
20. A secret used as a node's key never appears in logs, status, or output.
21. Each validation rule in [Validation](#validation) has a failing example.
    `nodes: "name: *.example"` validates.
22. With `keep_going: true`, a node whose key ref does not exist fails,
    naming the field and ref, and the other nodes run. Five hundred nodes
    that share the missing ref try to resolve it once.
23. `name: ${params.host}` with `.*`, and `labels: web+${params.env}` with
    `prod,db`, match no node. An array param `[web01, web02]` in
    `name: ${params.hosts}` matches both. `!name: ${params.skip}` with
    `web01,web02` fails the step with "use an array param", and with
    `web01.prod` excludes that node.
24. On an SSH node, a `run` script that sets and reads `$host` sees its own
    value while the DAG has a param `host`, and `${params.host}` in the same
    script inserts the param. A secret written as `${env.TOKEN}` reaches the
    node and is masked in the log. A local step that reads `$TOKEN` gets the
    secret, and no process argv holds it. On a worker node, the same script
    sees its own `$host` and gets `${env.TOKEN}`, and no process argv on the
    worker holds the secret.
25. A DAG whose step matches 5,000 nodes opens the run dialog with one page
    of nodes and correct per-step counts, and marks the step as over the
    limit. A node unchecked on a later page is excluded from the run.

## Alternatives considered

- **Keep `parallel` over a host parameter.** Works today, but every DAG
  repeats the host list and the child DAG, and the run shows child runs, not
  hosts.
- **Expand `nodes` into `foreach` in YAML.** Smaller runtime change, but
  `foreach` ends partially succeeded, has no stop-at-first-failure, and its
  items are values, not hosts with connections.
- **Allow `parallel` on every action.** Fans out, but carries none of the
  host semantics: connection, order, failure policy, or narrowing.
- **A DAG-level node-first switch.** Matches a Rundeck job in one document,
  but turns one run into many without a child run to show each. `dag.run`
  with `nodes` reuses child runs. See [Open questions](#open-questions).
- **Ansible-style batches** (`serial`). A sliding window with
  `max_concurrent` finishes sooner and needs one field fewer.

## Consequences

- A Rundeck job's node steps and dispatch settings move field by field.
- Rolling deploys are a `dag.run` step with `nodes` and `max_concurrent`.
- Operators can narrow a run to a few hosts without edit rights.
- Retrying a deploy costs only the hosts that failed.
- A `node: required` DAG always runs bound, so its command steps never run on
  the Dagu host by mistake. The run header shows the binding.

## Open questions

1. Should `max_concurrent` default to `1`, as proposed and as in Rundeck, or
   follow `defaults.max_concurrent`, which is `10`?
2. Should step `env` be sent to SSH nodes? Most servers reject `SendEnv`, so
   it would be exported in the remote shell's input.
3. Should `max_concurrent` and a new failure threshold accept percentages,
   such as `25%`, for rolling deploys over fleets of changing size?
4. Should Dagu answer `sudo` password prompts from a secret, as Rundeck's
   `sudo-password-storage-path` does?
5. Should node-first runs be possible in one DAG document, without a child?
6. Should an author be able to forbid narrowing, as Rundeck's
   `nodeFilterEditable: false` does, or start the dialog with no node
   selected?
7. Should `dag.enqueue` accept `nodes`?
8. Should an author be able to make a node removed before a retry fail the
   step, instead of being `removed`?
9. Should the object form accept a local fan-out, which runs a step once per
   matched node on the Dagu host, with `node` available and no child run?
   Today a Rundeck `localexec` step in a step-first job imports as `dag.run`
   with `nodes` over a bound child (RFC 018), which stores one child run per
   node per execution.
