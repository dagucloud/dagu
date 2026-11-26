# Spec: Node Targeting

## Status

Partially implemented. The `nodes` step field, the `node` DAG field, their
validation, running a step as one child run per node, binding SSH nodes into
their child runs, the `node` namespace, the records of node runs with the
API that reads them, narrowing a run to listed nodes, and retrying only the
nodes that did not succeed are implemented. Conformance covers validation
through `dagu validate`, runs through `dagu start` against SSH servers the
test harness starts, node runs through the API, narrowed runs through `dagu
start`, `dagu start --from-run-id`, and `dagu retry`, and retries of steps
with nodes through `dagu retry`. Bound runs and steps with nodes on SSH
nodes run on workers too, which Go integration tests cover, since the
conformance harness starts no worker. Further sections are added as RFC 013
lands.

## Scope

This spec defines how a workflow names the inventory nodes ([RFC 012](../rfcs/012-node-inventory.md))
a step runs on, which steps and DAGs may do so, how such a step runs one
child run per node, and how a child run reaches its node. It does not define
the inventory, its credentials, or its host key checks.

## Goal

A workflow names the hosts it acts on by role, and a mistake that would send
commands to the wrong place fails validation instead of running.

## Behavior

### The `nodes` field

`nodes` is an optional field of a step. It takes one of:

- a selector string: label filter terms separated by commas, as
  [Spec 087](087-label-filters.md) defines, such as `web,env=prod,!canary`;
- a list of selector terms, such as `[web, "!canary"]`;
- an object:

| Field | Default | Meaning |
| --- | --- | --- |
| `labels` | Required | A selector, as a string or a list of terms |
| `max_concurrent` | `1` | Nodes running at once, an integer from 1 to 1000 |
| `keep_going` | `false` | Whether the remaining nodes start after one fails |

A selector that holds a reference, such as `env=${params.env}`, is checked
when the step starts. Every other selector is checked when the DAG is
validated. A YAML scalar that starts with `!` is quoted, since YAML reads a
leading `!` as a tag.

`nodes` is allowed on a `dag.run` step and on command steps: `run`,
`command`, or `script` with no `action`, `ssh.run`, and `sftp.upload` and
`sftp.download`. A step with `nodes` runs on each node, so a DAG-level
`container` or `ssh` does not apply to it, and a `run` step under either is
still a command step.

### The `node` DAG field

`node: required` makes a DAG run only bound to one node: as the child of a
`dag.run` step with `nodes`, or started with exactly one node. `required` is
the only value. A worker selector given when such a DAG is started or
enqueued is refused, since the bound node decides where it runs.

### Running a step on its nodes

When a step with `nodes` starts, after its dependencies and preconditions,
its selector is resolved and matched against the project's nodes:

- Each part of a term that holds a reference must resolve to one label key
  or value, so a value from the run cannot add a term, negate one, or widen
  the match. `env=${params.env}` works with `prod` and fails with
  `prod,!canary`.
- The matched nodes are taken in name order. A step matches at most 1,000.
- A disabled node is not run. A step whose matched nodes are all disabled
  fails.

Each enabled node gets its own child run, bound to that node:

- On a `dag.run` step, the child runs the named DAG, which must declare
  `node: required`. The step fails before any child starts when it does not.
- On a command step, the child runs that step alone, in a `node: required`
  DAG with the parent DAG's settings. The step reads what it would read
  without `nodes`: the parent run's params, its env, and the outputs of
  earlier steps.
  `timeout_sec` and `retry_policy` apply
  to each node's run of the step. `depends`, `preconditions`, `continue_on`,
  and `output` apply to the step with `nodes`.

A bound child runs on the host that runs its parent, even when the default
execution mode is distributed.

Nodes start in name order, at most `max_concurrent` at a time.
`defaults.max_concurrent` does not apply. With `keep_going: false`, no node
starts after a node fails: the nodes running finish, and the rest do not
start. With `keep_going: true`, every node runs. Either way the step fails
when a node failed. Aborting the step stops the nodes not started and aborts
the running children. Otherwise the step's status follows `parallel`
([Spec 018](018-parallel-and-foreach.md)).

The step writes `parallel`'s aggregate to its standard output, with one entry
per matched node, in name order. Each entry adds `node`. A node that did not
run has the status `not_started`, `disabled`, or `removed`
([Retrying a step with nodes](#retrying-a-step-with-nodes)), and counts in
`summary.total` only. When the aggregate would exceed `max_output_size`, its
entries and `outputs` leave out the outputs, which each child run keeps.

```json
{
  "summary": {"total": 3, "succeeded": 1, "failed": 1},
  "results": [
    {"node": "web01", "name": "deploy-one", "dagRunId": "...", "status": "failed"},
    {"node": "web02", "status": "disabled"},
    {"node": "web03", "name": "deploy-one", "dagRunId": "...", "status": "not_started"}
  ],
  "outputs": []
}
```

The step's status details list the matched nodes in name order. A disabled
or removed node shows as skipped.

### Bound runs

A run bound to an SSH node reaches the node as a run with DAG-level `ssh`
reaches its host. Steps with no action, `ssh.run`, and `sftp.*`, including
handlers and `foreach` bodies, connect to the node, through its bastion.
Other actions run where the run runs. Fields under the `with` of an
`ssh.run` or `sftp.*` step override the node's, except that a step that sets
`with.host` or `with.ip` connects to that host as it would outside a bound
run. A key or a password under `with` replaces all of the node's
credentials. `with.known_host_file` and `with.strict_host_key` are errors,
since a node's inventory decides which host keys it accepts, and so is
`with.bastion`, since the node connects through the bastion its inventory
sets.

The node's credentials resolve once per run, before its first step, when a
step connects to the node, and join the run's masking. A run whose steps
never connect resolves none. Each hop accepts only its own node's
`host_key` or trusted key, unless the node sets `strict_host_key: false`. A
refused key fails the step before any credential is sent.

### Runs on workers

A step with `nodes` and a bound run behave on a worker as on the Dagu host.
The worker reads nodes from the coordinator: the nodes a step's selector
matches, a bound run's node and its bastion, and the host keys trusted for
them, each lookup once. A node's credentials resolve through the
coordinator as well, which serves a ref only to an attempt whose DAG has a
step with `nodes` or is `node: required`, and only when the connection of
the enabled node that the run names, or of its bastion, uses the ref. Host
keys are checked on the worker, against the node's `host_key` or the key
trusted when the run looked the node up. A run never trusts a key.

The child runs of a step with `nodes` run on the worker that runs their
parent. Each child run reads its own node again, so a run never reuses an
answer another run read. A worker that cannot reach the coordinator, or
whose coordinator answers no lookup, fails the step or the run as an
unreadable inventory does.

### The `node` namespace

Every value-resolved field of a bound run reads its node:

| Reference | Value |
| --- | --- |
| `${node.name}` | Node name |
| `${node.host}`, `${node.port}`, `${node.user}` | Connection fields. `port` is `22` when the node sets none |
| `${node.description}` | Description |
| `${node.labels}` | Labels, sorted and comma-separated, as `key` or `key=value` |
| `${node.labels.<key>}` | The value of the label with that key, empty for a label with no value |
| `${node.labels."<key>"}` | The same, for a key with a dot, such as `gpu.type` |

Outside a bound run, a `${node.*}` reference is kept as text, with a
runtime-only notice. A run with a variable named `node`, such as a step
output, reads `${node.<path>}` as a JSON path into that variable instead,
as it did before the namespace.

### Narrowing a run

A run can carry a list of node names. In every step with `nodes` of the
run and of its child runs, a node runs only when the step's selector matches
it and the list names it. The 1,000-node limit applies after the list.

| Surface | List |
| --- | --- |
| `dagu start`, `dagu enqueue` | `--node`, repeatable |
| API start, start-sync, and enqueue, for a DAG file or an inline spec | `targetNodes` |
| MCP `dagu_execute` start and enqueue | `targetNodes` |
| Web UI run dialog | a Node field, for a `node: required` DAG only |

The list is checked when the run is started or enqueued, before it exists,
and a mistake fails the request:

- each name must be a node of the project, compared without case, so a
  mistyped name never starts a run;
- a `node: required` DAG takes exactly one node, and the run is bound to it
  as a child of a `dag.run` step with `nodes` is. The start and enqueue
  requests refuse such a DAG without a node; `dagu start` and `dagu enqueue`
  record a run that fails before any step;
- any other DAG takes a list only when a step, a `foreach` body, or a
  handler sets `nodes` or runs a child DAG;
- a start of selected steps takes no list.

A narrowed run is dispatched as any run is, and a run dispatched to a
worker carries its list there.

The run keeps the list, spelled as the inventory spells the names, in its
status. Queued runs, retries, edit-and-retry runs, and `dagu restart` keep
it. A new run from a run, by `dagu start --from-run-id` or the reschedule
request, keeps it too, as it keeps the run's params, and checks it again as
a start does, so `--from-run-id` takes no `--node`. Run details carry the
list as `targetNodes`, the run header shows it unless it names only the
node the run is bound to, and the audit entry of the start or reschedule
records it.

A step whose selector matches nodes, none of which the list names, is
`skipped`, and the skip does not block the steps after it, as a step skipped
by a precondition would. A run whose list leaves every step with nodes that
matched nodes without a node, those of `foreach` bodies and of its `dag.run`
child runs included, fails with `no listed node was selected`. The steps
decide this before the handlers run, as they decide any other failure, so
the handlers that run are those of the failure. A run whose steps leave no
listed node and match none, and whose handlers with nodes then leave every
one of them without a node, fails the same way after its handlers. A step
that matched no node, such as one a precondition skipped or a `foreach`
with no items, does not count. A retry counts what the attempts before it
did, so the steps it keeps still count.

A child run of a narrowed run, started by `dag.run` or `dag.enqueue`, keeps
the list. A `dag.run` child run runs where its parent runs, so a child DAG
that selects workers fails the step that starts it, and it does not fail
for the list: its steps count toward the run it is part of. A `dag.enqueue`
child run is a run of its own, may select workers, and fails for the list
as any run does.

### Retrying a step with nodes

A retry of a run, by `dagu retry` or the retry request, with or without a
step, runs a step with nodes again only on the nodes that did not succeed:

- The step takes its nodes from the records of the latest attempt that
  recorded them ([Viewing node runs](#viewing-node-runs)) when it can read
  them, and does not match its selector again. Those are the nodes its first
  attempt matched, unless an attempt since matched the selector again, as
  described below. A node added to the inventory since is not added. A later
  retry keeps the same nodes, also after an attempt that reset the step
  without running it.
- A node whose child run succeeded or partially succeeded keeps its result
  and does not run.
- Any other node runs its recorded child run again, as `dagu retry` retries
  a run, or starts it if it never started. The node is read from the
  inventory as it is now: its connection settings and credentials, and
  whether it is disabled. A node disabled now does not run, and keeps its
  child run for a later retry.
- A node that has left the inventory since, and whose child run did not
  succeed, is `removed`. It does not run, does not fail the step, and counts
  in `summary.total` only. The step fails when every node it keeps is
  removed or disabled and none kept a result ([Errors](#errors)).
- After the run's process died during the step, the retry does the same.
  A child run still recorded as running runs its unfinished steps again.

A step whose previous attempt recorded no nodes, such as one whose selector
failed or one recorded before this version, matches its selector again, and
so does a step whose records cannot be read or parsed, with a warning in the
run log. Such a step can then run on a node added since, and a later retry
keeps the nodes it records then. A step that a run
reused from another run, as `--reuse-from`, `--only` with `--outputs-from`,
and edit and retry reuse steps, matches its selector too, since that run's
child runs are not this run's.

The run view labels the retry of a step with nodes **Retry failed nodes**,
with the number of its nodes that failed, were aborted, or did not start.
The confirmation of a retry of a root run states that number for all its
steps with nodes.

### Viewing node runs

A step with nodes records the run of each node it matched beside its log
files, outside the run status, so a step over many nodes does not grow the
status. A record holds the node, its status, its child run, and when the run
started and finished. Once the child run returns, the times are the child
run's own, so a child that a retry reused keeps the times it ran. Once the
child run finishes, the record adds how many
of its steps succeeded and the step that failed last. A node's status is one
its child run reached, or `not_started`, `running`, `disabled`, or
`removed`. A child run that returned while still queued, running, or not
started, as one that a retry reused can, is `failed`, as the step counts it. One that returned
waiting for approval stays `waiting`. The step
records its nodes before any of them starts, again at most once a second
while they change, and once more when it ends. A node running when the step
is aborted is `aborted`. When the run stops without that last write, as when
its process dies, a node still recorded as running reads as `aborted` once
the step is aborted, and as `failed` once the step ends otherwise.

`GET /dag-runs/{name}/{dagRunId}/node-runs` lists a run's node runs, and
`GET /dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/node-runs` those
of a sub-run:

- `steps` names the steps with nodes that recorded, in run order, with
  their counts by status. A partially succeeded run counts as succeeded and
  a rejected one as failed.
- `items` holds one row per node, with its run for each of those steps that
  matched it.
- Rows with a failed, aborted, or rejected run come first, then rows with a
  running or waiting run, then the rest, each group in node name order.
  `failedOnly=true` keeps only the first group. `page` and `perPage` page
  the rows.

Run details carry the same counts as `nodeRuns` on each step with nodes
that recorded, and a bound run's node as `boundNode`. A run recorded before
this version has no rows and no counts.

## Errors

`dagu validate` fails, naming the field, when:

- `nodes` is on any other step, or on a step that also sets `parallel`,
  `foreach`, `worker_selector`, `container`, or `repeat_policy`;
- `with.host` or `with.ip` is set on an `ssh.run` or `sftp.*` step with
  `nodes`, since each node gives the host;
- `with.shell_packages` is set on a step with `nodes`;
- an `sftp.download` step with `nodes` has a `with.destination` that does not
  use `${node.name}`, since every node would write the same local path;
- a selector without a reference has an invalid term or no terms, or a list
  form has an empty term;
- the object form has an unknown field, no `labels`, a `max_concurrent`
  outside 1 to 1000, or a `keep_going` that is not a boolean;
- `node` has a value other than `required`;
- a `node: required` DAG sets `ssh`, `container`, or `worker_selector`
  (`local` included), or has a step with `nodes`;
- a DAG with a `workspace` label sets `node: required` or has a step with
  `nodes`, since inventories belong to the installation's one project until
  workspaces become projects.

`dagu start`, `dagu enqueue`, and the start and enqueue requests fail
before any run exists when a node list names a name that is not a node of
the project (`node "web1" is not a node of the project`), is given for a DAG
that has nothing to narrow, or starts selected steps. The
start, enqueue, and reschedule requests also fail so for a `node: required`
DAG without exactly one node, and a reschedule for a list that no longer
passes these checks. `dagu start` refuses `--node` with `--from-run-id`.

A step with `nodes` fails before any node starts, with the error, when:

- a reference in its selector resolves to anything but one label key or
  value;
- its selector matches no nodes (`selector matched no nodes`), only disabled
  nodes, or more than 1,000;
- it is retried, and each of its recorded nodes has left the inventory or is
  disabled, with none that keeps a result;
- the inventory cannot be read;
- it is a `dag.run` step whose child DAG does not declare `node: required`.

A run fails before any step starts when:

- it is a `node: required` DAG started without a node, or with a list of
  other than one node;
- it is bound to a node that is not in the inventories, or is disabled;
- it reads `${node.labels.<key>}` for a label its node lacks, naming the
  node and the key;
- it reads a `${node.<path>}` that the namespace does not define, such as
  `${node.nmae}`, naming the reference;
- a credential of its node is a secret ref that does not resolve, naming the
  field and the ref.

A step of a bound run fails, naming the hop, when its node's or bastion's
host key is unknown or has changed.

## Examples

This DAG validates. Until a child run reaches its node, both of its node
steps fail on each node before any command runs, as [Status](#status) says.

```yaml
steps:
  - id: rollout
    action: dag.run
    with:
      dag: deploy-one
    nodes:
      labels: web,env=prod
      max_concurrent: 1
  - id: clean
    run: rm -rf /tmp/app-cache
    nodes: web,env=prod
---
name: deploy-one
node: required
steps:
  - run: systemctl restart app
```
