# Coming From Rundeck

Map Rundeck nodes, node filters, and dispatch settings to Dagu. Nodes live in inventories (`dagu nodes`, `references/cli.md`), and a step reaches them with `nodes` (`references/steptypes.md`, `nodes: Run On Inventory Nodes`). Dagu connects to inventory nodes over SSH from the Dagu host.

## Nodes

Write the resource model's nodes as inventories: a JSON array that `dagu nodes import` reads.

| Rundeck node | Dagu node: an inventory (SSH) node, or a worker that sets `worker.id` |
| --- | --- |
| `nodename` | `name`: a letter or digit, then letters, digits, `-`, `_`, or `.`; unique across inventories |
| `hostname`, `host:port` | `host`, `port` (default `22`) |
| `username` | `user` |
| `description` | `description` |
| `tags` | One label per tag, such as `web` |
| Another attribute, `env: prod` | A label, `env=prod` |
| `ssh-key-storage-path` | `key`: `{"secret": "<ref>"}` names a Dagu registry secret; a path names a key file |
| `ssh-password-storage-path`, `ssh-key-passphrase-storage-path` | `password`, `passphrase`: `{"secret": "<ref>"}` only |
| Settings every node shares | `defaults`; `defaults.labels` are added to each node's labels |

- Label keys and values are lowercased. A key is a letter or digit, then letters, digits, `-`, `_`, or `.`; a value may also hold `/`. Rewrite a value that holds anything else, such as a space (`osName: Mac OS X` as `osname=mac-os-x`), or leave the attribute out.
- An inventory never holds a secret value: a `password` or `passphrase` given as text is an error.
- To refresh an inventory from its source, write the JSON again and run `dagu nodes import --replace`. A node that keeps its name keeps its trusted host key.

```json
[
  {
    "name": "production",
    "defaults": {
      "user": "deploy",
      "key": { "secret": "ssh/deploy-key" },
      "labels": ["env=prod"]
    },
    "nodes": [
      { "name": "web01", "host": "10.0.1.11", "labels": ["web", "osfamily=unix"] },
      { "name": "web02", "host": "10.0.1.12", "labels": ["web", "canary", "osfamily=unix"] },
      { "name": "db01", "host": "db01.internal", "port": 2222, "labels": ["db"] }
    ]
  }
]
```

```sh
dagu nodes import inventories.json --dry-run   # check, store nothing
dagu nodes import inventories.json
dagu nodes test --labels web                   # connect; asks before it trusts a host key
dagu nodes list --labels 'web,!canary'
```

## Node Filters

A selector is the label filter that `dagu history --labels` takes: terms separated by commas, all of which must match.

| Rundeck node filter | Dagu selector |
| --- | --- |
| Tags, `tags: a+b` | `nodes: a,b` |
| An attribute, `env: prod` | `env=prod` |
| An excluded tag or attribute, `!tags: dev`, `!env: dev` | `'!dev'`, `'!env=dev'` |
| Names, a regular expression, OR (`tags: a,b`), or every node | No selector term. Add a label to the nodes the filter matched, in the inventory |

- A comma means AND in a Dagu selector and OR in a Rundeck tag filter.
- Quote a selector that starts with `!`, since YAML reads a leading `!` as a tag.
- Keys and values take globs, `*` and `?`, as in `region=us-*`, not regular expressions.
- To match every node of an inventory, label them all through `defaults.labels`.
- Selectors match labels, never node names. To run named nodes once, narrow the run with `--node` (see Running Jobs).

## Dispatch

| Rundeck job | Dagu |
| --- | --- |
| `nodefilters.dispatch.threadcount` | `nodes.max_concurrent`, 1 to 1000. The default is `1` |
| `nodefilters.dispatch.keepgoing` | `nodes.keep_going`. The default is `false`: no node starts after one fails, and running nodes finish |
| `sequence.strategy: node-first` | A `node: required` DAG started by `dag.run` with `nodes` |
| `sequence.strategy: step-first` | `nodes` on each command step |
| `rankAttribute`, `rankOrder` | Not supported. Nodes start in name order |
| `successOnEmptyNodeFilter` | Not supported. A step whose selector matches no nodes fails with `selector matched no nodes` |
| Any other dispatch setting, strategy, or orchestrator | Not supported |

Node-first: every step runs on one node before the next node starts.

```yaml
params:
  - version: ""
steps:
  - id: deploy
    action: dag.run
    with:
      dag: deploy-one
      params:
        version: ${params.version}  # a child reads only the params passed to it
    nodes:
      labels: web,env=prod
      max_concurrent: 1             # threadcount
      keep_going: false             # keepgoing
---
name: deploy-one
node: required                      # runs only bound to one node
params:
  - version: ""
steps:
  - id: install
    # ${params.version} is inserted as shell text, unescaped: pass only trusted values
    run: sudo apt-get install -y "app=${params.version}"
  - id: restart
    depends: [install]
    run: sudo systemctl restart app
```

Step-first: each step runs on all its nodes before the next step starts.

```yaml
steps:
  - id: stop
    run: sudo systemctl stop app
    nodes:
      labels: web,env=prod
      max_concurrent: 5
  - id: start
    depends: [stop]
    run: sudo systemctl start app
    nodes:
      labels: web,env=prod
      max_concurrent: 5
```

- A step with `nodes` fails when any of its nodes failed. `continue_on` decides whether the steps after it run.
- In a run bound to an SSH node, steps with no action, `ssh.run`, and `sftp.*` run on the node. Other actions, such as `http.request`, run where the run runs: the Dagu host, or the worker that runs its parent. In a run bound to a worker node, every step runs on that worker.
- A disabled node (`"disabled": true`) is not run and does not fail the step. A step whose matched nodes are all disabled fails.

## Node References

| Rundeck | Dagu, in a bound run |
| --- | --- |
| `${node.name}` | `${node.name}` |
| `${node.hostname}` | `${node.host}`, with the port in `${node.port}` |
| `${node.username}` | `${node.user}` |
| `${node.description}` | `${node.description}` |
| `${node.<attr>}` | `${node.labels.<attr>}`; quote a key with a dot: `${node.labels."gpu.type"}` |
| `${node.tags}` | `${node.labels}`: every label, sorted, comma-separated |
| `@node.<attr>@` in a script, `$RD_NODE_*` variables | The same `${node.*}` references, in `run:` commands and scripts |

- A reference to a label the node lacks fails the run before any step, naming the node and the key.
- Outside a bound run, `${node.*}` stays as text, with a notice.
- Quote references in shell commands: `"${node.labels.role}"`.

## Running Jobs

| Rundeck | Dagu |
| --- | --- |
| Job options, `${option.version}` | `params`, `${params.version}` |
| Key Storage | Registry secrets, named in an inventory as `{"secret": "<ref>"}` |
| Changing the target nodes when running a job | `dagu start --node web01 --node web03 deploy`; `targetNodes` in the API and MCP `dagu_execute` |
| Job reference step | `dag.run` |
| Execute locally | A step without `nodes`; it runs where the run runs |
| Per-node output of an execution | The run's Nodes tab; `GET /api/v1/dag-runs/{name}/{id}/node-runs` |
| Retry failed nodes | `dagu retry deploy --run-id <id>`, or **Retry failed nodes** on the step or **Retry** on the run in the UI: a step with `nodes` runs again only on the nodes that did not succeed, and the others keep their results |

- Dagu inserts `${params.*}` and step outputs into `run:` as shell text, without escaping, so a value such as `$(...)` runs as a command. Pass only trusted values, or limit a param with `pattern`, as in `- name: version`, `type: string`, `pattern: '^[0-9A-Za-z.+~-]+$'`, which refuses any other value before the run starts.
- A node list only removes nodes: a step runs on a node that its selector matches and the list names. A step none of whose nodes is listed is skipped without blocking later steps.
- A `node: required` DAG started directly takes exactly one node: `--node`, or the Node field of the run dialog. The run dialog has no node list for other DAGs.
- Each name must be a node, inventory or worker. Queued runs, retries, `dagu restart`, and `dagu start --from-run-id` keep the list.

## Workers

- A run on a worker reaches SSH nodes as a run on the Dagu host does. The worker reads nodes, secret refs, and trusted host keys from the coordinator.
- A worker that sets `worker.id` is a node of kind `worker`, with the worker's labels; `dagu nodes list` shows it. A step with `nodes` matches it as it matches an SSH node, and its child runs on that worker, every step there. `${node.host}` is the host the worker reports, and `ssh.run` or `sftp.*` without `with.host` or `with.ip` fails, since the worker has no SSH connection.
- A matched worker that is offline fails its node at once, and a worker lost during the run fails its node.

## Not Available Yet

- Starting a `node: required` DAG directly bound to a worker node, with `--node` or `targetNodes`, is refused. Such a DAG runs on a worker node only as the child of a step with `nodes`.
