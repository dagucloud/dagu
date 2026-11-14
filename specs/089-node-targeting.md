# Spec: Node Targeting

## Status

Partially implemented. The `nodes` step field, the `node` DAG field, and
their validation are implemented, and conformance covers them through
`dagu validate`. Running a step on its nodes is not implemented yet: a run
that uses `nodes` or `node: required` fails before any step starts, naming
the step or the DAG. Further sections are added as running lands (RFC 013).

## Scope

This spec defines how a workflow names the inventory nodes ([RFC 012](../rfcs/012-node-inventory.md))
a step runs on, and which steps and DAGs may do so. It does not define the
inventory, how nodes are matched at run time, or how a step runs on a node.

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
`sftp.download`.

### The `node` DAG field

`node: required` makes a DAG run only bound to one node: as the child of a
`dag.run` step with `nodes`, or started with exactly one node. `required` is
the only value.

## Errors

`dagu validate` fails, naming the field, when:

- `nodes` is on any other step, or on a step that also sets `parallel`,
  `foreach`, `worker_selector`, `container`, or `repeat_policy`;
- `with.host` or `with.ip` is set on an `ssh.run` or `sftp.*` step with
  `nodes`, since each node gives the host;
- `with.shell_packages` is set on a step with `nodes`;
- an `sftp.download` step with `nodes` has a `with.destination` that does not
  use `${node.name}`, since every node would write the same local path;
- a selector without a reference has an invalid term or no terms;
- the object form has an unknown field, no `labels`, a `max_concurrent`
  outside 1 to 1000, or a `keep_going` that is not a boolean;
- `node` has a value other than `required`;
- a `node: required` DAG sets `ssh`, `container`, or `worker_selector`, or has
  a step with `nodes`.

## Examples

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
