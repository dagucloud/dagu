# RFC 012: Node Inventory

- **Status:** Proposed
- **Date:** 2026-11-12
- **Affected areas:** a new `inventory` package, `ir` (label filters),
  `persis/file`, `secret`, `runtime/builtin/ssh`, `service/frontend` (API,
  UI), `service/coordinator`, `service/worker`, `service/mcp`, `cmn/config`,
  `cmd`, `api/v1`, `proto`, `ui`, and `docs`

## Summary

Give each project a list of the hosts it runs on. SSH hosts are listed in
one `nodes.yaml` file, with SSH settings whose credentials come from the
secret store. Workers with a fixed ID appear beside them. Every node has a
name and labels, the same `key` or `key=value` labels that DAGs and workers
already have, and nodes are selected with the label filter that already
filters DAGs:

```text
web,env=prod,!canary
```

A form or one command adds a host. [RFC 013](013-node-targeting.md) runs
workflows and steps on the nodes a selector matches. A server can also keep
its inventory in Dagu Cloud, which is off by default.

```text
nodes.yaml ──┬──▶ inventory ──▶ selector ──▶ matched nodes ──▶ RFC 013 runs
workers ─────┘        │
                      └──▶ Nodes page, API, CLI, MCP
```

## Problem statement

Dagu runs commands on remote hosts with `ssh.run`, but it has no list of
hosts:

| Gap | What happens today |
| --- | --- |
| No host list | Every `ssh.run` step names its own `host`. A DAG that runs on 40 hosts takes them as a parameter and fans out with `parallel` |
| No selection by role | Nothing maps "the production web servers" to hosts. Each DAG repeats the list or calls a script that builds it |
| Keys live on disk | `ssh.run` reads its key from a file path (`internal/runtime/builtin/ssh/client.go`) |
| Host keys per run | Strict host key checking reads `~/.ssh/known_hosts`. A fleet of new hosts fails until someone edits that file on the server |
| Nothing to migrate into | A Rundeck resource file or an Ansible inventory has no place to go |

## Goals

1. A project lists its SSH hosts in one file, and its workers appear beside
   them, all with labels.
2. Nodes are selected with the label filter Dagu already uses for DAGs.
3. SSH credentials come from the secret store and never reach logs, run
   status, API responses, or disk outside the store.
4. A user sees which nodes a selector matches, and checks that Dagu can reach
   a node, before any DAG runs on it.
5. A host is added with one form or one command.
6. Keeping the inventory in Dagu Cloud is a choice, and no run depends on it.
7. Nothing changes for installs that have no inventory.

## Non-goals

- Running anything on a node. RFC 013 does that.
- Transports other than SSH and Dagu workers.
- Configuration management, package state, or facts gathering.
- The items in [Future enhancements](#future-enhancements).

## Design

### Where the inventory lives

The inventory belongs to a project, as defined in
[RFC 003](003-remove-workspaces.md).

| Project | Inventory file | Trusted host keys |
| --- | --- | --- |
| Default project | `nodes.yaml` beside the global `base.yaml` | `<data dir>/nodes/` |
| Project | `projects/<name>/nodes.yaml` | `projects/<name>/data/nodes/` |

The inventory belongs to the server, not to the DAG files. A DAG is the same
in every environment and names a role, such as `web`. Which hosts have that
role differs between staging and production. So the inventory never lives in
a DAG directory, and git sync never reads or writes it.

A project without `nodes.yaml` has only its worker nodes.

Until RFC 003 ships, the installation has one inventory, at the default
project's location. Its secret refs resolve in the global scope, and roles on
it are server-wide. A DAG in a workspace cannot target nodes: `dagu validate`
and admission reject it. So RFC 003's migration moves no DAG that targets
nodes, and needs no inventory step.

### `nodes.yaml`

```yaml
defaults:
  user: deploy
  key: { secret: ssh/deploy }

nodes:
  web01:
    host: 10.0.1.11
    labels: [web, env=prod, region=us-east-1]
  web02:
    host: 10.0.1.12
    labels: [web, env=prod, region=us-east-1, canary]
  db01:
    host: db01.internal
    port: 2222
    user: postgres
    key: { secret: ssh/db }
    bastion: jump01
    labels: [db, env=prod]
  jump01:
    host: jump.example.com
    labels: [bastion]
```

| Root field | Meaning |
| --- | --- |
| `defaults` | Connection fields applied to every node that does not set them |
| `nodes` | Nodes, keyed by name |

Unknown fields, at the root or on a node, are a validation error.

The Nodes page and `dagu nodes add` edit `nodes.yaml` in place, keeping its
comments and order. A save fails when the file changed after it was read.

### Nodes

| Field | Meaning |
| --- | --- |
| `host` | Host name or IP address. Required |
| `port` | SSH port. Default `22` |
| `user` | SSH user |
| `key` | Private key: `{ secret: <ref> }`, `{ file: <path> }`, or a path |
| `password` | Password: `{ secret: <ref> }` only |
| `passphrase` | Passphrase for an encrypted key: `{ secret: <ref> }` only |
| `bastion` | Name of another node to connect through, one hop only |
| `host_key` | Pinned public key, such as `ssh-ed25519 AAAA...` |
| `strict_host_key` | Default `true` |
| `shell` | Remote shell |
| `timeout` | Connect timeout. Default `30s` |
| `labels` | Labels in any form a DAG's `labels` field takes |
| `description` | Text shown in the UI |
| `disabled` | `true` keeps runs off the node. Default `false` |

`defaults` takes the fields from `port` through `timeout`.

The connection fields have the names and forms that `ssh.run` takes once
[#3769](https://github.com/dagucloud/dagu/issues/3769) lands, so a setting
moves between a step and a node unchanged. Two differ: `bastion` names a
node, and `password` and `passphrase` take only secret refs.

Rules:

- Node names match `^[A-Za-z0-9][A-Za-z0-9_.-]*$`, so hostnames and names
  such as `app_server_01` are valid.
- Labels follow the rules of DAG labels (`internal/ir/label.go`). Their keys
  and values are lowercased. Fields and labels are separate, so a label may
  have any valid key, including `host` or `name`.
- `host` is a DNS name, an IPv4 address, or an IPv6 address. `user` matches
  `^[A-Za-z0-9_][A-Za-z0-9._-]*$`. These fields and label values are
  therefore safe to insert into a command unquoted.
- `{ secret: <ref> }` names an entry in the project's secret registry.
  Literal passwords are rejected, so the file is safe to commit.
- A key path is read on the host that opens the connection: the server, or
  the worker that runs the run.
- A node that sets `key` takes no `passphrase` from `defaults`, as #3769
  defines for base config.
- A disabled node is listed and matched, and the Nodes page marks it. RFC
  013 does not run it and records it as `disabled`.
- A file that breaks any rule is invalid as a whole. The UI refuses to save
  it, and resolution fails with the file, line, and error, so no step runs
  on part of the fleet.

### Workers

Every worker that sets `worker.id` is a node. Workers are shared by every
project ([RFC 003](003-remove-workspaces.md)), so every project lists them.

| Node field | Value |
| --- | --- |
| `name` | The worker ID |
| `host` | The host the worker reports, shown only |
| `labels` | The worker's `worker.labels` |

- Each node has a kind, `ssh` or `worker`, shown in the UI and API. RFC 013
  runs a workflow on a worker node by dispatching it to that worker.
- Worker labels follow the label rules from this RFC on. Keys and values are
  lowercased, and a label with no value is kept, where the configuration
  drops it today (`internal/cmn/config/loader.go`). `worker_selector` and
  `--worker-label` compare the same way, so they and selectors read one set
  of labels. A worker whose labels break the rules does not start, and names
  the label.
- A worker without `worker.id` gets `hostname@pid`, a new ID on every
  restart, so it is not a node.
- The coordinator remembers each worker node it has seen. One that
  disconnects stays listed as `offline`, with the time it was last seen, so a
  step does not quietly run on only the workers that happen to be up.
  **Forget** on the Nodes page, or `dagu nodes forget`, removes it until it
  connects again.
- A worker's labels come from its configuration, so `disabled` does not apply
  to it. A worker is kept out of a selector by changing its labels.
- A worker ID that names a node in `nodes.yaml` makes the worker no node, and
  the Nodes page shows the clash.

### Selecting nodes

A selector is the label filter that DAG lists take in `dagu history --labels`
and `GET /dags?labels=` (`internal/ir/label.go`). One parser serves both.

| Term | Selects nodes that |
| --- | --- |
| `web` | Have a label with key `web` |
| `env=prod` | Have the label `env=prod` |
| `region=us-*` | Have a `region` label that matches the glob. `*` and `?` work in keys and values |
| `!canary` | Have no label with key `canary` |
| `!env=dev` | Do not have the label `env=dev` |

Rules:

- Terms are separated by commas, or given as a YAML list. A node is
  selected when it matches every term.
- `!` before any term negates it.
- Keys and values match without case, since labels are lowercased.
- A term that is not a valid key, `key=value`, or glob, with or without `!`,
  is an error, so a mistyped operator fails instead of matching nothing.
- An empty selector is an error.
- A selector reads labels only. A DAG names roles, not hosts. RFC 013
  narrows a run to named hosts.
- The matched list is sorted by name.

Two changes to the shared parser apply to DAG list filters too:

- `!env=dev` is read today as "has no label whose key is `env=dev`", and
  matches every DAG. It becomes "does not have `env=dev`".
- An invalid term, such as `env==prod`, today selects nothing, or everything
  when negated. It becomes an error.

### Reading the inventory from a run

- A run on the server reads `nodes.yaml` from the project directory. The
  server and coordinator keep it in memory and reload it when the file
  changes. Worker nodes come from the coordinator.
- A run on a worker calls a new coordinator RPC, `ResolveNodes`, with a
  selector or with exact node names. The answer holds the matched nodes, the
  nodes they use as bastions, and the project's trusted host keys for them.
- Resolution returns secret refs, never values. Each distinct ref resolves
  once per run, when the first step that needs it starts, and joins the
  run's masking list. A ref that does not resolve fails the items that use
  it, as RFC 013 defines.
- A worker fetches a value with `ResolveSecretReference`, setting a new
  `node` field in place of `name`. The coordinator serves it when the
  worker's lease is fresh, the attempt targets nodes, and a node in the
  project's inventory uses that ref.

### Host keys

Host keys apply to SSH nodes. Strict host key checking is on by default. A
host key is accepted when it
matches the node's `host_key` or a key in the project's trusted host keys.
Trusted keys are stored by node name, not address, so two nodes that share a
private address behind different bastions keep separate keys.

**Test connection** checks the host key, authenticates, runs `true`, and
reports the fingerprint, the time taken, and any error. When a key is
unknown or has changed, the test stops before any credential is sent, and
names the hop that presented it: the bastion or the node. Results stream one
node at a time, with at most 16 connections open at once.

**Trust** adds keys to the project's trusted host keys. The request names
each node and the fingerprint the user saw. The server reads each key again
and stores it only when the fingerprints match. One request trusts many
nodes, so a batch of new hosts takes one confirmation. Replacing a changed
key shows the old and new fingerprints and asks first. Test connection never
trusts a key by itself.

Tests and trusts run from the server. A host that only a worker reaches pins
its key with `host_key`.

`strict_host_key: false` is allowed. The Nodes page marks such nodes.

### Coming from Rundeck or Ansible

A separate import tool, as [RFC 010](010-digdag-import.md) builds for
Digdag, converts a Rundeck resource document or the output of
`ansible-inventory --list` into `nodes.yaml` once, with a report. Every
SSH node of either fits: its name, host, port, and user become fields, its
groups become labels with no value, its other attributes become `key=value`
labels, and its Key Storage paths become secret refs that the report lists
for creation. The tool's own RFC defines the mapping. `dagu nodes validate`
checks its output before the file is installed.

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

- **What travels:** `nodes.yaml`, up to 4 MiB. Secret values, trusted host
  keys, and workers stay on each server.
- **Local first.** Runs read the local file and never Dagu Cloud.
- **Push and pull.** After a local change the server sends the file, and
  Dagu Cloud stores it as the next version. The server pulls a newer
  version at startup, every 10 minutes, and after its own push, and
  validates it before it replaces the local file.
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
│ Labels [web ×] [env=prod ×] [!canary ×]   Name ______            │
│                                             14 of 212 nodes      │
│                                                                  │
│ ☐ name     kind    host        user    labels                    │
│ ☐ web01    ssh     10.0.1.11   deploy  web, env=prod             │
│ ☐ web03    ssh     10.0.1.13   deploy  web, env=prod             │
│ ☐ osaka-1  worker  osaka-gw            web, env=prod, offline    │
│ ...                                                              │
│ [Add node] [Test connection] [Edit as YAML]                      │
└──────────────────────────────────────────────────────────────────┘
```

- The labels box is the label picker of the DAG list
  (`ui/src/components/ui/label-combobox.tsx`), with the inventory's labels as
  choices. The match count updates as the user types.
- **Add node** is a form for one host: name, host, user, labels, and key. It
  tests the connection and shows the host key to trust before it saves.
- **Test connection** checks the selected or matched nodes and shows each
  result as it arrives, with **Trust** next to unknown or changed keys.
- An SSH node's drawer shows its labels, its connection settings with secret
  refs, and its last test result. It edits the node's labels, disables it,
  and removes it. A worker's drawer shows its labels and when it was last
  seen, and forgets it when it is offline.
- **Edit as YAML** opens `nodes.yaml` with schema validation. An invalid
  file cannot be saved.

#### API

| Method and path | Purpose |
| --- | --- |
| `GET /nodes?labels=&name=` | Matched nodes, one page at a time, with the total and selector errors. `name` matches part of a node name |
| `GET /nodes/{name}` | One node |
| `PUT /nodes/{name}`, `DELETE /nodes/{name}` | Add, change, or remove a node in `nodes.yaml`. `DELETE` also forgets an offline worker |
| `GET /node-labels` | Label keys and values, for completion |
| `POST /node-connection-tests` | Test named nodes or a selector's matches |
| `POST /node-host-keys` | Trust host keys, given each node and the fingerprint the user saw |
| `GET /node-inventory`, `PUT /node-inventory` | Read and write `nodes.yaml` |
| `GET /node-inventory/versions`, `POST /node-inventory/versions/{n}/restore` | With Dagu Cloud sync on, list versions and restore one |

Collection operations sit outside `/nodes/{name}`, so every valid node name
reaches its node. Once RFC 003 ships, every path takes `project`. Tests and
trusts answer with a server-sent event stream, one event per node. The
schemas are named `InventoryNode` and so on, since the API's `Node` schema is
a step's status.

#### CLI

```sh
dagu nodes add web01 --host 10.0.1.11 --user deploy --labels web,env=prod
dagu nodes remove web01
dagu nodes forget osaka-1           # an offline worker
dagu nodes list --labels 'web,env=prod,!canary'
dagu nodes show web01
dagu nodes test --labels web
dagu nodes validate [nodes.yaml]
```

Each command works against a remote server like the other remote CLI
commands, and takes `--project` once RFC 003 ships. `--labels` is the flag
`dagu history` takes. `add` tests the connection and asks before it trusts
the host key. `validate` with a path checks a file without a server.

#### MCP

`dagu_read` gains `target=nodes`, with a `labels` field and paging, and
`target=node`, with `name`.

### Permissions

| Action | Role |
| --- | --- |
| List nodes, preview a selector | `viewer` |
| Test connection | `operator` |
| Add, change, or remove nodes, trust a host key, edit `nodes.yaml`, restore a version | The role that edits the project's base config |
| Forget a worker, turn Dagu Cloud sync on or off | `admin` |

Editing the inventory decides which hosts receive the project's SSH
credentials, so it takes the same role as editing base config. Workers are
server-wide, so forgetting one takes `admin`. Edits, tests, and trusted keys
are written to the audit log.

The inventory is in the free tier.

## Security

- `nodes.yaml`, API responses, and MCP results hold secret refs, never
  values.
- Passwords and passphrases can only be secret refs.
- Secret values are fetched once per run, kept in memory, and masked in
  every log and status the run writes.
- Labels are inserted into commands only where the author writes
  `${node.*}` (RFC 013).
- A worker's labels come from its configuration. Whoever configures a
  worker decides which selectors match it.
- With strict checking on, no credential is sent to a host whose key is
  unknown or has changed, even by Test connection. Only a user with the trust
  role adds a key, and only the key whose fingerprint that user saw.
- Dagu Cloud sync sends hostnames, users, labels, and secret refs, and
  nothing else. Host key trust is never synced, so a hostname changed in a
  pulled version still meets strict checking.

## Failure behavior

| Event | Behavior |
| --- | --- |
| `nodes.yaml` is invalid | Resolution fails with the file, line, and error. The UI refuses to save it |
| A save finds the file changed after it was read | The save fails, and the UI shows the current file |
| A selector is invalid | The API, CLI, and UI report the term and the error. A step with that selector fails before any node starts |
| A worker node is offline | It is listed as `offline`, and RFC 013 fails a run that targets it |
| A worker's labels break the label rules | The worker does not start, and names the label |
| A worker ID names a node in `nodes.yaml` | The worker is not a node. The Nodes page shows the clash |
| The coordinator is unreachable from a worker | Resolution fails, as secret resolution does |
| Dagu Cloud is unreachable with sync on | Runs and local edits are unaffected. The change is sent later, and the Nodes page shows the delay |
| A pulled version does not validate | It is not applied. The local file stays, and the Nodes page shows the error |

Failing a step that cannot see its whole inventory is safer than running it on
part of the fleet.

## Compatibility and rollout

- Additive. An install without an inventory behaves as before.
- `nodes.yaml` beside `base.yaml` is a new file. `cloud.inventory_sync` is a
  new setting in RFC 002's `cloud` section.
- DAG list filters change as [Selecting nodes](#selecting-nodes) lists:
  `!key=value` excludes, and an invalid term is an error.
- Worker labels change as [Workers](#workers) lists. `worker_selector` and
  `--worker-label` match without case, a worker label with no value is kept,
  and a worker with an invalid label no longer starts.

Rollout:

1. **Credentials from secrets** for `ssh.run`, `sftp.*`, and DAG-level `ssh`
   (#3769).
2. **Inventory core**: `nodes.yaml`, selectors and the parser changes,
   `GET /nodes`, `dagu nodes list|show|validate`, and a read-only Nodes page.
   A new spec defines the file and selectors, with conformance tests through
   `dagu nodes list`.
3. **Host keys**: test connection and trust from the server.
4. **Editing**: the forms, `dagu nodes add|remove`, the YAML editor, and the
   MCP targets.
5. **Workers**: worker nodes, the worker label changes, `ResolveNodes`, and
   the `node` field on `ResolveSecretReference`, with RFC 013.
6. **Dagu Cloud sync**, once Dagu Cloud accepts inventories.

## Verification and acceptance criteria

The sections above state the rules, and the new spec tests each one. This RFC
is accepted when the following hold. Item 5 needs a step on a node, so it is
checked with RFC 013's first rollout phase.

1. A selector matches the same nodes in `dagu nodes list`, `GET /nodes`, the
   Nodes page, and an RFC 013 step, and the same terms filter DAGs the same
   way.
2. Every case in [Failure behavior](#failure-behavior) ends as stated.
3. No secret value appears in any API response, log, or audit entry, and a
   literal credential fails validation.
4. With strict checking, no credential reaches a host whose key is unknown
   or has changed, and a key is trusted only by the fingerprint the user saw.
5. A run on a worker reaches a node with a key from a secret and a host key
   trusted in the UI.
6. `dagu nodes add` and the Add node form give a usable node in one step,
   and keep the file's comments.
7. Each role can do what [Permissions](#permissions) lists and nothing more.
8. With Dagu Cloud sync on, a rebuilt server gets its inventory back, two
   servers of one workspace share a project's inventory, and runs keep
   working while Dagu Cloud is unreachable.
9. A worker with a set ID is listed with its labels, stays listed while
   offline, and matches a selector as an SSH node with the same labels does.

## Alternatives considered

- **Workers only.** Supported, since workers are nodes. Rundeck and Ansible
  users chose agentless SSH, and installing an agent on hundreds of hosts is
  a migration of its own.
- **Nodes in `base.yaml` or in DAG files.** Puts fleet data into the DAG
  schema and gives two DAGs different views of one fleet.
- **The inventory in the DAG directory, synced with the DAGs.** Every server
  that syncs the repository would get the same hosts, and a staging server
  would hold production's.
- **Dagu Cloud as the only place the inventory lives.** Nothing to keep on
  the server, but runs would stop whenever Dagu Cloud is unreachable.
- **Rundeck's filter language.** A second selector syntax, in which `,`
  means OR and `!` negates a clause, beside a label filter in which `,` means
  AND. Rundeck users convert their filters once instead.
- **Reading Rundeck and Ansible inventories live.** Keeps the foreign file as
  the truth, but needs merge rules, snapshots, and scripts on a timer. A
  one-time conversion suits a migration.
- **A second file for form edits.** Leaves hand-written text untouched, but
  gives two places to look and a rule for a name in both.

## Consequences

- DAGs and nodes are filtered with one syntax.
- DAGs select hosts by role, so adding a host means editing the inventory,
  not every DAG.
- SSH keys move off the Dagu host into the secret store.
- Rundeck and Ansible users convert their inventory once, and `nodes.yaml`
  becomes the source of truth.
- "Node" now means an inventory entry in the UI and docs.
- With sync on, Dagu Cloud holds host lists for the first time.

## Future enhancements

These are left out to keep the first version small. Each fits on top of the
design above without changing it.

- **Live sources.** A script or URL that prints nodes in Dagu's format, read
  on a timer, with built-in sources for EC2, Azure, GCP, and Kubernetes.
- **Selecting by name or host, and OR,** in a selector.
- **Tests and trust from a worker.** A host that only a worker reaches is
  tested by a run dispatched to that worker.
- **Routing SSH through a worker.** A `via` worker selector on a node opens
  its connection from a matching worker, for hosts in networks the server
  cannot reach.
- **More host key forms.** Several trusted keys per host for planned
  rotation, and a host CA, so rebuilt hosts need no new trust.
- **Limiting which nodes a user may target.**
- **A server-wide inventory** shared by projects that opt in.
- **Recent runs per node** in the drawer, from an index of runs by node.
- **Other transports**, such as WinRM.
- **Renaming "Remote Nodes"** to "Remote servers" in the UI, so two pages are
  not both called nodes.
