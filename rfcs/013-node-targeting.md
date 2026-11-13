# RFC 013: Node Targeting

- **Status:** Proposed
- **Date:** 2026-11-12
- **Affected areas:** `spec`, `ir`, `runtime` (node fan-out, retry),
  `runtime/builtin/ssh`, `service/coordinator`, `service/worker`,
  `service/frontend` (API, UI), `service/mcp`, `cmd`, `api/v1`, `proto`,
  `ui`, `specs`, and `docs`

## Summary

Let a step run on every node a selector matches in the inventory of
[RFC 012](012-node-inventory.md):

```yaml
steps:
  - id: restart
    run: sudo systemctl restart app
    nodes:
      labels: web,env=prod
      max_concurrent: 2
```

The step runs once per matched node, over SSH, with a status, log, and output
per node. It sets how many nodes run at once and whether the rest start after
one fails. A retry reruns only the nodes that did not succeed. An operator
can narrow a run to named nodes, never widen it.

## Problem statement

Dagu can run a command on many hosts only through `parallel`, which works for
`dag.run` steps alone:

| Gap | What happens today |
| --- | --- |
| No fan-out over hosts | A DAG takes a host list as a parameter and runs `parallel` over a child DAG with one `ssh.run` step. Every DAG repeats this |
| No per-host view | The parent step lists child runs. There is no grid of hosts by steps and no log with host names on each line |
| No failure policy | `parallel` runs every item and fails at the end. A deploy needs to stop at the first failed host |
| No narrowing | An operator cannot run a DAG on two of its twenty hosts without editing it |

## Goals

1. One field makes a step run on every node a selector matches.
2. Concurrency and failure policy are set on the step.
3. Each node's status, log, and output can be seen on its own, and a retry
   reruns only the nodes that did not succeed.
4. An operator can narrow a run's nodes and can never widen them.
5. Nothing changes for DAGs that do not use `nodes`.

## Non-goals

- Limiting which nodes a user may target.
- The items in [Future enhancements](#future-enhancements).

## Design

### The `nodes` field

`nodes` is an optional step field. A string or a list is an RFC 012
selector:

```yaml
nodes: web,env=prod,!canary
```

A selector that starts with `!` is quoted, since YAML reads a leading `!` as
a tag.

The object form adds dispatch settings:

| Field | Default | Meaning |
| --- | --- | --- |
| `labels` | Required | An RFC 012 selector. It can use `${params.*}`, `${consts.*}`, and step outputs, as [References in a selector](#references-in-a-selector) defines |
| `max_concurrent` | `1` | Nodes running at once, `1` through `1000` |
| `keep_going` | `false` | Whether other nodes start after one fails |

The default `max_concurrent` is `1`. A command that restarts a service should
not reach every host at once unless the author says so.
`defaults.max_concurrent` from Spec 018 does not apply to `nodes`.

### References in a selector

A reference in a selector must resolve to a valid label key or value
(`internal/ir/label.go`). Anything else fails the step before any node
starts. A label key or value holds no `,`, `!`, `=`, `*`, or `?`, so a
run-time value cannot add a term, negate one, or widen a match:

```yaml
nodes: web,env=${params.env}     # "prod" works; "prod,!canary" fails
```

An operator who needs fewer nodes narrows the run.

### Where a node item runs

| Step | On a node |
| --- | --- |
| `run`, `command`, or `script` with no `action` | Over SSH |
| `ssh.run` | Over SSH |
| `sftp.upload`, `sftp.download` | Over SFTP |

Node items ignore DAG-level `ssh` and `container`, whether the DAG writes them
or inherits them from base config. The DAG's other steps keep using them.

- The connection opens from the host that runs the run: the Dagu server, or
  the worker that runs it. The node supplies the connection.
- On `ssh.run` and `sftp.*`, connection fields under `with`, such as `user`
  or `key`, override the node's.
- The shell is the step's `with.shell`, then the node's `shell`, then
  `/bin/sh`.
- Only a `working_dir` set on the step itself applies on the node.
- Node items behind one bastion share one connection to it.

The item's text goes to the shell's standard input, never as a `sh -c`
argument, and step `env` is not sent. Unqualified `$NAME` belongs to the
remote shell. Dagu resolves only its own references before sending, such as
`${params.x}`, `${env.NAME}`, and `${node.name}`. So a secret reaches a node
item as `${env.NAME}`: it travels inside the script, never in a process
argument, and the run's masker hides it in logs. `dagu validate` gives a
notice for an unqualified name that matches a param, env entry, or secret,
with the `${env.NAME}` fix.

### Validation

`dagu validate` fails when:

- `nodes` is on a step other than those in the table above.
- `nodes` is on a step with `parallel`, `foreach`, `worker_selector`,
  `container`, or `repeat_policy`.
- `with.host` or `with.ip` is set on an `ssh.run` or `sftp.*` step with
  `nodes`, or `with.shell_packages` on any step with `nodes`.
- An `sftp.download` step with `nodes` has a `with.destination` that does not
  use `${node.name}`. Every node would write the same local path.
- A literal selector is invalid.
- The object form has an unknown field, a missing `labels`, or an
  out-of-range value.

Validation does not read the inventory, so it works offline and in CI.

### Matching

- The selector is resolved and matched when the step starts, after its
  dependencies and preconditions. The run's [node list](#narrowing-a-run),
  if any, is applied.
- Nodes are taken in name order. The list is recorded in the step's status,
  and retries use it.
- A resolution error fails the step before any node starts.
- A step that matches no nodes fails with "selector matched no nodes".
- A matched node that is disabled (RFC 012) is not run. It is `disabled`,
  and does not fail the step. A step whose matched nodes are all disabled
  fails.
- After narrowing, a step can have at most 1,000 nodes, as Spec 018 limits
  items.

### Concurrency and failure

```text
max_concurrent: 2, keep_going: false

web01 ████████✓
web02 ██████████████✗
web03          ███████████✓      started before web02 failed, so it finishes
web04                      ·     not started
step: failed
```

- Nodes start in order, at most `max_concurrent` at a time.
- With `keep_going: false`, no node starts after a node fails. Running nodes
  finish. The rest are `not_started`. The step fails.
- With `keep_going: true`, every node runs. The step fails if any node failed.
- The step succeeds only when every node it runs succeeds.
- `continue_on` applies to the step's result, as for any step.
- Aborting the step stops pending nodes and closes their sessions. Their
  items are `aborted`.

Node items do not count against `max_active_steps`. The step counts as one.

### Step fields on a node step

| Field | Applies to |
| --- | --- |
| `timeout_sec` | Each node. One hung host does not use up the others' time |
| `retry_policy` | Each node. A failed node retries alone |
| `preconditions` | The step, once, before matching |
| `continue_on` | The step |
| `output`, `outputs` | The step's aggregate. See [Outputs](#outputs) |

### Per-node records and logs

- Each node item has its own record and log in the step's directory, as
  `foreach` items do. The step's status holds the matched list and, when the
  step ends, the name, status, and exit code of each node. It holds no
  stdout.
- When a node finishes, a line with its name, status, and exit code is
  appended to the step's outcome file and synced to disk. A retry after a
  killed run process, a reboot, or a lost worker reads it to know which
  nodes succeeded.
- The step's own stdout and stderr hold every node's lines, each prefixed by
  its node name.
- On a worker, the worker streams each node's log with its node name, and
  sends the records and outcome lines to the coordinator, which writes them
  into the server's step directory. The server shows a run on a worker as it
  shows a local one.

### The `node` namespace

The Spec 003 namespace registry gains `node`, available in every
value-resolved field of a step with `nodes`, except the selector:

| Reference | Value |
| --- | --- |
| `${node.name}` | Node name |
| `${node.host}`, `${node.port}`, `${node.user}` | Connection fields |
| `${node.description}` | Description |
| `${node.labels}` | Labels, comma-separated, sorted, as `key` or `key=value` |
| `${node.labels.<key>}` | The value of the label with that key. Empty for a label with no value |
| `${node.labels."<key>"}` | The same, for a key such as `gpu.type`, as Spec 018 quotes item fields |

A reference to a label the node lacks fails that node's item, naming the node
and the key. The text goes to a remote shell, where it would fail later and
less clearly. Outside a node step, a `${node.*}` reference is kept as text,
with a notice.

### Narrowing a run

A run can carry a list of node names. It applies to every `nodes` step in the
run and in its child runs. A node runs only when the step's selector matches
it and the list names it.

```text
step selects   web           → web01 web02 web03 web04
run narrowed   web01 web03
runs on                      → web01 web03
```

| Surface | Node list |
| --- | --- |
| CLI `dagu start`, `dagu enqueue` | `--node web01 --node web03`, repeatable as `--worker-label` is |
| API start, start-sync, and enqueue | `targetNodes` |
| MCP `dagu_execute` start and enqueue | `targetNodes` |

- Admission rejects a name that no inventory node has, so a mistyped `web1`
  never starts a run. It also rejects a list on a DAG with no `nodes` step
  and no `dag.run` step.
- A step that matches nodes, but none in the list, is skipped, and the skip
  does not block the steps after it. Narrowing a run to two web hosts neither
  fails its database step nor skips the web steps after it. A run whose list
  empties every node step that matched nodes fails with "no listed node was
  selected".
- The list is stored with the run when it is admitted. A queued run, a
  retry, and `dagu restart` keep it.
- The list is shown in the run header and written to the audit entry of the
  start.

A list can only remove nodes, so a user who may run a DAG cannot use it to
reach a node the DAG would not reach.

### Retry

- Retrying a node step, through `dagu retry` or the UI, reruns its `failed`,
  `aborted`, and `not_started` nodes. Succeeded nodes keep their recorded
  results.
- The retry uses the node list recorded by the first attempt. A node added to
  the inventory since then is not added. A node removed since then is not
  run. It is `removed`, and does not fail the step. Hosts often leave the
  inventory because they failed, so the retry still finishes the others.
- Connection settings are read again from the current inventory, so a rotated
  key works on retry.

The UI shows **Retry failed nodes**, with a count, on a node step and on a
run that has one.

### Outputs

The step's `output` variable (Spec 012) holds JSON:

```json
{
  "summary": { "total": 3, "succeeded": 1, "failed": 2 },
  "nodes": [
    { "name": "web01", "status": "succeeded", "exit_code": 0, "output": "..." },
    { "name": "web02", "status": "failed", "exit_code": 1, "error": "..." },
    { "name": "web03", "status": "not_started" }
  ]
}
```

- `failed` counts every node not counted as successful, as Spec 018 defines.
  `removed` and `disabled` nodes count in `total` only.
- A node's `status` is `succeeded`, `failed`, `aborted`, `not_started`,
  `removed`, or `disabled`.
- `output` is the node's captured stdout. The whole aggregate is limited by
  `max_output_size`, and each node gets an equal share. The node's log keeps
  the full output.
- Declared `outputs` appear per node as `nodes[].outputs`.
- `${steps.<id>.outputs.<name>}` on a node step is not defined. Steps read the
  aggregate.

### Run view

```text
┌ deploy ── run 2026-11-12 09:14 ── nodes: web01 web02 web03 ───────┐
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
- The **Nodes** tab shows nodes by node steps, failed nodes first, one page
  at a time, and updates live. Each cell opens that node's log for that
  step.
- The step log shows **All nodes** and one tab per node. The step log API and
  MCP's `step_log` target take a node name.
- The DAG editor shows the match count next to each literal selector.

### Coming from Rundeck or Ansible

The migration guide and the import tool of RFC 012 use these mappings.

| Rundeck job | Dagu |
| --- | --- |
| A filter on groups, `a+b` | `nodes: a,b` |
| A filter on an attribute, `env: prod` | `env=prod` |
| An excluded group or attribute | `!dev`, `!env=dev` |
| A filter on names, a regular expression, OR, or every node | A label that the import tool adds to the nodes it matches |
| `nodefilters.dispatch.threadcount` | `nodes.max_concurrent` |
| `nodefilters.dispatch.keepgoing` | `nodes.keep_going` |
| `sequence.strategy: step-first` | `nodes` on each node step |
| `${node.name}`, `${node.hostname}`, `${node.username}`, `${node.<attr>}` | `${node.name}`, `${node.host}`, `${node.user}`, `${node.labels.<attr>}` |
| Any other dispatch setting or strategy | Not supported. The import tool reports it |

| Ansible | Dagu |
| --- | --- |
| `hosts:` naming groups | `nodes` with those groups as labels |
| `forks` | `nodes.max_concurrent` |
| `any_errors_fatal: true` | `keep_going: false` |
| `--limit` | `--node` |
| `run_once`, `delegate_to: localhost` | A step without `nodes` |

## Security

- A step can match only nodes in its run's project inventory.
- A reference in a selector resolves to one label key or value, so a run-time
  value cannot add terms, negate them, or widen a match.
- A node list can only remove nodes. It is recorded and audited.
- Credentials resolve as RFC 012 defines, are kept in memory, and are masked
  in every log and status. Values never enter run status.
- Host keys are checked as RFC 012 defines, on a worker too. A run never
  trusts a new key.
- Node fields and label values follow RFC 012's character rules and are safe
  to insert unquoted. The docs still quote them in shell commands:
  `"${node.labels.role}"`.
- An item's text goes to the shell's standard input, so a secret in it never
  reaches a process argument.

## Failure behavior

| Event | Behavior |
| --- | --- |
| The inventory cannot be read, or the selector is invalid | The step fails before any node starts, with the error |
| A reference in a selector is not a valid label key or value | The step fails before any node starts |
| SSH connection or authentication fails | That node fails, with the host, port, error, and the hop that failed: the bastion or the node |
| The host key is unknown or has changed | That node fails, with the fingerprint. The Nodes page can trust a new key |
| A node's credential ref does not resolve | That node fails, naming the field and ref. Other nodes follow `keep_going`. Nodes that share the ref do not resolve it again in that attempt |
| A node is gone from the inventory at retry | That node is `removed` and not run |
| The run process is killed, or its worker is lost, during a node step | The run fails. A retry reads the outcome file and reruns only nodes that did not succeed |
| `${node.labels.<key>}` names a label the node lacks | That node fails, with the node name and the label key |
| A node list names a node not in the inventory, or is given for a DAG with no node steps | The run is rejected at admission |
| A node list emptied every node step that matched nodes | The run fails with "no listed node was selected" |

## Compatibility and rollout

- `nodes` is a new step field, and `node` a new namespace.
- `${node.*}` was ordinary text before. Inside node steps it now resolves.
  Elsewhere it is still kept, with a notice.
- No existing step changes behavior.

Rollout:

1. **Step-first over SSH**: `nodes` on `run`, `command`, `script`, `ssh.run`,
   and `sftp.*`, with matching, concurrency, failure policy, the `node`
   namespace, per-node records and logs, the aggregate output, the Nodes
   tab, and the editor's match count. A new spec, with conformance tests
   against an SSH server the test harness starts.
2. **Retry failed nodes.**
3. **Narrowing** on the CLI, API, and MCP.
4. **Runs on workers**: node steps in a run on a worker, with RFC 012's
   `ResolveNodes`.
5. **Docs**: "Coming from Rundeck" and "Coming from Ansible", with the
   mappings above.

## Verification and acceptance criteria

The sections above state the rules, and the new spec tests each one. This RFC
is accepted when the following hold.

1. A `run` step with `nodes: web` over three SSH nodes runs on each, and its
   aggregate lists them in name order.
2. `max_concurrent` and `keep_going` start and stop nodes as
   [Concurrency and failure](#concurrency-and-failure) states.
3. Every case in [Failure behavior](#failure-behavior) ends as stated.
4. `dagu retry` reruns only the nodes that did not succeed, including after
   the run process is killed mid-step.
5. A node list limits every node step and child run, and a mistyped name is
   rejected before the run starts.
6. A secret used by a node item never appears in logs, status, output, or a
   process argument, on the server or a worker.
7. A node step in a run on a worker shows the same Nodes tab, badge, and
   per-node logs on the server as a local one.
8. A step over 1,000 nodes keeps its run status small and its aggregate
   within `max_output_size`.
9. A DAG without `nodes` behaves as before.

## Alternatives considered

- **Keep `parallel` over a host parameter.** Works today, but every DAG
  repeats the host list and the child DAG, and the run shows child runs, not
  hosts.
- **Expand `nodes` into `foreach`.** A smaller runtime change, but `foreach`
  has no stop-at-first-failure, and its items are values, not hosts with
  connections.
- **Batches**, as Ansible's `serial`. A sliding window with `max_concurrent`
  finishes sooner and needs one field fewer.
- **Narrowing with a selector.** An operator would learn which terms widen
  and which narrow. A list of names says exactly which hosts run.

## Consequences

- Rundeck and Ansible jobs move with the mappings above.
- Operators can narrow a run to a few hosts without edit rights.
- Retrying a deploy costs only the hosts that failed.

## Future enhancements

These are left out to keep the first version small. Each fits on top of the
design above without changing it.

- **Node-first runs, for rolling deploys.** A DAG that declares
  `node: required` runs bound to one node, and a `dag.run` step with `nodes`
  starts one bound child per node, so every step runs on a node before the
  next node starts. A step in a bound run can opt out and run on the Dagu
  host, such as a load balancer call. `dagu start --bind-node` runs one
  alone.
- **A node checklist in the run dialog**, listing the nodes the DAG's steps
  match, to fill the node list.
- **Succeeding on no match**, for a step whose nodes may not exist yet.
- **Ordering by a label** instead of by name.
- **Rerunning every node on retry**, not only those that did not succeed.
- **Percentages** for `max_concurrent` and a failure threshold, for fleets of
  changing size.
- **Answering `sudo` prompts** from a secret.
- **Sending step `env`** to SSH nodes.
- **Letting an author forbid narrowing.**
- **`nodes` on `dag.enqueue`.**
- **A local fan-out** that runs a step once per matched node on the Dagu
  host, with `node` available.
