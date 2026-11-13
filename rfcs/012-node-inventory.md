# RFC 012: Node Inventory

- **Status:** Proposed
- **Date:** 2026-11-12
- **Affected areas:** a new `inventory` package, `persis/file`, `secret`,
  `runtime/builtin/ssh`, `service/frontend` (API, UI), `service/scheduler`,
  `service/coordinator`, `service/worker`, `service/mcp`, `cmn/config`,
  `cmd`, `api/v1`, `proto`, `ui`, and `docs`

## Summary

Give each project a list of the hosts it runs commands on. A node has a name,
a hostname, labels, and SSH settings whose credentials come from the secret
store. Labels are the same `key` or `key=value` labels that DAGs and workers
already have. Nodes come from a `nodes.yaml` file, from Rundeck resource files
and Ansible inventories, from scripts, and from connected workers. A form or
one command adds a host.

A filter selects nodes with Rundeck's filter syntax:

```text
labels: web+prod !name: web03
```

This RFC defines the inventory, the filter language, and the surfaces that
show them. [RFC 013](013-node-targeting.md) runs steps on the nodes a filter
selects. A server can also keep its inventory in Dagu Cloud, which is off by
default.

```text
nodes.yaml ──┐
sources ─────┼──▶ inventory ──▶ filter ──▶ matched nodes ──▶ RFC 013 runs steps
workers ─────┘        │
                      └──▶ Nodes page, API, CLI, MCP
```

## Problem statement

Rundeck users keep their fleet in a resource model: every host, how it is
grouped, its SSH user, and the Key Storage path of its key. Jobs select hosts
with a node filter, and nothing in a job names a host. Ansible users keep the
same data in an inventory.

Dagu has no such list:

| Gap | What happens today |
| --- | --- |
| No host list | Every `ssh.run` step names its own `host`. A DAG that runs on 40 hosts takes them as a parameter and fans out with `parallel` |
| No selection by role | Nothing maps "the production web servers" to hosts. Each DAG repeats the list or calls a script that builds it |
| Keys live on disk | `ssh.run` reads its key from a file path (`internal/runtime/builtin/ssh/client.go`) |
| Host keys per run | Strict host key checking reads `~/.ssh/known_hosts`. A fleet of new hosts fails until someone edits that file on the server |
| Nothing to import into | A Rundeck `resources.yaml` or an Ansible inventory has no place to go |

## Goals

1. A project lists its nodes in one place, with labels, from files,
   scripts, or workers.
2. Rundeck resource files and Ansible inventories load without conversion.
3. A Rundeck node filter, rewritten by the import tool, selects the same
   nodes. Where Dagu differs, the spec lists the difference, and no
   difference selects more nodes than Rundeck would.
4. SSH credentials come from the secret store and never reach logs, run
   status, API responses, or disk outside the store.
5. A user sees which nodes a filter matches, and checks that Dagu can reach
   a node, before any DAG runs on it.
6. A host is added with one form or one command.
7. Keeping the inventory in Dagu Cloud is a choice, and no run depends on it.
8. Nothing changes for installs that have no inventory.

## Non-goals

- Running anything on a node. RFC 013 does that.
- Transports other than SSH and Dagu workers.
- Configuration management, package state, or facts gathering.
- The items in [Future enhancements](#future-enhancements).

## Design

### Where the inventory lives

The inventory belongs to a project, as defined in
[RFC 003](003-remove-workspaces.md).

| Project | Inventory files | Snapshots and trusted host keys |
| --- | --- | --- |
| Default project | `nodes.yaml` beside the global `base.yaml` | `<data dir>/nodes/` |
| Project | `projects/<name>/nodes.yaml` | `projects/<name>/data/nodes/` |

The inventory belongs to the server, not to the DAG files. A DAG is the same
in every environment and names a role, such as `labels: web`. Which hosts have
that role differs between staging and production. So the inventory never
lives in a DAG directory, and git sync never reads or writes it.

A project without an inventory has no nodes.

Until RFC 003 ships, the installation has one inventory, at the default
project's location. Its secret refs resolve in the global scope, and roles on
it are server-wide. A DAG in a workspace cannot target nodes: `dagu validate`
and admission reject it. So RFC 003's migration moves no DAG that targets
nodes, and needs no inventory step.

### `nodes.yaml`

```yaml
defaults:
  user: deploy
  ssh:
    key: { secret: ssh/deploy }

nodes:
  web01:
    hostname: 10.0.1.11
    labels: [web, prod, region=us-east-1]
  db01:
    hostname: db01.internal:2222
    user: postgres
    labels: [db, prod]
    ssh:
      key: { secret: ssh/db }
      bastion: jump01
  jump01:
    hostname: jump.example.com
    labels: [bastion]

sources:
  - name: legacy
    file: rundeck/resources.yaml
    format: rundeck
  - name: ansible
    command: [ansible-inventory, -i, /etc/ansible/hosts, --list]
    format: ansible
    refresh: 1h
```

| Root field | Meaning |
| --- | --- |
| `defaults` | Node fields applied to every SSH node that does not set them |
| `nodes` | Nodes declared in this file, keyed by name |
| `sources` | Other places nodes come from |
| `workers` | When `false`, workers are not nodes. Default `true` |

Unknown root fields are a validation error.

The Nodes page and `dagu nodes add` write `nodes.ui.yaml` beside
`nodes.yaml`. It holds only a `nodes` block, and its entries count as entries
of the `nodes` block. A name in both files is a problem on the node. Dagu
never rewrites `nodes.yaml`, so hand-written comments and layout stay.

### Nodes

| Field | Meaning |
| --- | --- |
| `hostname` | Host name or IP address. `host:port` also sets `port` |
| `port` | SSH port. Default `22` |
| `user` | SSH user |
| `description` | Text shown in the UI |
| `labels` | Labels in any form a DAG's `labels` field takes, such as `[web, region=tokyo]` |
| `ssh` | Connection settings. See below |
| `disabled` | `true` keeps runs off the node. Default `false` |

Unknown node fields are a validation error. Dagu also sets two fields on every
node: `transport`, which is `ssh` or `worker`, and `source`, which is
`nodes.yaml`, the source's name, or `workers`.

Rules:

- Node names match `^[A-Za-z0-9][A-Za-z0-9_.@:-]*$`, so hostnames, worker
  IDs, and names such as `app_server_01` are valid. `local` is reserved.
- Labels follow the rules of DAG labels (`internal/ir/label.go`), and their
  keys and values ignore case. The keys `name`, `hostname`, `port`, `user`,
  `description`, `transport`, `source`, `nodename`, and `username` are
  reserved, since filters read those fields. An invalid or reserved label is
  dropped, with a warning.
- `hostname` is a DNS name, an IPv4 address, or a bracketed IPv6 address,
  with an optional `:port`. `user` matches `^[A-Za-z0-9_][A-Za-z0-9._-]*$`.
  These fields and label values are therefore safe to insert into a command
  unquoted.
- An SSH node must have a `hostname`.
- A disabled node is listed and matched by filters, and the Nodes page marks
  it. RFC 013 does not run it and records it as `disabled`.
- A node that breaks any other rule is still listed and matched. It carries
  a problem, and RFC 013 fails any item that targets it. Leaving it out would
  make a filter match fewer hosts without saying why.

#### SSH settings

| Field | Meaning |
| --- | --- |
| `key` | Private key: `{ secret: <ref> }`, or a file path |
| `password` | Password: `{ secret: <ref> }` only |
| `passphrase` | Passphrase for an encrypted key: `{ secret: <ref> }` only |
| `bastion` | Name of another node to connect through, one hop only |
| `host_key` | Pinned public key, such as `ssh-ed25519 AAAA...` |
| `strict_host_key` | Default `true` |
| `shell` | Remote shell, as `ssh.run`'s `shell` |
| `timeout` | Connect timeout. Default `30s` |

- `{ secret: <ref> }` names an entry in the project's secret registry.
  Literal passwords are rejected, so the inventory is safe to commit.
- A key path is absolute or starts with `~`, and is read on the host that
  opens the connection: the server, or the worker that runs the run.
- A node that sets `key` takes no `passphrase` from `defaults`, as
  [#3769](https://github.com/dagucloud/dagu/issues/3769) defines for base
  config.
- The same credential form reaches `ssh.run`, `sftp.*`, and the DAG-level
  `ssh` field through #3769, which lands first.

#### One place per name

A node comes from one place: the `nodes` block, one source, or one worker.

- A name that two places declare is a problem on the node, naming both.
- A `nodes` entry that sets only `labels`, `description`, or `disabled`
  does not declare a node. It adds to the node of that name from a source,
  and its labels win over the source's. This is how an operator labels a
  discovered host. For a worker, such an entry may set only `disabled`. A
  worker's labels come from its own configuration, so `worker_selector` and
  filters see the same labels. An entry that matches no node is a problem.
- A source's `defaults` fill fields its nodes did not set, then the file's
  `defaults` fill the rest. Defaults do not apply to workers.

### Sources

| Field | Meaning |
| --- | --- |
| `name` | Required, unique, `^[a-z0-9][a-z0-9-]*$` |
| `file` | Path, relative to `nodes.yaml` |
| `command` | Argument list, run without a shell |
| `format` | `dagu` (default), `rundeck`, or `ansible` |
| `refresh` | For `command`. Default `15m`, minimum `30s` |
| `timeout` | For `command`. Default `60s` |
| `defaults` | Node fields for this source's nodes |
| `required` | Default `true`. See [Failure behavior](#failure-behavior) |

Exactly one of `file` or `command` is set.

- **`file`** is read with the inventory and cached until it changes.
- **`command`** runs in the directory of `nodes.yaml`, as the user that runs
  Dagu, with the environment a step process starts from, so it holds none of
  Dagu's own credentials
  ([Spec 006](../specs/006-value-resolution-env.md)). A non-zero exit is an
  error. Its stdout is parsed, up to 10 MiB. Its stderr is kept in the
  source status, truncated and masked. A script that calls a cloud CLI or
  `curl` covers cloud and HTTP inventories.

#### Formats

| Format | Input |
| --- | --- |
| `dagu` | YAML or JSON with `nodes` and optional `defaults`, as in `nodes.yaml` |
| `rundeck` | A Rundeck resource document in YAML (map or list form), JSON, or XML, detected from the content |
| `ansible` | The JSON that `ansible-inventory --list` prints |

`ansible-inventory` resolves group variables, host ranges, and plugins as
Ansible does, so Dagu reads its output instead of parsing inventory files. A
server without Ansible reads a saved copy of that output as a `file` source.

#### Rundeck mapping

| Rundeck attribute | Dagu |
| --- | --- |
| `nodename`, or the map key | `name` |
| `hostname`, including `host:port` | `hostname` and `port` |
| `username` | `user` |
| The node's group list | Labels with no value |
| `description` | `description` |
| `ssh-key-storage-path` | `ssh.key` as `{ secret: <ref> }` |
| `ssh-password-storage-path` | `ssh.password` as `{ secret: <ref> }` |
| `ssh-key-passphrase-storage-path` | `ssh.passphrase` as `{ secret: <ref> }` |
| `node-executor` naming an SSH executor, or unset | `transport: ssh` |
| `node-executor` naming anything else | The node is listed with a problem |
| Any other attribute, such as `osFamily: unix` | A label, `osfamily=unix` |

A Key Storage path becomes a secret ref by dropping a leading `keys/`,
lowercasing, and replacing each run of characters other than `a-z`, `0-9`,
and `/` with `-`. `keys/prod/Web_01.pem` becomes `prod/web-01-pem`. Two paths
that map to one ref are a problem on each node that uses them.

#### Ansible mapping

| Ansible | Dagu |
| --- | --- |
| Host name | `name` |
| `ansible_host`, else `ansible_ssh_host`, else the host name | `hostname` |
| `ansible_port`, else `ansible_ssh_port` | `port` |
| `ansible_user`, else `ansible_ssh_user` | `user` |
| `ansible_ssh_private_key_file` | `ssh.key` as a file path |
| `ansible_connection` unset, `ssh`, `smart`, or `paramiko` | `transport: ssh` |
| Any other `ansible_connection`, such as `local` or `winrm` | The node is listed with a problem |
| Every group containing the host, except `all` and `ungrouped` | Labels with no value |

No other host variable is read. Ansible inventories carry many variables that
mean nothing to Dagu, and some hold passwords. A label that filters need is
added with a label-only `nodes` entry.

### Workers

Every worker that connects to the coordinator is a node, unless `workers` is
`false`:

| Node field | Value |
| --- | --- |
| `name` | The worker ID |
| `transport` | `worker` |
| `hostname` | The host the worker reports in its heartbeat |
| `labels` | The worker's labels. An invalid or reserved one is dropped, with a warning |

A worker that sets `worker.id` stays listed after it disconnects. It is
`offline`, with the time it was last seen, and RFC 013 fails an item that
targets it. Otherwise a step would run on the workers that happen to be up
and say nothing of the rest. **Forget** on the Nodes page, or `dagu nodes
forget`, removes one.

A worker with the default ID, `hostname@pid`, gets a new ID on every restart,
so it leaves the list when its heartbeats stop. A worker meant to be targeted
sets `worker.id`.

### Filters

A filter is a string of clauses separated by whitespace.

| Clause | Selects nodes where |
| --- | --- |
| `web01` | `name` matches `web01` |
| `name: web.*` | `name` matches the regular expression `web.*` |
| `name: web01,web02` | `name` matches `web01` or `web02` |
| `hostname: 10.0.1.11` | `hostname` matches |
| `labels: web` | The node has a label with key `web` |
| `labels: web+prod` | The node has both |
| `labels: web+prod,canary` | The node has both `web` and `prod`, or has `canary` |
| `labels: region=tokyo` | The node has label `region=tokyo` |
| `region: tokyo` | The same, in Rundeck's attribute form |
| `!labels: dev` | Excludes nodes with label `dev` |
| `.*` | Every node |

Rules:

- A node is selected when it matches every include clause and no exclude
  clause. A filter with only exclude clauses starts from every node.
- A clause with no key matches `name`. A clause on `name`, `hostname`,
  `port`, `user`, `description`, `transport`, or `source` reads that field.
  A clause on any other key reads the value of the label with that key.
- A value is a comma-separated list. Each item matches when it equals the
  value or, as an RE2 regular expression, matches the whole value. Items of
  a `labels:` clause are exact.
- An item that does not compile, including the Java constructs RE2 lacks
  such as lookaround, is a filter error with its position. Comparing it for
  equality instead could make an exclusion exclude nothing.
- An include clause on a label a node lacks does not match that node. An
  exclude clause on it does not exclude it. An exclude clause whose key is
  neither a field nor a label key that any node in the inventory has is a
  filter error, since it would exclude nothing. A misspelled or foreign
  keyword fails instead of widening the selection.
- Label keys and values match without case. Other fields match with case.
- An empty filter is an error. Every node is selected only by `.*`.
- The matched list is sorted by name.

Rundeck's filter keywords differ only in name. The import tool (RFC 018)
rewrites its grouping keyword as `labels:`, and `nodename` and `username` as
`name` and `user`. The spec defines compatibility as a set of resource files
and rewritten filters run against Rundeck, with the nodes Rundeck selects.
Each case where Dagu selects differently is listed in the spec, and none
selects more.

### Reading the inventory from a run

- A run on the server reads the inventory files, sources, and snapshots from
  the project directory. The server, scheduler, and coordinator keep the
  merged inventory in memory and rebuild it when a file or the workers
  change.
- A run on a worker calls a new coordinator RPC, `ResolveNodes`, with a
  filter or with exact node names. The answer holds the matched nodes, the
  nodes they use as bastions, and the project's trusted host keys for them.
- Runs never execute a `command` source. They read its latest snapshot.
- Resolution returns secret refs, never values. Each distinct ref resolves
  once per run, when the first step that needs it starts, and joins the
  run's masking list. A ref that does not resolve fails the items that use
  it, as RFC 013 defines.
- A worker fetches a value with `ResolveSecretReference`, setting a new
  `node` field in place of `name`. The coordinator serves it when the
  worker's lease is fresh, the attempt targets nodes, and a node in the
  project's inventory uses that ref.

### Snapshots

The active scheduler runs each `command` source at startup, every
`refresh`, and when `nodes.yaml` changes. A user can also ask for a refresh,
which the server runs. Without a scheduler, sources refresh only when asked.

A snapshot holds the source's nodes, the time it was taken, and the error of
the last attempt. A failed refresh keeps the previous nodes. Snapshots hold
secret refs only.

### Host keys

Strict host key checking is on by default. A host key is accepted when it
matches the node's `ssh.host_key` or a key in the project's trusted host
keys. Trusted keys are stored by node name, not address, so two nodes that
share a private address behind different bastions keep separate keys.

**Test connection** checks the host key, authenticates, runs `true`, and
reports the fingerprint, the time taken, and any error. When a key is
unknown or has changed, the test stops before any credential is sent, and
names the hop that presented it: the bastion or the node. Results stream one
node at a time, with at most 16 connections open at once.

**Trust** adds keys to the project's trusted host keys. The request names
each node and the fingerprint the user saw. The server reads each key again
and stores it only when the fingerprints match. One request trusts many
nodes, so hosts from an import take one confirmation. Replacing a changed key
shows the old and new fingerprints and asks first. Test connection never
trusts a key by itself.

Tests and trusts run from the server. A host that only a worker reaches pins
its key with `ssh.host_key`.

`strict_host_key: false` is allowed. The Nodes page marks such nodes.

### Dagu Cloud sync

A server connected to Dagu Cloud ([RFC 002](002-dagu-cloud-connector.md)) can
keep each project's inventory there. It is off by default, and an admin of
the server turns it on, in the UI or in configuration:

```yaml
cloud:
  inventory_sync: false          # omit to choose in the UI
```

In Dagu Cloud, an inventory belongs to the workspace the server is connected
to, and to the project. Every server of that workspace that syncs the project
shares it. RFC 018 of the Dagu Cloud repository defines that side.

- **What travels:** `nodes.yaml`, `nodes.ui.yaml`, and the `file` sources
  they name, up to 4 MiB together. Secret values, trusted host keys,
  snapshots, and the workers list stay on each server.
- **Local first.** Runs read the local files and never Dagu Cloud.
- **Push and pull.** After a local change the server sends the files, and
  Dagu Cloud stores them as the next version. The server pulls a newer
  version at startup, every 10 minutes, and after its own push, and
  validates it before it replaces the local files.
- **The last change wins.** Changes are not merged. Earlier versions are
  kept, and **History** on the Nodes page restores one as a new version.
- **Turning it on.** A server with no inventory takes Dagu Cloud's, which is
  how a rebuilt server gets its inventory back. When both hold one and they
  differ, nothing is copied until an admin chooses which to keep.

### Surfaces

#### UI

A **Nodes** page in each project:

```text
┌ Nodes ───────────────────────────────────────────────────────────┐
│ Filter: labels: web+prod !name: web03      14 of 212 nodes match │
│                                                                  │
│ Sources  legacy 180 ✓ 2m ago   ansible 30 ⚠ failed 1h ago        │
│          workers 2 ✓ 1 offline                                   │
│                                                                  │
│ ☐ name    hostname     user    transport labels      source      │
│ ☐ web01   10.0.1.11    deploy  ssh       web,prod    nodes.yaml  │
│ ☐ web02   10.0.1.12    deploy  ssh       web,prod    legacy      │
│ ...                                                              │
│ [Add node] [Import] [Test connection] [Refresh sources]          │
└──────────────────────────────────────────────────────────────────┘
```

- The filter box completes label keys and values, explains
  syntax errors in place, and updates the match count as the user types.
- **Add node** is a form for one host: name, hostname, user, labels, and key.
  It tests the connection and shows the host key to trust before it saves.
  **Import** takes a Rundeck or Ansible file and adds it as a `file` source.
- **Test connection** checks the selected or matched nodes and shows each
  result as it arrives, with **Trust** next to unknown or changed keys.
- A node's drawer shows its labels with where each came from, its SSH
  settings with secret refs, and its last test result. It edits the node's
  labels, disables it, and removes it.
- **Edit as YAML** opens `nodes.yaml` with schema validation. An invalid
  file cannot be saved.

#### API

| Method and path | Purpose |
| --- | --- |
| `GET /nodes?filter=` | Matched nodes, one page at a time, with the total and filter errors |
| `GET /nodes/{name}` | One node |
| `PUT /nodes/{name}`, `DELETE /nodes/{name}` | Add, change, or remove an entry in `nodes.ui.yaml`. `DELETE` also forgets an offline worker |
| `GET /node-labels` | Label keys and values, for completion |
| `POST /node-imports` | Add an uploaded Rundeck or Ansible file as a `file` source |
| `POST /node-connection-tests` | Test named nodes or a filter's matches |
| `POST /node-host-keys` | Trust host keys, given each node and the fingerprint the user saw |
| `GET /node-sources`, `POST /node-sources/refresh` | Source status, and refresh |
| `GET /node-inventory`, `PUT /node-inventory` | Read and write `nodes.yaml` |
| `GET /node-inventory/versions`, `POST /node-inventory/versions/{n}/restore` | With Dagu Cloud sync on, list versions and restore one |

Collection operations sit outside `/nodes/{name}`, so every valid node name
reaches its node. Once RFC 003 ships, every path takes `project`. Tests and
trusts answer with a server-sent event stream, one event per node.

#### CLI

```sh
dagu nodes add web01 --hostname 10.0.1.11 --user deploy --labels web,prod
dagu nodes remove web01
dagu nodes forget osaka-1           # an offline worker
dagu nodes list 'labels: web'
dagu nodes show web01
dagu nodes test 'labels: web'
dagu nodes refresh [--source ansible]
dagu nodes validate
dagu nodes list 'labels: web' --inventory ./out/nodes.yaml --output json
```

Each command works against a remote server like the other remote CLI
commands, and takes `--project` once RFC 003 ships. `add` tests the
connection and asks before it trusts the host key. `list --inventory` reads
a file without a server, so the Rundeck import tool (RFC 018) checks resource
files and filters with Dagu's own parser.

#### MCP

`dagu_read` gains `target=nodes`, with a `filter` field and paging, and
`target=node`, with `name`.

### Permissions

| Action | Role |
| --- | --- |
| List nodes, preview a filter, view source status | `viewer` |
| Test connection, refresh sources | `operator` |
| Add, change, remove, or import nodes, forget a worker, trust a host key, edit `nodes.yaml`, restore a version | The role that edits the project's base config |
| Turn Dagu Cloud sync on or off | `admin` |

Editing the inventory can add a `command` source, which runs on the server,
so it takes the same role as editing base config. Edits, refreshes, tests,
and trusted keys are written to the audit log.

The inventory is in the free tier.

## Security

- Inventory files, snapshots, API responses, and MCP results hold secret
  refs, never values.
- Passwords and passphrases can only be secret refs.
- Secret values are fetched once per run, kept in memory, and masked in
  every log and status the run writes.
- `command` sources run as the Dagu user, without Dagu's own credentials in
  their environment.
- Labels from scripts and Ansible are untrusted text. RFC 013 inserts them
  into commands only where the author writes `${node.*}`.
- A worker's labels come from its configuration. Whoever configures a
  worker decides which filters select it.
- With strict checking on, no credential is sent to a host whose key is
  unknown or has changed, even by Test connection. Only a user with the trust
  role adds a key, and only the key whose fingerprint that user saw.
- Dagu Cloud sync sends hostnames, users, labels, and secret refs, and
  nothing else. A server that pulls a version runs its `command`
  sources, so servers that sync one project trust each other's inventory
  editors. Host key trust is never synced, so a hostname changed in a pulled
  version still meets strict checking.

## Failure behavior

| Event | Behavior |
| --- | --- |
| An inventory file is invalid | Resolution fails with the file, line, and error. The UI refuses to save it |
| A required source has never succeeded | Resolution fails with the source name and its last error |
| A `command` source fails after an earlier success | Its last good nodes are used, with a warning naming the source and the snapshot's age |
| A source with `required: false` fails with nothing earlier to use | Its nodes are left out, with a warning |
| A node breaks a rule, or two places declare its name | It is listed and matched, with a problem |
| A worker with a set ID is offline | It is listed as `offline`, and RFC 013 fails an item that targets it |
| A filter does not parse, or excludes by a key no node has | The API, CLI, and UI report the position and the error. A step with that filter fails before any node starts |
| The coordinator is unreachable from a worker | Resolution fails, as secret resolution does |
| Dagu Cloud is unreachable with sync on | Runs and local edits are unaffected. The change is sent later, and the Nodes page shows the delay |
| A pulled version does not validate | It is not applied. The local files stay, and the Nodes page shows the error |

Failing a step that cannot see its whole inventory is safer than running it on
part of the fleet.

## Compatibility and rollout

- Additive. An install without an inventory behaves as before.
- `nodes.yaml` and `nodes.ui.yaml` beside `base.yaml` are new files.
  `cloud.inventory_sync` is a new setting in RFC 002's `cloud` section.
- Node name `local` is reserved for RFC 013.

Rollout:

1. **Credentials from secrets** for `ssh.run`, `sftp.*`, and DAG-level `ssh`
   (#3769).
2. **Inventory core**: `nodes.yaml`, `file` and `command` sources, the three
   formats, snapshots, workers as nodes, the filter language, `GET /nodes`,
   `dagu nodes list|show|validate|refresh`, and a read-only Nodes page. A new
   spec defines the file format and the filter language, with conformance
   tests through `dagu nodes list` and the Rundeck comparison set.
3. **Host keys**: test connection and trust from the server.
4. **Editing**: the forms, import, `dagu nodes add|remove|forget`, the YAML
   editor, and the MCP targets.
5. **Runs on workers**: `ResolveNodes` and the `node` field on
   `ResolveSecretReference`, with RFC 013.
6. **Dagu Cloud sync**, once Dagu Cloud accepts inventories.

## Verification and acceptance criteria

The sections above state the rules, and the new spec tests each one. This RFC
is accepted when the following hold. Item 7 needs a step on a node, so it is
checked with RFC 013's first rollout phase.

1. The Rundeck comparison set passes, and `dagu nodes list`, `GET /nodes`,
   and the Nodes page give the same match.
2. A Rundeck resource document in each of its four forms, and
   `ansible-inventory --list` output, load into the nodes the mapping tables
   give.
3. A label-only `nodes` entry adds to a discovered node, a worker keeps the
   labels its configuration gives it, and a name declared in two places is a
   problem.
4. Every case in [Failure behavior](#failure-behavior) ends as stated.
5. No secret value appears in any API response, snapshot, log, or audit
   entry, and a literal credential fails validation.
6. With strict checking, no credential reaches a host whose key is unknown
   or has changed, and a key is trusted only by the fingerprint the user saw.
7. A run on a worker reaches a node with a key from a secret and a host key
   trusted in the UI.
8. `dagu nodes add` and the Add node form give a usable node in one step,
   and leave `nodes.yaml` unchanged.
9. Each role can do what [Permissions](#permissions) lists and nothing more.
10. With Dagu Cloud sync on, a rebuilt server gets its inventory back, two
    servers of one workspace share a project's inventory, and runs keep
    working while Dagu Cloud is unreachable.

## Alternatives considered

- **Install a worker on every host.** Supported, since workers are nodes.
  Rundeck and Ansible users chose agentless SSH, and installing an agent on
  hundreds of hosts is a migration of its own.
- **Nodes in `base.yaml` or in DAG files.** Puts fleet data into the DAG
  schema and gives two DAGs different views of one fleet.
- **The inventory in the DAG directory, synced with the DAGs.** Every server
  that syncs the repository would get the same hosts, and a staging server
  would hold production's.
- **Dagu Cloud as the only place the inventory lives.** Nothing to keep on
  the server, but runs would stop whenever Dagu Cloud is unreachable.
- **A new selector syntax**, such as Kubernetes label selectors. Every
  Rundeck filter would need more than a keyword rewrite.
- **Merging one node's fields from several places.** Rundeck does this. It
  lets a stale source overwrite a correct value without a sign, and makes
  every node's origin hard to explain.

## Consequences

- Rundeck and Ansible users bring their fleet data as it is.
- DAGs select hosts by role, so adding a host means editing the inventory,
  not every DAG.
- SSH keys move off the Dagu host into the secret store.
- The active scheduler runs inventory scripts on a timer, bounded by
  `refresh`, `timeout`, and the output limit.
- "Node" now means an inventory entry in the UI and docs.
- With sync on, Dagu Cloud holds host lists for the first time.

## Future enhancements

These are left out to keep the first version small. Each fits on top of the
design above without changing it.

- **Tests and trust from a worker.** A host that only a worker reaches is
  tested by a run dispatched to that worker, once RFC 013 runs node items
  there.
- **Routing SSH through a worker.** A `via` worker selector on a node opens
  its connection from a matching worker, for hosts in networks the server
  cannot reach.
- **More host key forms.** Several trusted keys per host for planned
  rotation, and a host CA, so rebuilt hosts need no new trust.
- **`url` sources**, with credential headers from secrets.
- **Built-in cloud sources** for EC2, Azure, GCP, and Kubernetes.
- **Limiting which nodes a user may target**, on top of the filter language.
- **A server-wide inventory** shared by projects that opt in.
- **Recent runs per node** in the drawer, from an index of runs by node.
- **Other transports**, such as WinRM. A transport adds its own map beside
  `ssh`.
- **Renaming "Remote Nodes"** to "Remote servers" in the UI, so two pages are
  not both called nodes.
