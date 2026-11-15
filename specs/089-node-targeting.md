# Spec: Node Targeting

## Status

Partially implemented. The `nodes` step field, the `node` DAG field, their
validation, and running a step as one child run per node are implemented.
Conformance covers validation through `dagu validate` and runs through
`dagu start`. A child bound to a node does not reach the node yet: a step in
it that runs commands fails before any step starts, so the commands never
run on the Dagu host. Steps with nodes do not run on workers yet. Further
sections are added as RFC 013 lands.

## Scope

This spec defines how a workflow names the inventory nodes ([RFC 012](../rfcs/012-node-inventory.md))
a step runs on, which steps and DAGs may do so, and how such a step runs one
child run per node. It does not define the inventory, or how a child run
reaches its node.

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
  DAG with the parent DAG's settings and the parent run's params.
  `timeout_sec` and `retry_policy` apply
  to each node's run of the step. `depends`, `preconditions`, `continue_on`,
  and `output` apply to the step with `nodes`.

Nodes start in name order, at most `max_concurrent` at a time.
`defaults.max_concurrent` does not apply. With `keep_going: false`, no node
starts after a node fails: the nodes running finish, and the rest do not
start. With `keep_going: true`, every node runs. Either way the step fails
when a node failed. Aborting the step stops the nodes not started and aborts
the running children. Otherwise the step's status follows `parallel`
([Spec 018](018-parallel-and-foreach.md)).

The step writes `parallel`'s aggregate to its standard output, with one entry
per matched node, in name order. Each entry adds `node`. A node that did not
run has the status `not_started`, or `disabled`, and counts in
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
node shows as skipped.

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

A step with `nodes` fails before any node starts, with the error, when:

- a reference in its selector resolves to anything but one label key or
  value;
- its selector matches no nodes (`selector matched no nodes`), only disabled
  nodes, or more than 1,000;
- the inventory cannot be read;
- it is a `dag.run` step whose child DAG does not declare `node: required`.

A run fails before any step starts when:

- it is a `node: required` DAG started without a node;
- it is bound to a node, and a step of it runs commands: a step with no
  action, `ssh.run`, or `sftp.*`, including handlers and `foreach` bodies;
- it runs on a worker and has a step with `nodes`.

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
