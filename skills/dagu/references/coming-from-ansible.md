# Coming From Ansible

Map Ansible inventories, plays, and run limits to Dagu. Hosts live in inventories (`dagu nodes`, `references/cli.md`), and a step reaches them with `nodes` (`references/steptypes.md`, `nodes: Run On Inventory Nodes`). Dagu runs commands on nodes over SSH from the Dagu host. It has no modules or facts.

## Inventory

Write the Ansible inventory as Dagu inventories: a JSON array that `dagu nodes import` reads.

| Ansible | Dagu inventory |
| --- | --- |
| Host name (`inventory_hostname`) | `name`: a letter or digit, then letters, digits, `-`, `_`, or `.`; unique across inventories |
| `ansible_host`, `ansible_port`, `ansible_user` | `host`, `port` (default `22`), `user` |
| `ansible_ssh_private_key_file` | `key`: a key file path, or `{"secret": "<ref>"}` for a Dagu registry secret |
| `ansible_password`, a Vault value | `password: {"secret": "<ref>"}`; an inventory never holds a secret value |
| `ProxyJump` in `ansible_ssh_common_args` | `bastion`: another node of the same inventory |
| A group, and each parent group of a `:children` group | One label per group, such as `web` and `prod` |
| `all` | A label every node carries, through `defaults.labels` |
| A host or group variable with a short value | A label, `role=frontend`, read as `${node.labels.role}` |
| Variables every host of the inventory shares | `defaults`, for connection fields and labels |
| `host_key_checking = False` | `strict_host_key: false` on the node or in `defaults`: accepts any key |
| `known_hosts` | Trusted host keys: `dagu nodes test` asks before it trusts one. Or pin `host_key` |
| `ansible_connection` other than `ssh` | Not supported. Inventory nodes are SSH hosts |

- Labels do not nest: give a host a label for every group it belongs to, directly or through a parent.
- Label keys and values are lowercased. A key is a letter or digit, then letters, digits, `-`, `_`, or `.`; a value may also hold `/`. Keep other variables in the commands or params.
- A group's variables apply to its hosts only, so set them on each node, or keep the group in its own inventory with them as `defaults`.
- To refresh an inventory from a dynamic source, write the JSON again and run `dagu nodes import --replace`. A node that keeps its name keeps its trusted host key.

```ini
[web]
web01 ansible_host=10.0.1.11 role=frontend
web02 ansible_host=10.0.1.12 role=frontend

[db]
db01 ansible_host=10.0.2.11 ansible_port=2222 role=primary

[prod:children]
web
db

[prod:vars]
ansible_user=deploy
```

```json
[
  {
    "name": "prod",
    "defaults": {
      "user": "deploy",
      "key": { "secret": "ssh/deploy-key" },
      "labels": ["prod"]
    },
    "nodes": [
      { "name": "web01", "host": "10.0.1.11", "labels": ["web", "role=frontend"] },
      { "name": "web02", "host": "10.0.1.12", "labels": ["web", "role=frontend"] },
      { "name": "db01", "host": "10.0.2.11", "port": 2222, "labels": ["db", "role=primary"] }
    ]
  }
]
```

```sh
dagu nodes import inventories.json --dry-run   # check, store nothing
dagu nodes import inventories.json
dagu nodes test --labels prod                  # like `ansible prod -m ping`; asks before it trusts a host key
dagu nodes list --labels web                   # like `ansible-inventory --list`
```

## Plays

| Ansible | Dagu |
| --- | --- |
| A play's `hosts:` naming groups | A `node: required` DAG started by `dag.run` with those groups as labels |
| `hosts: web:&prod` | `nodes: web,prod` |
| `hosts: web:!canary` | `nodes: web,!canary` |
| `hosts: web:db`, a regular expression, or host names | No selector term. Add a label to the hosts it should match |
| `serial: 1` | `max_concurrent: 1`: every task on one node before the next node starts |
| `serial: N` | `max_concurrent: N`, a sliding window: a node starts when another finishes, not in batches |
| `forks` | `nodes.max_concurrent`, 1 to 1000. The default is `1` |
| `any_errors_fatal: true` | `keep_going: false`, the default: no node starts after one fails, and running nodes finish |
| A play that goes on with the other hosts after one fails | `keep_going: true`: every node runs, and the step fails if any node failed |
| A play on `localhost`, or `run_once` with `delegate_to: localhost` in a linear play without `serial` | A step outside the bound DAG |
| `delegate_to: localhost` on each host, such as a load balancer call, or `run_once` with it under `serial: 1` | A step with an action such as `http.request` in the bound DAG: for an SSH node it runs where the run runs, not on the node, once per node; for a worker node it runs on that worker |
| `serial` as a percentage or a list, `max_fail_percentage`, `order` | Not supported. Nodes start in name order |
| `strategy: free` | A `node: required` DAG started by `dag.run` with `nodes`: each node runs its steps without waiting for the others, `max_concurrent` nodes at a time |
| `run_once` under `strategy: free`, or under `serial` above 1, which runs once per batch | Not supported |

- A comma means AND in a Dagu selector. Quote a selector that starts with `!`, since YAML reads a leading `!` as a tag.
- Keys and values take globs, `*` and `?`, as in `role=front*`, not regular expressions.
- In a run bound to an SSH node, steps with no action, `ssh.run`, and `sftp.*` run on the node. Other actions run where the run runs: the Dagu host, or the worker that runs its parent. In a run bound to a worker node, every step runs on that worker.
- In a linear play without `serial`, a `run_once` task delegated to localhost runs after the tasks before it finish on every host, and before the tasks after it start. A step outside one bound DAG runs before or after all of that DAG's steps, so split the play at the task: a step with `nodes`, a command step or a `dag.run` of a bound DAG, for the tasks before it, the `run_once` task as a step without `nodes`, then a step with `nodes` for the tasks after it, each depending on the one before. A `run_once` task without `delegate_to` runs on one host instead: select that host with a label only it carries. A run narrowed with `--node` to other hosts skips that step, and the steps after it still run ([Limits](#limits)), where Ansible runs `run_once` on a host that `--limit` selects.
- The linear strategy runs each task on every host before the next task. For that order, put `nodes` on each command step instead. A step with `nodes` fails when any of its nodes failed, and the steps after it then run on every matched node, or not at all, as `continue_on` decides.

```yaml
# site.yaml: hosts: web:&prod, serial: 1, any_errors_fatal: true
params:
  - version: ""
steps:
  - id: announce                # a play on localhost that runs first
    action: http.request
    with:
      method: POST
      url: https://chat.internal/hooks/deploy
      body: "deploying ${params.version}"
  - id: webservers
    depends: [announce]
    action: dag.run
    with:
      dag: webservers
      params:
        version: ${params.version}  # a child reads only the params passed to it
    nodes:
      labels: web,prod
      max_concurrent: 1
      keep_going: false
---
name: webservers
node: required                  # runs only bound to one node
params:
  - version: ""
steps:
  - id: drain                   # delegate_to: localhost, once per node
    action: http.request
    with:
      method: POST
      url: https://lb.internal/pools/web/${node.name}/drain
  - id: install                 # runs on the node
    depends: [drain]
    # ${params.version} is inserted as shell text, unescaped: pass only trusted values
    run: sudo apt-get install -y "app=${params.version}"
  - id: enable
    depends: [install]
    action: http.request
    with:
      method: POST
      url: https://lb.internal/pools/web/${node.name}/enable
```

## Tasks And Variables

| Ansible | Dagu, in a bound run |
| --- | --- |
| `command`, `shell` | `run:` |
| `copy` | `sftp.upload` |
| `fetch` | `sftp.download`. Put `${node.name}` in `destination` so nodes do not write one local path; a step with `nodes` must |
| `template` | `template.render` writes the file on the Dagu host, then `sftp.upload` copies it. Give `with.output` a path no other run shares, as below |
| `become: true` | `sudo` in the command. Dagu does not answer a `sudo` password prompt |
| Other modules, facts, and handlers | Not supported. Run the commands they would run |
| `{{ inventory_hostname }}` | `${node.name}` |
| `{{ ansible_host }}`, `{{ ansible_port }}`, `{{ ansible_user }}` | `${node.host}`, `${node.port}`, `${node.user}` |
| `{{ role }}`, a host variable kept as a label | `${node.labels.role}`; quote a key with a dot: `${node.labels."gpu.type"}` |
| `-e version=1.2` | `dagu start site -- version=1.2`, read as `${params.version}` |

- A reference to a label the node lacks fails the run before any step, naming the node and the key.
- Outside a bound run, `${node.*}` stays as text, with a notice.
- Quote references in shell commands: `"${node.labels.role}"`.
- A relative path, such as `with.output` of `template.render`, resolves in the run's work directory, which each bound run has to itself. A bound DAG that sets `working_dir` shares that directory with every node and run, so put `${context.run.id}` in the path, as in `rendered/${context.run.id}/app.conf`.
- Dagu inserts `${params.*}` and step outputs into `run:` as shell text, without escaping, so a value such as `$(...)` runs as a command. Pass only trusted values, or limit a param with `pattern`, as in `- name: version`, `type: string`, `pattern: '^[0-9A-Za-z.+~-]+$'`, which refuses any other value before the run starts.

## Limits

| Ansible | Dagu |
| --- | --- |
| `--limit web01,web03` | `dagu start --node web01 --node web03 site`; `targetNodes` in the API and MCP `dagu_execute` |
| `--limit @site.retry` | `dagu retry site --run-id <id>`, or Retry failed nodes in the UI: a step with `nodes` runs again only on the nodes that did not succeed, and the others keep their results |

- `--node` takes node names, not groups or patterns. Each must be a node, inventory or worker.
- A node list only removes nodes: a step runs on a node that its selector matches and the list names. A step none of whose nodes is listed is skipped without blocking later steps.
- A `node: required` DAG started directly takes exactly one node: `--node`, or the Node field of the run dialog. The run dialog has no node list for other DAGs.
- Queued runs, retries, `dagu restart`, and `dagu start --from-run-id` keep the list.

## Workers

- A run on a worker reaches SSH nodes as a run on the Dagu host does. The worker reads nodes, secret refs, and trusted host keys from the coordinator.
- A worker that sets `worker.id` is a node of kind `worker`, with the worker's labels; `dagu nodes list` shows it. A step with `nodes` matches it as it matches an SSH node, and its child runs on that worker, every step there. `${node.host}` is the host the worker reports, and `ssh.run` or `sftp.*` without `with.host` or `with.ip` fails, since the worker has no SSH connection.
- A matched worker that is offline fails its node at once, and a worker lost during the run fails its node.
- A `node: required` DAG started with `--node` or `targetNodes` naming a worker node runs on that worker, from `dagu start`, `dagu enqueue`, `dagu retry`, `dagu restart`, and the API, whatever the default execution mode. It needs a coordinator, and a worker that is offline when the run is dispatched fails it at once.
