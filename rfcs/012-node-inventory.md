# RFC 012: Node Inventory

- **Status:** Proposed
- **Date:** 2026-11-12
- **Affected areas:** a new `inventory` package, `persis/file`, `secret`,
  `runtime/builtin/ssh`, `service/frontend` (API, UI), `service/coordinator`,
  `service/mcp`, `cmd`, `api/v1`, `proto`, `ui`, and `docs`

## Summary

Give each project a list of the hosts it runs commands on. A node has a name,
a hostname, tags, attributes, and SSH settings whose credentials come from the
secret store. Nodes are declared in a `nodes.yaml` file, read from Rundeck
resource files and Ansible inventories, produced by scripts or URLs, or
discovered from connected workers.

A filter selects nodes with Rundeck's filter syntax:

```text
tags: web+prod !name: web03
```

This RFC defines the inventory, the filter language, and the surfaces that
show them. [RFC 013](013-node-targeting.md) runs steps on the nodes a filter
selects.

```text
nodes.yaml ──┐
sources ─────┼──▶ inventory ──▶ filter ──▶ matched nodes ──▶ RFC 013 runs steps
workers ─────┘        │
                      └──▶ Nodes page, API, CLI, MCP
```

## Problem statement

Rundeck users keep their fleet in a resource model: every host, its tags, its
SSH user, and the Key Storage path of its key. Jobs select hosts with a node
filter, and nothing in a job names a host. Ansible users keep the same data in
an inventory.

Dagu has no such list:

| Gap | What happens today |
| --- | --- |
| No host list | Every `ssh.run` step names its own `host`. A DAG that runs on 40 hosts takes them as a parameter and fans out with `parallel` |
| No selection by role | Nothing maps "the production web servers" to hosts. Each DAG repeats the list or calls a script that builds it |
| Keys live on disk | `ssh.run` reads its key from a file path (`internal/runtime/builtin/ssh/client.go`). Rundeck keeps keys in Key Storage and never writes them to the job host |
| Host keys per run | Strict host key checking reads `~/.ssh/known_hosts`. A fleet of new hosts fails until someone edits that file on the server |
| Nothing to import into | A Rundeck `resources.yaml` or an Ansible inventory has no place to go |

## Goals

1. A project lists its nodes in one place, with tags and attributes, and the
   list can come from files, scripts, URLs, or workers.
2. Rundeck resource files and Ansible inventories load without conversion.
3. Rundeck node filters select the same nodes in Dagu, unchanged.
4. SSH credentials come from the secret store and never reach logs, run
   status, API responses, or disk outside the store.
5. A user can see which nodes a filter matches, and check that Dagu can reach
   a node, before any DAG runs on it.
6. Nothing changes for installs that have no `nodes.yaml`.

## Non-goals

- Running anything on a node. [RFC 013](013-node-targeting.md) does that.
- WinRM, AWS SSM, or other transports. The model leaves room for them. See
  [Open questions](#open-questions).
- Built-in cloud sources such as EC2 or Kubernetes. A `command` source that
  calls the vendor CLI covers them.
- Configuration management, package state, or facts gathering.
- Limiting which nodes a user may target. A later access RFC can add it on
  top of the filter language.

## Naming

"Node" already has two meanings in Dagu:

| Meaning | Where | Change |
| --- | --- | --- |
| Another Dagu server, reached through its API | `remote_nodes` config, `/remote-nodes` API, "Remote Nodes" page | The UI and docs call these **remote servers**. The config key, API paths, and the `remoteNode` URL parameter stay |
| A step's execution state | `runtime` code, such as `NodeStatus` | Not shown to users. Inventory code lives in a package named `inventory` to keep the two apart |

"Node" is kept for inventory entries because Rundeck users already use the
word, and an import maps one to one.

## Design

### Where the inventory lives

The inventory belongs to a project, as defined in
[RFC 003](003-remove-workspaces.md). Until RFC 003 ships, the installation has
one inventory, at the default project's location.

| Project | Inventory file | Snapshots and trusted host keys |
| --- | --- | --- |
| Default project | `nodes.yaml` beside the global `base.yaml` | `<data dir>/nodes/` |
| Project, `paths.projects_dags_dir` unset | `projects/<name>/nodes.yaml` | `projects/<name>/data/nodes/` |
| Project, `paths.projects_dags_dir` set | `<projects_dags_dir>/<name>/nodes.yaml` | `projects/<name>/data/nodes/` |

With `paths.projects_dags_dir` set, `nodes.yaml` sits beside the project's
`base.yaml`, so a git checkout holds the inventory with the DAGs. Like
`base.yaml`, `nodes.yaml` at the root of a project's DAG directory is not a
DAG.

A project without `nodes.yaml` has no nodes.

### `nodes.yaml`

```yaml
defaults:
  user: deploy
  ssh:
    key: { secret: ssh/deploy }

nodes:
  web01:
    hostname: 10.0.1.11
    tags: [web, prod]
    region: us-east-1
  web02:
    hostname: 10.0.1.12
    tags: [web, prod]
    region: us-east-1
  db01:
    hostname: db01.internal:2222
    user: postgres
    tags: [db, prod]
    ssh:
      key: { secret: ssh/db }
      bastion: jump01
  jump01:
    hostname: jump.example.com
    tags: [bastion]

sources:
  - name: legacy
    file: rundeck/resources.yaml
    format: rundeck
  - name: ec2
    command: [./inventory/ec2-nodes.sh, --region, us-east-1]
    refresh: 10m
  - name: ansible
    command: [ansible-inventory, -i, /etc/ansible/hosts, --list]
    format: ansible
    refresh: 1h

workers: true
```

| Root field | Meaning |
| --- | --- |
| `defaults` | Node fields applied to every node that does not set them |
| `nodes` | Nodes declared in this file, keyed by name |
| `sources` | Other places nodes come from, in order |
| `workers` | When `true`, connected workers are nodes. Default `false` |

Unknown root fields are a validation error.

### Nodes

| Field | Meaning |
| --- | --- |
| `hostname` | Host name or IP address. `host:port` also sets `port` |
| `port` | SSH port. Default `22` |
| `user` | SSH user |
| `description` | Text shown in the UI |
| `tags` | A list, or a comma-separated string |
| `ssh` | Connection settings. See below. These are not attributes |
| Any other key | A custom attribute. The value must be a scalar and is kept as a string |

Dagu adds three read-only attributes to every node:

| Attribute | Value |
| --- | --- |
| `name` | The node's key |
| `transport` | `ssh` or `worker` |
| `source` | The source that produced the node, `nodes.yaml`, or `workers` |

Rules:

- Node names match `^[A-Za-z0-9][A-Za-z0-9._@:-]*$`, so hostnames and worker
  IDs are valid names. `local` is reserved.
- Attribute names match `^[A-Za-z][A-Za-z0-9_-]*$`. `name`, `transport`,
  `source`, `nodename`, and `username` are reserved.
- Tags match `^[A-Za-z0-9][A-Za-z0-9._:-]*$`.
- `hostname` is a DNS name, an IPv4 address, or a bracketed IPv6 address,
  with an optional `:port`. `user` matches `^[A-Za-z0-9_][A-Za-z0-9._-]*$`.
  Built-in attributes are therefore safe to insert into a command unquoted.
- After merging, a node with transport `ssh` must have a `hostname`.
- A node that breaks a rule is still listed and still matched by filters. It
  carries a problem message, and RFC 013 fails any step item that targets it.
  Leaving it out would make a filter match fewer hosts without saying why.

#### SSH settings

| Field | Meaning |
| --- | --- |
| `key` | Private key: `{ secret: <ref> }`, `{ file: <path> }`, or a path string |
| `password` | Password: `{ secret: <ref> }` only |
| `passphrase` | Passphrase for an encrypted key: `{ secret: <ref> }` only |
| `bastion` | Name of another node to connect through |
| `host_key` | Pinned public key, such as `ssh-ed25519 AAAA...` |
| `strict_host_key` | Default `true` |
| `known_hosts` | Path of an extra known-hosts file |
| `shell` | Remote shell, as `ssh.run`'s `shell` |
| `timeout` | Connect timeout. Default `30s` |

`{ secret: <ref> }` names an entry in the project's secret registry. Registry
entries can be backed by Vault, AWS, and the other providers, so every store
Dagu supports works here. Literal passwords are rejected, so `nodes.yaml` is
safe to commit.

The same credential form is added to `ssh.run`, `sftp.upload`,
`sftp.download`, and the DAG-level `ssh` field by a change to
[Spec 042](../specs/042-ssh-run.md), which lands before this RFC.

#### Merging

A node can come from several places: a source, the workers list, and the
`nodes` block. Dagu merges them by name in this order, later winning:

```text
sources, in listed order ─▶ workers ─▶ nodes block in nodes.yaml
```

- Each field is taken from the last place that sets it.
- `tags` are the union of every place.
- `ssh` merges field by field.
- Within one place, a field missing on the node comes from that source's
  `defaults`, then from the file's `defaults`.

The `nodes` block goes last, so a short entry in `nodes.yaml` can add tags or
credentials to a node that a script discovers:

```yaml
nodes:
  i-0abc123:            # also listed by the ec2 source
    tags: [canary]
```

### Sources

| Field | Meaning |
| --- | --- |
| `name` | Required, unique, `^[a-z0-9][a-z0-9-]*$` |
| `file` | Path, relative to `nodes.yaml` |
| `command` | Argument list, run without a shell |
| `url` | HTTPS or HTTP URL, fetched with `GET` |
| `format` | `dagu` (default), `rundeck`, or `ansible` |
| `refresh` | For `command` and `url`. Default `15m`, minimum `30s` |
| `timeout` | For `command` and `url`. Default `60s` |
| `headers` | For `url`. Values are strings or `{ secret: <ref> }` |
| `defaults` | Node fields for this source's nodes |
| `required` | Default `true`. See [Failure behavior](#failure-behavior) |

Exactly one of `file`, `command`, or `url` is set.

- **`file`** is read when the inventory is read, and cached until the file
  changes.
- **`command`** runs in the directory of `nodes.yaml`, as the user that runs
  Dagu, with the server's environment. A non-zero exit is an error. Its stdout
  is parsed. Its stderr is kept in the source status, truncated and masked.
- **`url`** must return a 2xx status. TLS certificates are verified.

Output from a `command` or `url` is limited to 10 MiB.

#### Formats

| Format | Input |
| --- | --- |
| `dagu` | YAML or JSON with `nodes` and optional `defaults`, as in `nodes.yaml` |
| `rundeck` | A Rundeck resource document in YAML (map or list form), JSON, or XML, detected from the content |
| `ansible` | The JSON that `ansible-inventory --list` prints |

`ansible-inventory` resolves group variables, host ranges, and inventory
plugins exactly as Ansible does, so Dagu reads its output instead of parsing
inventory files itself.

#### Rundeck mapping

| Rundeck attribute | Dagu |
| --- | --- |
| `nodename`, or the map key | `name` |
| `hostname`, including `host:port` | `hostname` and `port` |
| `username` | `user` |
| `tags` | `tags` |
| `description` | `description` |
| `ssh-key-storage-path` | `ssh.key` as `{ secret: <ref> }` |
| `ssh-password-storage-path` | `ssh.password` as `{ secret: <ref> }` |
| `ssh-key-passphrase-storage-path` | `ssh.passphrase` as `{ secret: <ref> }` |
| `node-executor` naming an SSH executor, or unset | `transport: ssh` |
| `node-executor` naming anything else | The node is listed with a problem |
| Any other attribute, such as `osFamily` | A custom attribute of the same name |

A Key Storage path becomes a secret ref by dropping a leading `keys/`,
lowercasing, and replacing each run of characters other than `a-z`, `0-9`,
and `/` with `-`, trimmed at segment edges. `keys/prod/Web_01.pem` becomes
`prod/web-01-pem`. The Rundeck import tool lists every ref a project needs.

#### Ansible mapping

| Ansible | Dagu |
| --- | --- |
| Host name | `name` |
| `ansible_host` | `hostname` |
| `ansible_port` | `port` |
| `ansible_user` | `user` |
| `ansible_ssh_private_key_file` | `ssh.key` as `{ file: <path> }` |
| Every group containing the host, directly or through children, except `all` and `ungrouped` | `tags` |
| Other scalar host variables | Custom attributes |
| Non-scalar variables | Dropped, with a warning in the source status |
| `ansible_password`, `ansible_become_password` | Not read, with a warning. Use a secret |

### Workers

With `workers: true`, every worker connected to the coordinator is a node:

| Node field | Value |
| --- | --- |
| `name` | The worker ID |
| `transport` | `worker` |
| `hostname` | The host the worker reports in its heartbeat |
| Attributes | The worker's labels. A label whose name is reserved is dropped, with a warning |

A worker that stops sending heartbeats leaves the list. Worker IDs default to
`hostname@pid`, which changes on restart, so a worker meant to be targeted as
a node should set `worker.id`.

Workers are off by default. Turning them on changes what broad filters such as
`.*` match, and that should be a choice.

### Filters

A filter is a string of clauses separated by whitespace.

| Clause | Selects nodes where |
| --- | --- |
| `web01` | `name` matches `web01` |
| `name: web.*` | `name` matches the regular expression `web.*` |
| `name: web01,web02` | `name` is exactly `web01` or `web02` |
| `region: us-east-1` | The custom attribute `region` matches |
| `tags: web` | The node has tag `web` |
| `tags: web+prod` | The node has both tags |
| `tags: web+prod,canary` | The node has both `web` and `prod`, or has `canary` |
| `!tags: dev` | Excludes nodes with tag `dev` |
| `.*` | Every node |

Rules:

- A node is selected when it matches every include clause and no exclude
  clause. Exclusions win.
- A filter with only exclude clauses starts from every node.
- A clause with no attribute name matches `name`.
- A value without a comma is an RE2 regular expression that must match the
  whole attribute value. A value with commas is a list of exact values. Tag
  terms are exact.
- Clauses with the same attribute and the same sign join into one list:
  `name: a name: b` is `name: a,b`.
- An include clause on an attribute a node lacks does not match that node. An
  exclude clause on it does not exclude it.
- `nodename` and `username` are accepted as aliases of `name` and `user`, so
  Rundeck filters work unchanged.
- A value with spaces is written in double quotes: `role: "web tier"`.
- Matching is case-sensitive.
- An empty filter is an error. Every node is selected only by writing `.*`.
- A filter is at most 4 KiB.

These rules reproduce Rundeck's documented examples:

| Filter | Selects |
| --- | --- |
| `mynode1 mynode2` | Both nodes |
| `mynode1 !nodename: mynode2` | `mynode1` only |
| `tags: www+prod hostname: dev1.example.com,dev2.example.com` | Nodes tagged `www` and `prod` with either hostname |

The matched list is sorted by name, so the same inventory and filter always
give the same list. RFC 013 adds other orderings.

### Reading the inventory from a run

```text
                    ┌───────────────────────────────┐
nodes.yaml ────────▶│                               │
file sources ──────▶│ resolve(project, filter)      │──▶ nodes with SSH settings
snapshots ─────────▶│                               │    (secret refs, not values)
workers list ──────▶│                               │
                    └───────────────────────────────┘
         local run: reads the project directory
         run on a worker: asks the coordinator (ResolveNodes RPC)
```

- A run on the server host reads `nodes.yaml`, file sources, and snapshots
  from the project directory.
- A run on a worker cannot read those files. It calls a new coordinator RPC,
  `ResolveNodes`, which returns the matched nodes. This follows
  `ResolveSecretReference`, which workers already use for secrets.
- Resolution returns secret refs, never secret values. Values are fetched when
  a connection is made, through the existing secret resolution, and are added
  to the run's masking list.
- Runs never execute a `command` source or fetch a `url` source. They read
  the latest snapshot.

### Snapshots

The server refreshes each `command` and `url` source:

- at startup,
- every `refresh`,
- when `nodes.yaml` changes, and
- when a user asks for a refresh.

A snapshot holds the source's nodes, the time it was taken, and the error of
the last attempt, if any. A failed refresh keeps the previous nodes. With
several server processes, a file lock lets one refresh a source at a time.

Snapshots hold secret refs only. They are written with the permissions of
other files in the data directory.

### Host keys

Strict host key checking stays on by default. A node's host key is accepted
when it matches, in order:

1. the node's `ssh.host_key`,
2. the project's trusted host keys in `<data dir>/nodes/known_hosts`,
3. the node's `ssh.known_hosts` file, or `~/.ssh/known_hosts`.

**Test connection** connects to a node, authenticates, runs `true`, and
reports the host key fingerprint, the time taken, and any error. When the key
is unknown, a user with the role below can **Trust** it, which adds it to the
project's trusted host keys. Test connection never trusts a key by itself.

`strict_host_key: false` is allowed. The Nodes page marks such nodes.

### Surfaces

#### UI

A **Nodes** page in each project:

```text
┌ Nodes ───────────────────────────────────────────────────────────┐
│ Filter: tags: web+prod !name: web03        14 of 212 nodes match │
│                                                                  │
│ Sources  legacy 180 ✓ 2m ago   ec2 30 ⚠ refresh failed 1h ago    │
│          ansible 0 ✓   workers 2 ✓                               │
│                                                                  │
│ ☐ name    hostname     user    transport tags        source      │
│ ☐ web01   10.0.1.11    deploy  ssh       web,prod    nodes.yaml  │
│ ☐ web02   10.0.1.12    deploy  ssh       web,prod    nodes.yaml  │
│ ...                                                              │
│ [Test connection] [Refresh sources] [Edit nodes.yaml]            │
└──────────────────────────────────────────────────────────────────┘
```

- The filter box completes attribute names and tag values, explains syntax
  errors in place, and updates the match count as the user types.
- **Copy filter** puts the current filter on the clipboard for a DAG.
- Selecting nodes and **Test connection** checks them together and shows one
  result per node, with **Trust** next to unknown host keys.
- A node's drawer shows its attributes, tags, the sources that contributed to
  it, its SSH settings with secret refs (never values), and its last test
  result.
- **Edit nodes.yaml** opens the file in an editor with schema validation, as
  the base config page does. An invalid file cannot be saved.
- A source with an error shows the error and its stderr.

The **Remote Nodes** page is renamed **Remote servers**.

#### API

| Method and path | Purpose |
| --- | --- |
| `GET /nodes?filter=` | Matched nodes, the total count, and filter errors |
| `GET /nodes/{name}` | One node |
| `GET /nodes/sources` | Each source's status |
| `POST /nodes/refresh` | Refresh every source, or one with `?source=` |
| `POST /nodes/{name}/test-connection` | Test connection |
| `POST /nodes/{name}/trust-host-key` | Trust the key the last test saw |
| `GET /nodes/inventory`, `PUT /nodes/inventory` | Read and write `nodes.yaml` |

Every path takes `project`, as RFC 003 defines. `test-connection` follows the
existing `/remote-nodes/{id}/test-connection`.

#### CLI

```sh
dagu nodes list 'tags: web'         # matched nodes
dagu nodes show web01
dagu nodes test 'tags: web'         # test connection to each match
dagu nodes refresh [--source ec2]
dagu nodes validate [nodes.yaml]
```

Each command takes `--project`, and works against a remote server like the
other remote CLI commands.

#### MCP

`dagu_read` gains `target=nodes`, with `query` holding `filter=...`, and
`target=node` with `name`.

### Permissions

| Action | Role |
| --- | --- |
| List nodes, preview a filter, view sources | `viewer` |
| Test connection, refresh sources | `operator` |
| Trust a host key, edit `nodes.yaml` | The role that edits the project's base config |

Editing `nodes.yaml` can add a `command` source, which runs on the server.
That is why it takes the same role as editing base config, which can already
change what every DAG runs. Edits, refreshes, tests, and trusted keys are
written to the audit log.

The inventory is in the free tier. Audit entries follow the audit license
like every other entry.

## Security

- `nodes.yaml`, snapshots, API responses, and MCP results hold secret refs,
  never values.
- Passwords and passphrases can only be secret refs. A literal value is a
  validation error.
- Secret values are fetched only to open a connection, are kept in memory, and
  are masked in every log and status the run writes.
- `command` sources run as the Dagu user. Editing the inventory is restricted
  to the role that edits base config.
- `url` sources verify TLS. Header values given as secrets are masked in the
  source status.
- Attribute values from scripts, URLs, and Ansible are untrusted text. RFC 013
  inserts them into commands only where the author writes `${node.*}`, and
  its Security section covers quoting.
- Host keys are checked by default. Only a user with the trust role can add a
  key, and only after seeing its fingerprint.

## Failure behavior

| Event | Behavior |
| --- | --- |
| `nodes.yaml` is invalid | Resolution fails with the file, line, and error. The UI refuses to save an invalid file |
| A `file` source is missing or invalid | Resolution fails with the source name and the error |
| A required `command` or `url` source has never succeeded | Resolution fails with the source name and its last error |
| A `command` or `url` source fails after an earlier success | Its last good nodes are used. Resolution returns a warning naming the source and the age of its snapshot |
| A source with `required: false` fails | Its nodes are left out, with a warning |
| A node breaks a rule, such as a missing hostname | It is listed and matched, with a problem message |
| A filter does not parse | The API, CLI, and UI report the position and the error |
| The coordinator is unreachable from a worker | Resolution fails, as secret resolution does |

Failing a step that cannot see its whole inventory is safer than running it on
part of the fleet.

## Compatibility and rollout

- Additive. An install without `nodes.yaml` behaves as before.
- `nodes.yaml` at the root of a project DAG directory is reserved, as
  `base.yaml` is. RFC 003's migration reports any DAG file with that name.
- Node name `local` is reserved for RFC 013.
- The UI and docs say "remote servers" for remote nodes. Nothing in
  configuration or the API is renamed.

Rollout:

1. **Credentials from secrets** for `ssh.run`, `sftp.*`, and DAG-level `ssh`,
   with Spec 042 updated.
2. **Inventory core**: `nodes.yaml` with `defaults` and `nodes`, the filter
   language, `GET /nodes`, `dagu nodes list|show|validate`, and a read-only
   Nodes page. A new spec defines the file format and the filter language,
   with conformance tests through `dagu nodes list`.
3. **Sources**: `file`, `command`, and `url`, the three formats, snapshots, and
   source status.
4. **Workers** as nodes, and the `ResolveNodes` RPC.
5. **Host keys**: test connection, trust, and the project's trusted host keys.
6. **Editing**: the `nodes.yaml` editor, refresh, and the MCP targets.
7. **Rename** remote nodes to remote servers in the UI and docs.

## Verification and acceptance criteria

1. The three Rundeck examples in [Filters](#filters) select the stated nodes.
2. `tags: a+b,x` selects nodes with both `a` and `b`, and nodes with `x`.
3. `name: web01,web02` matches exactly, and `name: web0.` matches as a
   regular expression against the whole name.
4. An empty filter is an error, and `.*` selects every node.
5. A Rundeck resource document in YAML map form, YAML list form, JSON, and XML
   loads into the same nodes, and `ssh-key-storage-path: keys/prod/Web_01.pem`
   becomes `{ secret: prod/web-01-pem }`.
6. `ansible-inventory --list` output gives each host the tags of its groups and
   their parent groups, and its scalar host variables as attributes.
7. The merge order and tag union hold for a node listed by a source and by the
   `nodes` block.
8. A `command` source that fails after a success keeps its nodes and reports a
   warning. A required source that never succeeded fails resolution.
9. With `workers: true`, a worker appears as a node while connected and
   disappears when its heartbeats stop.
10. Test connection reports a fingerprint for an unknown key. After Trust, a
    strict connection succeeds. A changed host key fails.
11. No secret value appears in any API response, snapshot, log, or audit
    entry. A literal `password` fails validation.
12. `dagu nodes list`, `GET /nodes`, and the Nodes page give the same match
    for the same filter.
13. A `viewer` can list nodes and cannot test, refresh, trust, or edit.

## Alternatives considered

- **Install a worker on every host.** Works today with worker labels and
  `worker_selector`, and stays supported through `workers: true`. Rundeck and
  Ansible users chose agentless SSH, and installing an agent on hundreds of
  hosts is a migration of its own.
- **Declare nodes in `base.yaml` or in DAG files.** Puts fleet data into the
  DAG schema, makes `command` sources run while DAGs load, and gives two DAGs
  different views of one fleet.
- **A store managed through the UI**, like remote nodes. Not reviewable in git,
  and imports would land in a database-like file nobody edits by hand.
- **A new selector syntax**, such as Kubernetes label selectors or CEL. Cleaner,
  but every Rundeck filter would need rewriting, and `attr: value` already
  covers labels.
- **Parse Ansible inventory files directly.** Partial fidelity for group
  variables, ranges, and plugins. `ansible-inventory` is exact.
- **A server-wide inventory.** Simpler for one team, but RFC 003 makes the
  project the unit of access, and hosts are part of what a team owns. See
  [Open questions](#open-questions).

## Consequences

- Rundeck and Ansible users can bring their fleet data as it is.
- DAGs select hosts by role, so adding a host means editing the inventory, not
  every DAG.
- SSH keys can move off the Dagu host into the secret store.
- The server runs inventory scripts on a timer, which is new background work.
  It is bounded by `refresh`, `timeout`, and the output limit.
- "Node" now means an inventory entry in the UI and docs.

## Open questions

1. Should a server-wide inventory exist, shared by projects that opt in?
2. Should `workers: true` be the default once targeting ships?
3. Should Dagu parse static Ansible INI and YAML files itself, for hosts
   without Ansible installed?
4. Should built-in sources exist for EC2, Azure, GCP, and Kubernetes, or is a
   `command` source enough?
5. Should a node carry a `disabled` flag that every filter skips, to drain a
   host without editing filters?
6. Should tags merge as a union, as proposed, or should a later source replace
   them?
7. Should the node drawer show the node's recent runs? That needs an index of
   runs by node.
8. Should the model reserve fields now for WinRM and other transports?
