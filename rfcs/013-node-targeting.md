# RFC 013: Node Targeting

- **Status:** Proposed
- **Date:** 2026-11-12
- **Affected areas:** `spec`, `ir`, `runtime` (node fan-out, retry, bound
  runs), `runtime/builtin/ssh`, `runtime/builtin/dag`, `service/coordinator`,
  `service/frontend` (API, UI), `service/mcp`, `cmd`, `api/v1`, `proto`,
  `ui`, `specs`, and `docs`

## Summary

Let a step run on every node a filter selects from the inventory of
[RFC 012](012-node-inventory.md):

```yaml
steps:
  - id: uptime
    run: uptime
    nodes: "tags: web+prod"
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
      filter: "tags: web+${params.env}"
      max_concurrent: 2

---
name: deploy-one
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
6. Nothing changes for DAGs that do not use `nodes`.

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
nodes: "tags: web !name: web03"
```

The object form adds dispatch settings:

```yaml
nodes:
  filter: "tags: web+${params.env}"
  max_concurrent: 5
  keep_going: true
  rank_by: rack
  rank_order: desc
  allow_empty: false
  retry: failed
```

| Field | Default | Meaning |
| --- | --- | --- |
| `filter` | Required | An RFC 012 filter. Value-resolved, so it can use `${params.*}`, `${consts.*}`, and step outputs |
| `max_concurrent` | `1` | Nodes running at once, `1` through `1000` |
| `keep_going` | `false` | Whether other nodes start after one fails |
| `rank_by` | `name` | Attribute that orders the nodes |
| `rank_order` | `asc` | `asc` or `desc` |
| `allow_empty` | `false` | Whether matching no nodes succeeds |
| `retry` | `failed` | `failed` reruns failed nodes on retry, `all` reruns every node |

`nodes: local` is a reserved string. It is described under
[Node-first runs](#node-first-runs).

The default `max_concurrent` is `1`, as in Rundeck. A command that restarts a
service should not reach every host at once unless the author says so.
`defaults.max_concurrent` from Spec 018 does not apply to `nodes`.

### Where a node item runs

| Step | On an `ssh` node | On a `worker` node |
| --- | --- | --- |
| `run`, `command`, or `script` with no `action` | Over SSH | As a one-step child run on that worker |
| `ssh.run` | Over SSH | Fails: the node has no SSH connection |
| `sftp.upload`, `sftp.download` | Over SFTP | Fails: the node has no SSH connection |
| `dag.run` | A child run [bound](#node-first-runs) to the node | A child run bound to the node, dispatched to that worker |

For SSH:

- The node supplies the connection. Connection fields under the step's
  `with`, such as `user`, `key`, or `shell`, override the node's, field by
  field. The DAG-level `ssh` field is not used.
- `shell` comes from the step, then the node's `ssh.shell`, then the
  executor's default.
- Only a `working_dir` set on the step itself applies on the node. An
  inherited `working_dir` names a directory on the Dagu host and is ignored.
- Step `env` is not sent to the node, as for `ssh.run` today. Values reach the
  node through `${...}` references, which resolve before the command is sent.

### Validation

`dagu validate` fails when:

- `nodes` is on a step other than those in the table above.
- `nodes` is on a step with `parallel`, `foreach`, `worker_selector`,
  `container`, or `repeat_policy`.
- `with.host` or `with.ip` is set on an `ssh.run` or `sftp.*` step with
  `nodes`.
- A literal filter does not parse, or contains an invalid regular expression.
- The object form has an unknown field, a missing `filter`, or an
  out-of-range value.
- `local` is used in the object form.

Validation does not read the inventory, so it works offline and in CI.

### Matching

- The filter is resolved and matched when the step starts, after its
  dependencies and preconditions.
- The run's [target filter](#narrowing-a-run), if any, is applied.
- Nodes are ordered by `rank_by`, then by name. Two values compare as numbers
  when both are numbers, and as strings otherwise. Nodes without the attribute
  come last.
- The ordered list of node names is recorded in the step's status. Retries
  use it.
- A resolution warning from RFC 012, such as a stale source, is written to
  the step log.
- A resolution error fails the step before any node starts.
- A step that matches no nodes fails with "filter matched no nodes", unless
  `allow_empty` is set, in which case it succeeds.
- A step that matches nodes, but none after the run's target filter, is
  skipped. Narrowing a run to the web hosts should not fail its database step.
- A step can match at most 1,000 nodes, as Spec 018 limits items.

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
| `${node.tags}` | Tags, comma-separated, sorted |
| `${node.<attribute>}` | A custom attribute |
| `${node."<attribute>"}` | An attribute whose name has a `-`, as Spec 018 quotes item fields |

Rules:

- `node` is available in every value-resolved field of a step with `nodes`,
  including `with`, except `nodes.filter` itself.
- `node` is available in every field of a [bound run](#node-first-runs).
- Elsewhere a `${node.*}` reference is preserved with a passive notice.
- A reference to an attribute the node lacks fails that node's item with the
  node and attribute names. Sending the literal text to a remote shell would
  fail later and less clearly. Spec 018 fails a `foreach` item the same way.
- For processes that run on the Dagu host or a worker, Spec 017 gains
  `DAG_NODE_NAME` and `DAG_NODE_HOSTNAME`.

### Node-first runs

A `dag.run` step with `nodes` starts one child run per matched node. Each child
run is bound to its node.

- The child run ID is derived from the parent run ID, the step, the child DAG,
  and the node name, as `parallel` derives its child run IDs. A retry finds
  the same children, which is what RFC 011 Part 2 needs.
- `with.params` is resolved once per node, with `node` available.

A bound run:

- records its node, with its attributes and without secrets, in its status.
  The run header shows it.
- resolves `${node.*}` in every field.
- runs its `run`, `command`, and `script` steps, and its `ssh.run` and
  `sftp.*` steps that set no host, on the bound node.
- runs other actions, such as `http.request` or `docker.run`, where the run
  runs, as before.
- runs a step with `nodes: local` where the run runs.
- fans out a step with its own `nodes` filter as usual.
- passes its binding to its `dag.run` children, unless the call step sets
  `nodes`.

A run bound to a worker node is dispatched to that worker through the
existing `target_worker_id`, so its command steps run there directly.

`dagu start deploy-one --node web01` binds a run to one node, so the child DAG
can be tested alone. The API and MCP take `node` for the same purpose. The run
dialog offers a node picker for a DAG that reads `${node.*}` outside a step
with `nodes`.

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

A run can carry a target filter. It is joined with every `nodes` filter in the
run and in its child runs, as if each filter had the target filter's clauses
added. `nodes: local` is not affected.

- The run dialog lists the nodes that the DAG's literal filters match, all
  checked, as Rundeck does by default. Unchecking nodes sets a target filter
  of the form `name: web01,web04`. A text field takes any filter. A step whose
  filter uses references is shown as "matched when the step starts".
- `dagu start --nodes 'name: web01,web04'`, the API's `nodeFilter`, and
  `dagu_execute`'s `nodes` set it too.
- `dagu retry` and UI retries keep the target filter.
- The target filter is recorded in the run status, shown in the run header,
  and written to the audit entry of the start.

Joining filters can only remove nodes, so a user who may run a DAG cannot use
narrowing to reach a node the DAG would not reach.

### Retry

Retry builds on [RFC 011](011-hpc-batch-steps-and-sweeps.md) Part 2. A node
item is identified by its node name.

- Retrying a node step, through `dagu retry` or the UI, reruns its `failed`,
  `aborted`, and `not_started` nodes. Succeeded nodes keep their recorded
  results. `retry: all` reruns every node.
- The retry uses the node list recorded by the first attempt. A node added to
  the inventory since then is not added. A node removed since then fails with
  "node web03 is no longer in the inventory".
- Connection settings are read again from the current inventory, so a rotated
  key works on retry.
- For a `dag.run` step, the failed children resume as retries of their own
  runs, as RFC 011 specifies for `parallel`.

The UI shows **Retry failed nodes**, with a count, on a node step and on a
run that has one.

### Outputs

The step's `output` variable (Spec 012) holds JSON:

```json
{
  "summary": { "total": 3, "succeeded": 1, "failed": 1, "not_started": 1 },
  "nodes": [
    { "name": "web01", "status": "succeeded", "exit_code": 0, "output": "..." },
    { "name": "web02", "status": "failed", "exit_code": 1, "error": "..." },
    { "name": "web03", "status": "not_started" }
  ]
}
```

- `nodes` follows the matched order.
- `output` is the node's captured stdout, limited by `max_output_size` per
  node.
- Declared `outputs` appear per node as `nodes[].outputs`. For `dag.run`, each
  entry has the child's outputs and its `dagRunId`.
- The aggregate is written whether or not every node succeeded.
- `${steps.<id>.outputs.<name>}` on a node step is not defined. Steps read the
  aggregate.

### Logs

- Each node item has its own stdout and stderr.
- The step log shows **All nodes**, with each line prefixed by its node name
  in arrival order, and one tab per node. A toggle shows failed nodes only.
- The step log API and MCP's `step_log` target take `node`.
- `dagu status` lists each node step with its per-node counts.

### Run view

```text
┌ deploy ── run 2026-11-12 09:14 ── target: tags: web+prod ─────────┐
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
  the node steps. For node-first runs, the columns are the child DAG's steps,
  read from the child runs.
- Each cell opens that node's log for that step.

### Editor

- The DAG editor shows the match count next to each literal `nodes` filter,
  from `GET /nodes`. Hovering lists the first 20 nodes.
- Filter syntax errors are marked in place.
- A filter with references shows "matched when the step starts".

### Rundeck mapping

| Rundeck job | Dagu |
| --- | --- |
| `nodefilters.filter` | `nodes.filter` |
| `nodefilters.dispatch.threadcount` | `nodes.max_concurrent` |
| `nodefilters.dispatch.keepgoing` | `nodes.keep_going` |
| `nodefilters.dispatch.rankAttribute` | `nodes.rank_by` |
| `nodefilters.dispatch.rankOrder` `ascending` or `descending` | `nodes.rank_order` `asc` or `desc` |
| `excludePrecedence: true` | The default |
| `excludePrecedence: false` | Not supported. Reported by the import tool |
| `successOnEmptyNodeFilter` | `nodes.allow_empty` |
| `nodeFilterEditable` | Narrowing is always available |
| `sequence.strategy: step-first` | `nodes` on each node step |
| `sequence.strategy: node-first` | `dag.run` with `nodes`, over an inline child DAG holding the node steps |
| `jobref` with `nodeStep: true` | `dag.run` with `nodes` |
| `${node.name}`, `${node.hostname}`, `${node.username}` | `${node.name}`, `${node.hostname}`, `${node.user}` |

### Ansible mapping

| Ansible | Dagu |
| --- | --- |
| `hosts:` pattern | `nodes.filter` |
| `forks` | `nodes.max_concurrent` |
| `serial: N` | `dag.run` with `nodes` and `max_concurrent: N`. Dagu starts the next host as soon as one finishes, where Ansible waits for the whole batch |
| `any_errors_fatal: true` | `keep_going: false` |
| `--limit` | The run's target filter |
| `run_once`, `delegate_to: localhost` | A step without `nodes`, or `nodes: local` in a bound run |

## Security

- A step can match only nodes in its run's project inventory.
- The target filter can only remove nodes. It is recorded and audited.
- Credentials are fetched per node item through secret resolution, kept in
  memory, and masked in every log and status. They never enter run status.
- Host keys are checked as RFC 012 defines. A run never trusts a new key.
- Built-in attributes follow RFC 012's character rules and are safe to insert
  unquoted. Custom attributes can come from scripts and URLs, so authors quote
  them in shell commands: `"${node.role}"`. The docs show this in every
  example.
- On a worker node, the item runs as the worker's OS user, as worker runs do
  today.

## Failure behavior

| Event | Behavior |
| --- | --- |
| The inventory cannot be read | The step fails before any node starts, with RFC 012's error |
| The filter does not parse at run time | The step fails, with the position and the error |
| SSH connection or authentication fails | That node fails, with the host, port, and error |
| The host key is unknown or has changed | That node fails, with the fingerprint. The Nodes page can trust a new key |
| A node has an RFC 012 problem, such as no hostname | That node fails, with the problem |
| A worker node disconnects during its item | That node fails, as a worker loss fails a run today |
| A node is gone from the inventory at retry | That node fails, with "no longer in the inventory" |
| `${node.<attribute>}` names a missing attribute | That node fails, with the node and attribute names |

## Compatibility and rollout

- `nodes` is a new step field. The `node` namespace is new.
- `${node.*}` was ordinary text before. Inside node steps and bound runs it
  now resolves. Elsewhere it is still preserved, with a notice.
- No existing step changes behavior.

Rollout:

1. **Step-first over SSH**: `nodes` on `run`, `command`, `script`, `ssh.run`,
   and `sftp.*`, with matching, order, concurrency, failure policy, the `node`
   namespace, per-node logs, the aggregate output, the Nodes tab, and the
   editor's match count. A new spec, with conformance tests against an SSH
   server the test harness starts.
2. **Retry failed nodes**, after RFC 011 Part 2, and **narrowing** in the CLI,
   API, run dialog, and MCP.
3. **Node-first**: `dag.run` with `nodes`, bound runs, and `--node`.
4. **Worker nodes**, for command items and bound runs.
5. **Docs**: "Coming from Rundeck" and "Coming from Ansible", with the mapping
   tables above.

## Verification and acceptance criteria

1. A `run` step with `nodes: "tags: web"` over three SSH nodes runs on each,
   and its aggregate lists three nodes in name order.
2. `max_concurrent: 1` runs nodes one at a time in rank order, and
   `rank_order: desc` reverses it.
3. With `keep_going: false`, a failure on the second of four nodes leaves the
   third and fourth `not_started`, and the step fails.
4. With `keep_going: true`, all four run and the step fails.
5. A filter matching nothing fails the step, and `allow_empty: true` makes it
   succeed.
6. A target filter that removes every node of a step skips that step.
7. `dagu retry` reruns only failed and not-started nodes. A node added to the
   inventory between attempts is not added.
8. `--nodes 'name: web01'` limits every node step and child run to `web01`.
   A target filter naming a node that no step matches runs nothing extra.
9. A `dag.run` step with `nodes` starts one bound child per node. In the
   child, `${node.hostname}` resolves, a `run` step runs on the node, and a
   `nodes: local` step runs on the Dagu host.
10. `dagu start deploy-one --node web01` runs the child alone, bound to
    `web01`.
11. A `run` step on a worker node runs on that worker.
12. `timeout_sec` and `retry_policy` apply to each node separately.
13. A secret used as a node's key never appears in logs, status, or output.
14. Each validation rule in [Validation](#validation) has a failing example.

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
- Command steps in a bound child run on the node, which differs from the same
  DAG run alone. The run header shows the binding to make this visible.

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
