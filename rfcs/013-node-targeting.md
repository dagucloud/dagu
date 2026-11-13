# RFC 013: Node Targeting

- **Status:** Proposed
- **Date:** 2026-11-12
- **Affected areas:** `spec`, `ir`, `runtime` (node fan-out, bound runs,
  retry), `runtime/builtin/dag`, `runtime/builtin/ssh`, `service/scheduler`,
  `service/coordinator`, `service/worker`, `service/frontend` (API, UI),
  `service/mcp`, `cmd`, `api/v1`, `proto`, `ui`, `specs`, and `docs`

## Summary

Run a workflow, or one step, on every node a selector matches in the
inventory of [RFC 012](012-node-inventory.md):

```yaml
steps:
  - id: deploy
    action: dag.run
    with:
      dag: deploy-one
    nodes:
      labels: web,env=prod
      max_concurrent: 1

  - id: clean
    run: rm -rf /tmp/app-cache
    nodes: web,env=prod
```

`deploy` starts one child run of `deploy-one` per matched node, bound to that
node. On an SSH node, the child's commands run over SSH. On a worker, the
child runs on that worker. With `max_concurrent: 1`, web01 finishes every
step before web02 starts: a rolling deploy.

`nodes` on a command step is shorthand for the same thing with a one-step
child. The step sets how many nodes run at once and whether the rest start
after one fails. A retry reruns only the nodes that did not succeed. An
operator can narrow a run to named nodes, never widen it.

## Problem statement

| Gap | What happens today |
| --- | --- |
| No fan-out over hosts | A DAG takes a host list as a parameter and runs `parallel` over a child DAG with one `ssh.run` step. Every DAG repeats this |
| No rolling deploy | Nothing runs a whole workflow on one host before the next host starts |
| No fan-out over workers | `worker_selector` gives a run to any one matching worker (`internal/service/coordinator/handler.go`). Nothing runs a DAG on every worker of a site |
| No per-host view | The parent step lists child runs. There is no grid of hosts by steps |
| No failure policy | `parallel` runs every item and fails at the end. A deploy needs to stop at the first failed host |
| No narrowing | An operator cannot run a DAG on two of its twenty hosts without editing it |

## Goals

1. One field runs a workflow, or one step, on every node a selector matches,
   SSH hosts and workers alike.
2. A workflow can run on one node at a time, every step on a node before the
   next node starts.
3. Concurrency and failure policy are set on the step.
4. Each node's status, log, and output can be seen on its own, and a retry
   reruns only the nodes that did not succeed.
5. An operator can narrow a run's nodes and can never widen them.
6. Nothing changes for DAGs that do not use `nodes`.

## Non-goals

- Limiting which nodes a user may target.
- The items in [Future enhancements](#future-enhancements).

## Design

### Bound runs

A run can be bound to one node. Its steps read the node as `${node.*}`. A DAG
that is meant to run on a node declares it, and never runs unbound:

```yaml
# deploy-one.yaml
node: required
params:
  - version: ""
steps:
  - id: drain
    action: http.request
    with:
      method: POST
      url: https://lb.internal/pools/web/${node.name}/drain
  - id: install
    run: sudo apt-get install -y app=${params.version}
  - id: restart
    run: sudo systemctl restart app
  - id: enable
    action: http.request
    with:
      method: POST
      url: https://lb.internal/pools/web/${node.name}/enable
```

| Node | Where the bound run runs | Its steps |
| --- | --- | --- |
| SSH node | Where a `dag.run` child runs today: on the host that runs its parent | The node becomes the run's DAG-level `ssh`. Steps with no action, `ssh.run`, and `sftp.*` reach the node. Other actions, such as `http.request`, run on the host that runs the run, as under DAG-level `ssh` today (`internal/spec/step.go`) |
| Worker node | On that worker, dispatched by its ID | Every step runs on the worker, as in any run dispatched to it. `ssh.run` and `sftp.*` without `with.host` fail, since the node has no SSH connection |

- Commands reach an SSH node as `ssh.run` sends them today, and connection
  fields under `with` override the node's.
- A `node: required` DAG sets no `ssh`, `container`, or `worker_selector`,
  and has no `nodes` step. The node decides where it runs. An `ssh` or
  `container` inherited from base config does not apply to a bound run.
- Starting a `node: required` DAG without exactly one node is rejected at
  admission.

### The `nodes` field

`nodes` is an optional field on a `dag.run` step and on a command step: `run`,
`command`, or `script` with no `action`, `ssh.run`, and `sftp.*`. A string or
a list is an RFC 012 selector:

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

- **On a `dag.run` step**, each matched node gets its own child run of the
  named DAG, bound to that node. The child DAG must declare `node: required`.
  The step behaves as `parallel` does over child runs (Spec 018), except
  where this RFC says otherwise.
- **On a command step**, the step runs as the only step of a bound child per
  node, as if it were a `node: required` DAG started by `dag.run` with the
  same `nodes`. `timeout_sec` and `retry_policy` apply to each node, so one
  hung host does not use up the others' time. `preconditions`,
  `continue_on`, and `output` apply to the step.

### References in a selector

A reference in a selector must resolve to a valid label key or value
(`internal/ir/label.go`). Anything else fails the step before any node
starts. A label key or value holds no `,`, `!`, `=`, `*`, or `?`, so a
run-time value cannot add a term, negate one, or widen a match:

```yaml
nodes: web,env=${params.env}     # "prod" works; "prod,!canary" fails
```

An operator who needs fewer nodes narrows the run.

### Validation

`dagu validate` fails when:

- `nodes` is on a step other than those [The `nodes` field](#the-nodes-field)
  names, or on a step with `parallel`, `foreach`, `worker_selector`,
  `container`, or `repeat_policy`.
- `with.host` or `with.ip` is set on an `ssh.run` or `sftp.*` step with
  `nodes`, or `with.shell_packages` on any step with `nodes`.
- An `sftp.download` step with `nodes` has a `with.destination` that does not
  use `${node.name}`. Every node would write the same local path.
- A `node: required` DAG breaks a rule of [Bound runs](#bound-runs).
- A literal selector is invalid.
- The object form has an unknown field, a missing `labels`, or an
  out-of-range value.

Validation does not read the inventory, so it works offline and in CI. A
`dag.run` step with `nodes` whose child DAG does not declare `node: required`
fails when it starts, before any child starts.

### Matching

- The selector is resolved and matched when the step starts, after its
  dependencies and preconditions. The run's [node list](#narrowing-a-run),
  if any, is applied.
- SSH nodes and worker nodes match alike. Nodes are taken in name order, and
  the list is recorded in the step's status.
- A resolution error fails the step before any node starts.
- A step that matches no nodes fails with "selector matched no nodes".
- A matched node that is disabled (RFC 012) is not run. It is `disabled`,
  and does not fail the step. A step whose matched nodes are all disabled
  fails.
- A matched worker that is offline fails its node at once, rather than
  waiting or being left out.
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
- Otherwise the step's status follows `parallel` (Spec 018), including
  `partially_succeeded` and waiting for approval.
- `continue_on` applies to the step's result, as for any step.
- Aborting the step stops pending nodes and aborts running child runs.

### The `node` namespace

The Spec 003 namespace registry gains `node`, available in every
value-resolved field of a bound run:

| Reference | Value |
| --- | --- |
| `${node.name}` | Node name |
| `${node.host}`, `${node.port}`, `${node.user}` | Connection fields. A worker has only `host`, the host it reports |
| `${node.description}` | Description |
| `${node.labels}` | Labels, comma-separated, sorted, as `key` or `key=value` |
| `${node.labels.<key>}` | The value of the label with that key. Empty for a label with no value |
| `${node.labels."<key>"}` | The same, for a key such as `gpu.type`, as Spec 018 quotes item fields |

A reference to a label the node lacks fails that node's run, naming the node
and the key. The text would otherwise fail later and less clearly on the
node. Outside a bound run, a `${node.*}` reference is kept as text, with a
notice.

### Narrowing a run

A run can carry a list of node names. It applies to every `nodes` step in the
run and in its child runs. A node runs only when the step's selector matches
it and the list names it.

```text
step selects   web           → web01 web02 web03 web04
run narrowed   web01 web03
runs on                      → web01 web03
```

The same list binds a `node: required` DAG started directly, and must then
name exactly one node.

| Surface | Node list |
| --- | --- |
| CLI `dagu start`, `dagu enqueue` | `--node web01 --node web03`, repeatable as `--worker-label` is |
| API start, start-sync, and enqueue | `targetNodes` |
| MCP `dagu_execute` start and enqueue | `targetNodes` |
| UI run dialog | A node picker, for a `node: required` DAG only |

- Admission rejects a name that no inventory node has, so a mistyped `web1`
  never starts a run. It also rejects a list on a DAG that is not
  `node: required` and has no `nodes` step and no `dag.run` step.
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

- The step records each child run when it starts. Retrying the step, through
  `dagu retry` or the UI, retries each child that did not succeed, as
  `dagu retry` retries a run, and starts the nodes that never started.
  Children that succeeded keep their results.
- The retry uses the node list recorded by the first attempt. A node added to
  the inventory since then is not added. A node removed since then is not
  run. It is `removed`, and does not fail the step. Hosts often leave the
  inventory because they failed, so the retry still finishes the others.
- Connection settings are read again from the current inventory, so a rotated
  key works on retry.
- A parent run killed during a node step reads the recorded children on
  retry, so a node that already succeeded does not run again.

The UI shows **Retry failed nodes**, with a count, on a node step and on a
run that has one.

### Outputs

The step writes `parallel`'s aggregate (Spec 018), with these changes:

- `results` is in node name order, and each entry adds `node`.
- Nodes that did not run have an entry with status `not_started`,
  `removed`, or `disabled`. `removed` and `disabled` count in
  `summary.total` only.
- The whole aggregate is limited by `max_output_size`. Each child run keeps
  its full output.

### Run view

```text
┌ deploy ── run 2026-11-12 09:14 ── narrowed: web01 web02 web03 ────┐
│ Graph  Nodes  Logs                                                │
│                                                                   │
│ Nodes          deploy                 clean                       │
│ web01          ✓ 4/4                  ✓                           │
│ web02          ✗ restart (3/4)        ·      ▸ child run          │
│ web03          · not started          ·                           │
│                                                                   │
│ [Retry failed nodes (2)]   [Show failed only]                     │
└───────────────────────────────────────────────────────────────────┘
```

- A node step in the graph shows a count badge, such as `12 ✓ 1 ✗ 1 ⋯`.
- The **Nodes** tab shows nodes by node steps, failed nodes first, one page
  at a time, and updates live. Each cell shows the child's progress and
  opens its run.
- The header shows the node list when the run is narrowed, and the node when
  the run is bound.
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
| `sequence.strategy: step-first` | `nodes` on each command step |
| `sequence.strategy: node-first` | A `node: required` DAG started by `dag.run` with `nodes` |
| `${node.name}`, `${node.hostname}`, `${node.username}`, `${node.<attr>}` | `${node.name}`, `${node.host}`, `${node.user}`, `${node.labels.<attr>}` |
| Any other dispatch setting or strategy | Not supported. The import tool reports it |

| Ansible | Dagu |
| --- | --- |
| A play's `hosts:` naming groups | A `node: required` DAG started by `dag.run` with those groups as labels |
| `serial: 1` | `max_concurrent: 1` |
| `forks` | `nodes.max_concurrent` |
| `any_errors_fatal: true` | `keep_going: false` |
| `--limit` | `--node` |
| `run_once`, `delegate_to: localhost` | A step outside the bound DAG |

## Security

- A step can match only its project's inventory and the server's workers.
- A reference in a selector resolves to one label key or value, so a run-time
  value cannot add terms, negate them, or widen a match.
- A node list can only remove nodes. It is recorded and audited.
- A `node: required` DAG never runs unbound, so its commands never run on the
  Dagu host by mistake.
- Credentials resolve as RFC 012 defines, are kept in memory, and are masked
  in every log and status. Values never enter run status.
- Host keys are checked as RFC 012 defines, on a worker too. A run never
  trusts a new key.
- Node fields and label values follow RFC 012's character rules and are safe
  to insert unquoted. The docs still quote them in shell commands:
  `"${node.labels.role}"`.

## Failure behavior

| Event | Behavior |
| --- | --- |
| The inventory cannot be read, or the selector is invalid | The step fails before any node starts, with the error |
| A reference in a selector is not a valid label key or value | The step fails before any node starts |
| The child DAG of a `dag.run` step with `nodes` is not `node: required` | The step fails before any child starts |
| A `node: required` DAG is started without exactly one node | The run is rejected at admission |
| SSH connection or authentication fails | That node fails, with the host, port, error, and the hop that failed: the bastion or the node |
| The host key is unknown or has changed | That node fails, with the fingerprint. The Nodes page can trust a new key |
| A node's credential ref does not resolve | That node fails, naming the field and ref. Other nodes follow `keep_going` |
| A matched worker is offline, or disconnects during its run | That node fails, as a worker loss fails a run today |
| A node is gone from the inventory at retry | That node is `removed` and not run |
| The parent run is killed during a node step | The run fails. A retry reads the recorded children and reruns only nodes that did not succeed |
| `${node.labels.<key>}` names a label the node lacks | That node fails, with the node name and the label key |
| A node list names a node not in the inventory, or is given for a DAG with no node steps | The run is rejected at admission |
| A node list emptied every node step that matched nodes | The run fails with "no listed node was selected" |

## Compatibility and rollout

- `nodes` is a new step field, `node: required` a new DAG field, and `node`
  a new namespace.
- `${node.*}` was ordinary text before. Inside bound runs it now resolves.
  Elsewhere it is still kept, with a notice.
- No existing step changes behavior.

Rollout:

1. **SSH nodes**: bound runs, `node: required`, `nodes` on `dag.run` and on
   command steps, matching, concurrency, failure policy, the `node`
   namespace, the aggregate, the Nodes tab, and the editor's match count. A
   new spec, with conformance tests against an SSH server the test harness
   starts.
2. **Workers**: worker nodes, child runs dispatched to a named worker, and
   node steps in runs on workers, with RFC 012's `ResolveNodes`.
3. **Retry failed nodes.**
4. **Narrowing and binding** with the node list on the CLI, API, MCP, and
   run dialog.
5. **Docs**: "Coming from Rundeck" and "Coming from Ansible", with the
   mappings above.

## Verification and acceptance criteria

The sections above state the rules, and the new spec tests each one. This RFC
is accepted when the following hold.

1. A `dag.run` step with `nodes` and `max_concurrent: 1` over three SSH nodes
   runs every step of its child on one node before the next node starts.
2. A command step with `nodes` over SSH nodes and worker nodes runs once on
   each, and its aggregate lists them in name order.
3. `max_concurrent` and `keep_going` start and stop nodes as
   [Concurrency and failure](#concurrency-and-failure) states.
4. Every case in [Failure behavior](#failure-behavior) ends as stated.
5. `dagu retry` reruns only the nodes that did not succeed, including after
   the parent run is killed mid-step.
6. A node list limits every node step and child run, a mistyped name is
   rejected before the run starts, and a `node: required` DAG never starts
   unbound.
7. A secret used on a node never appears in logs, status, output, or a
   process argument, on the server or a worker.
8. A step over 1,000 nodes keeps its run status small and its aggregate
   within `max_output_size`.
9. A DAG without `nodes` behaves as before.

## Alternatives considered

- **Keep `parallel` over a host parameter.** Works today, but every DAG
  repeats the host list and the child DAG, and the run shows child runs, not
  hosts.
- **Node items inside the step instead of child runs.** Lighter for one
  command on many hosts, but a second mechanism beside child runs, with its
  own records, logs, and retry, and no way to run a whole workflow on a node
  or on a worker.
- **Batches**, as Ansible's `serial: N`. A sliding window with
  `max_concurrent` finishes sooner and needs one field fewer.
- **Narrowing with a selector.** An operator would learn which terms widen
  and which narrow. A list of names says exactly which hosts run.

## Consequences

- One mechanism, a child run bound to a node, covers SSH hosts and workers,
  single commands and whole workflows.
- Rolling deploys and runs across sites that the server cannot reach, through
  their workers, need no agent beyond Dagu.
- Rundeck and Ansible jobs move with the mappings above.
- Operators can narrow a run to a few hosts without edit rights.
- Retrying a deploy costs only the hosts that failed.

## Future enhancements

These are left out to keep the first version small. Each fits on top of the
design above without changing it.

- **A command step in a bound run that runs on the Dagu host**, without moving
  it to another action or a child DAG.
- **A node checklist in the run dialog**, listing the nodes the DAG's steps
  match, to fill the node list.
- **Succeeding on no match**, for a step whose nodes may not exist yet.
- **Ordering by a label** instead of by name.
- **Rerunning every node on retry**, not only those that did not succeed.
- **Percentages** for `max_concurrent` and a failure threshold, for fleets of
  changing size.
- **Answering `sudo` prompts** from a secret.
- **Letting an author forbid narrowing.**
- **`nodes` on `dag.enqueue`.**
- **`nodes` steps inside a bound run.**
- **A local fan-out** that runs a step once per matched node on the Dagu
  host, with `node` available.
