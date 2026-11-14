# RFC 012: Node Inventory

- **Status:** Proposed
- **Date:** 2026-11-12
- **Affected areas:** a new `inventory` package, `ir` (label filters),
  `persis/store`, `secret`, `runtime/builtin/ssh`, `service/frontend` (API,
  UI), `service/coordinator`, `service/worker`, `service/mcp`, `cmn/config`,
  `cmd`, `api/v1`, `proto`, `ui`, and `docs`

## Summary

Give each project lists of the hosts it runs on. A project holds any number
of inventories. Each is a named list of SSH hosts with shared defaults, kept
by Dagu as JSON and edited in the UI, CLI, or API. Credentials come from the
secret store. Workers with a fixed ID appear beside the hosts.

Every node has a name and labels, the same `key` or `key=value` labels that
DAGs and workers already have. Nodes are selected across all of a project's
inventories with the label filter that already filters DAGs:

```text
site=tokyo,web,!canary
```

A form or one command adds a host. [RFC 013](013-node-targeting.md) runs
workflows and steps on the nodes a selector matches. A server can also keep
its inventories in Dagu Cloud, which is off by default.

```text
inventories ──┬──▶ project's nodes ──▶ selector ──▶ matched nodes ──▶ RFC 013
workers ──────┘           │
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

1. A project keeps its SSH hosts in any number of named inventories, and its
   workers appear beside them, all with labels.
2. Nodes are selected with the label filter Dagu already uses for DAGs.
3. SSH credentials come from the secret store and never reach logs, run
   status, API responses, or disk outside the store.
4. A user sees which nodes a selector matches, and checks that Dagu can reach
   a node, before any DAG runs on it.
5. A host is added with one form or one command.
6. Keeping inventories in Dagu Cloud is a choice, and no run depends on it.
7. Nothing changes for installs that have no inventory.

## Non-goals

- Running anything on a node. RFC 013 does that.
- Transports other than SSH and Dagu workers.
- Configuration management, package state, or facts gathering.
- The items in [Future enhancements](#future-enhancements).

## Design

### Inventories

An inventory is a JSON record in a collection-backed store
(`internal/persis/store`), as profiles, secrets, and views are:

```json
{
  "id": "inv_7f3k2a",
  "name": "Tokyo production (DC-1)",
  "description": "Racks A and B",
  "defaults": {
    "user": "deploy",
    "key": { "secret": "ssh/tokyo" },
    "labels": ["site=tokyo", "env=prod"]
  },
  "nodes": [
    { "name": "tokyo-web01", "host": "10.0.1.11", "labels": ["web"] },
    { "name": "tokyo-web02", "host": "10.0.1.12", "labels": ["web", "canary"] },
    {
      "name": "tokyo-db01",
      "host": "db01.internal",
      "port": 2222,
      "user": "postgres",
      "key": { "secret": "ssh/tokyo-db" },
      "bastion": "tokyo-jump",
      "labels": ["db"]
    },
    { "name": "tokyo-jump", "host": "jump.tokyo.example.com" }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `id` | Set by Dagu when the inventory is created. It never changes, and the store keys the record by it |
| `name` | Display name, any text up to 200 characters. Unique in the project, compared without case |
| `description` | Text shown in the UI |
| `defaults` | Node fields from `port` through `timeout`, and `labels`, applied to this inventory's nodes |
| `nodes` | The inventory's nodes |

- The store keys a record by its ID, so a name has no file-name limits and
  can change at any time. DAGs select nodes by label and never name an
  inventory, so a rename breaks nothing.
- A default fills a field a node leaves unset. `defaults.labels` are added to
  each node's labels, and a node's label replaces a default label with the
  same key.
- Unknown fields are a validation error.

An inventory belongs to a project, as defined in
[RFC 003](003-projects.md). It belongs to the server, not to the
DAG files. A DAG is the same in every environment and names a role, such as
`web`. Which hosts have that role differs between staging and production. So
inventories never live in a DAG directory, and git sync never reads or writes
them.

A project's nodes are the nodes of all its inventories, plus the workers. A
project without inventories has only its worker nodes.

Until RFC 003 ships, the installation has one project. Its secret refs
resolve in the global scope, and roles on its nodes are server-wide. A DAG
in a workspace cannot target nodes: `dagu validate` and admission reject it.
So RFC 003's migration moves no DAG that targets nodes, and needs no
inventory step.

### Storage

Three collections hold the data, each one JSON file per record, as the
file-backed collection lays them out (`internal/persis/file/collection.go`):

| Collection | Location | One record per |
| --- | --- | --- |
| `inventories/` | The project's data directory: `<data dir>/` for the default project, `projects/<name>/data/` for a project | Inventory, keyed by its ID |
| `host-keys/` | The project's data directory | Trusted host key, keyed by node name |
| `worker-nodes/` | `<data dir>/`, since workers are server-wide | Worker node, keyed by worker ID |

- **Writes.** Every write to a project's inventories holds one lock for the
  project (`internal/cmn/dirlock`), checks the change against all of the
  project's inventories, and then stores it. A stored inventory is therefore
  always valid, and two changes that add the same node name to different
  inventories never both succeed. A change based on an older version of the
  record fails. Writes are rare, so one lock per project costs nothing.
- **A trusted host key** holds the node name, the key, its fingerprint, and
  who trusted it when. Removing or renaming a node deletes its key, so a new
  node that takes the name does not inherit the old host's trust.
- **A worker node** holds the worker ID, its host, its labels, and when it
  was last seen. The coordinator writes it when a worker first connects,
  changes its labels, or goes offline. Forget deletes it.
- **History.** The server keeps only the current version of each inventory.
  Earlier versions exist only with [Dagu Cloud sync](#dagu-cloud-sync) on.
  Without it, a bad edit is undone from an export or a backup of the data
  directory.

### Nodes

| Field | Meaning |
| --- | --- |
| `name` | Node name. Required |
| `host` | Host name or IP address. Required |
| `port` | SSH port. Default `22` |
| `user` | SSH user |
| `key` | Private key: `{ "secret": <ref> }`, `{ "file": <path> }`, or a path |
| `password` | Password: `{ "secret": <ref> }` only |
| `passphrase` | Passphrase for an encrypted key: `{ "secret": <ref> }` only |
| `bastion` | Name of another node of the same inventory to connect through, one hop only |
| `host_key` | Pinned public key, such as `ssh-ed25519 AAAA...` |
| `strict_host_key` | Default `true` |
| `shell` | Remote shell |
| `timeout` | Connect timeout. Default `30s` |
| `labels` | Labels, as `key` or `key=value` |
| `description` | Text shown in the UI |
| `disabled` | `true` keeps runs off the node. Default `false` |

The connection fields have the names and forms that `ssh.run` takes once
[#3769](https://github.com/dagucloud/dagu/issues/3769) lands, so a setting
moves between a step and a node unchanged. Two differ: `bastion` names a
node, and `password` and `passphrase` take only secret refs.

Rules:

- Node names match `^[A-Za-z0-9][A-Za-z0-9_.-]*$` and are unique across all
  of a project's inventories. RFC 013's node lists, `${node.name}`, and
  trusted host keys each name one node.
- Labels follow the rules of DAG labels (`internal/ir/label.go`). Their keys
  and values are lowercased. Fields and labels are separate, so a label may
  have any valid key, including `host` or `name`.
- `host` is a DNS name, an IPv4 address, or an IPv6 address. `user` matches
  `^[A-Za-z0-9_][A-Za-z0-9._-]*$`. These fields and label values are
  therefore safe to insert into a command unquoted.
- `{ "secret": <ref> }` names an entry in the project's secret registry.
  Literal passwords are rejected, so an exported inventory is safe to share.
- A key path is read on the host that opens the connection: the server, or
  the worker that runs the run.
- A node that sets `key` takes no `passphrase` from `defaults`, as #3769
  defines for base config.
- A disabled node is listed and matched, and the Nodes page marks it. RFC
  013 does not run it and records it as `disabled`.

### Workers

Every worker that sets `worker.id` is a node. Workers are shared by every
project (RFC 003), so every project lists them.

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
- The coordinator records each worker node it has seen in `worker-nodes/`.
  One that disconnects stays listed as `offline`, with the time it was last
  seen, so a step does not quietly run on only the workers that happen to be
  up.
  **Forget** on the Nodes page, or `dagu nodes forget`, removes it until it
  connects again.
- A worker's labels come from its configuration, so `disabled` does not apply
  to it. A worker is kept out of a selector by changing its labels.
- A worker ID that names a node of an inventory makes the worker no node, and
  the Nodes page shows the clash.

### Selecting nodes

A selector is the label filter that DAG lists take in `dagu history --labels`
and `GET /dags?labels=` (`internal/ir/label.go`). One parser serves both. It
matches the project's nodes: every inventory and every worker.

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
- A selector reads labels only. A DAG names roles, not hosts or inventories.
  An inventory's `defaults.labels`, such as `site=tokyo`, select all its
  nodes. RFC 013 narrows a run to named hosts.
- The matched list is sorted by name.

Two changes to the shared parser apply to DAG list filters too:

- `!env=dev` is read today as "has no label whose key is `env=dev`", and
  matches every DAG. It becomes "does not have `env=dev`".
- An invalid term, such as `env==prod`, today selects nothing, or everything
  when negated. It becomes an error.

### Import and export

`dagu nodes export` prints inventories as a JSON array of records without
their IDs. `dagu nodes import` reads the same array:

- An inventory whose name is new is created.
- An inventory whose name exists is refused, unless `--replace` is given,
  which replaces that inventory as a whole and keeps its ID.
- An import that breaks any rule, such as a node name that another inventory
  holds, imports nothing and lists every error. `--dry-run` only checks.
- An import is checked as a whole first, then written one inventory at a
  time under the project's lock. If the server stops partway, the
  inventories written so far stay. Running the same import again with
  `--replace` finishes it.

The Nodes page imports and exports the same JSON. Export and import move
inventories between servers and take bulk edits made in a text editor.

### Reading the inventory from a run

- A run on the server reads the project's inventories from the store. The
  server and coordinator keep them in memory and reload them after each
  change. Worker nodes come from the coordinator.
- A run on a worker calls a new coordinator RPC, `ResolveNodes`, with a
  selector or with exact node names. The answer holds the matched nodes, the
  nodes they use as bastions, and the project's trusted host keys for them.
- Resolution returns secret refs, never values. Each distinct ref resolves
  once per run, when the first step that needs it starts, and joins the
  run's masking list. A ref that does not resolve fails the items that use
  it, as RFC 013 defines.
- A worker fetches a value with `ResolveSecretReference`, setting a new
  `node` field in place of `name`. The coordinator serves it when the
  worker's lease is fresh, the attempt targets nodes, and a node of the
  project uses that ref.

### Host keys

Host keys apply to SSH nodes. Strict host key checking is on by default. A
host key is accepted when it matches the node's `host_key` or a key in the
project's trusted host keys. Trusted keys are stored by node name, not
address, so two nodes that share a private address behind different bastions
keep separate keys.

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
`ansible-inventory --list` into the JSON that `dagu nodes import` reads, once,
with a report. Every SSH node of either fits: its name, host, port, and user
become fields, its groups become labels with no value, its other attributes
become `key=value` labels, and its Key Storage paths become secret refs that
the report lists for creation. The tool's own RFC defines the mapping.
`dagu nodes import --dry-run` checks its output.

### Dagu Cloud sync

A server connected to Dagu Cloud ([RFC 002](002-dagu-cloud-connector.md)) can
keep each project's inventories there. It is off by default, and an admin of
the server turns it on, in the UI or in configuration:

```yaml
cloud:
  inventory_sync: false          # omit to choose in the UI
```

In Dagu Cloud, an inventory belongs to the workspace the server is connected
to, and to the project. Every server of that workspace that syncs the project
shares the project's inventories. RFC 018 of the Dagu Cloud repository
defines that side.

- **What travels:** each inventory record, by its ID, up to 4 MiB. Secret
  values, trusted host keys, and workers stay on each server.
- **Local first.** Runs read the local store and never Dagu Cloud.
- **Push and pull.** After a local change the server sends the changed
  inventory, and Dagu Cloud stores it as that inventory's next version. A
  deletion is sent as a version with no content. The server pulls newer
  versions at startup, every 10 minutes, and after its own push, and
  validates each before it replaces the local record.
- **The last change wins**, per inventory. Changes are not merged. Earlier
  versions are kept, and **History** restores one as a new version.
- **Turning it on.** A server with no inventories takes Dagu Cloud's, which
  is how a rebuilt server gets them back. When both hold inventories and they
  differ, nothing is copied until an admin chooses which side to keep.

### Surfaces

#### UI

A **Nodes** page in each project:

```text
┌ Nodes ───────────────────────────────────────────────────────────┐
│ Inventory [All ▾]  Labels [site=tokyo ×] [web ×]  Name ______    │
│                                             14 of 212 nodes      │
│                                                                  │
│ ☐ name         inventory         kind    host        labels      │
│ ☐ tokyo-web01  Tokyo production  ssh     10.0.1.11   web, ...    │
│ ☐ tokyo-web02  Tokyo production  ssh     10.0.1.12   web, ...    │
│ ☐ tokyo-gw     —                 worker  tokyo-gw    web, ...    │
│ ...                                                              │
│ [Add node] [Test connection] [Inventories] [Import] [Export]      │
└──────────────────────────────────────────────────────────────────┘
```

- The labels box is the label picker of the DAG list
  (`ui/src/components/ui/label-combobox.tsx`), with the project's labels as
  choices. The match count updates as the user types.
- **Add node** is a form for one host: inventory, name, host, user, labels,
  and key. It tests the connection and shows the host key to trust before it
  saves.
- **Test connection** checks the selected or matched nodes and shows each
  result as it arrives, with **Trust** next to unknown or changed keys.
- An SSH node's drawer shows its inventory, labels, connection settings with
  secret refs, and last test result. It edits the node's labels, disables
  it, and removes it. A worker's drawer shows its labels and when it was last
  seen, and forgets it when it is offline.
- **Inventories** lists the project's inventories with their node counts.
  It creates, renames, and deletes them, edits their defaults, and opens
  one as JSON with schema validation. An invalid change cannot be saved.

#### API

| Method and path | Purpose |
| --- | --- |
| `GET /nodes?labels=&inventory=&name=` | Matched nodes, one page at a time, with the total and selector errors. `inventory` limits them to one inventory, and `name` matches part of a node name |
| `GET /nodes/{name}` | One node |
| `DELETE /nodes/{name}` | Forget an offline worker |
| `GET /inventories`, `POST /inventories` | List the project's inventories, or create one |
| `GET /inventories/{id}`, `PUT /inventories/{id}`, `DELETE /inventories/{id}` | Read, replace, or delete one. `PUT` takes the version it read |
| `PUT /inventories/{id}/nodes/{name}`, `DELETE /inventories/{id}/nodes/{name}` | Add, change, or remove one node |
| `POST /inventory-imports` | Import inventories, with `replace` and `dryRun` |
| `GET /inventories/{id}/versions`, `POST /inventories/{id}/versions/{n}/restore` | With Dagu Cloud sync on, list versions and restore one |
| `GET /node-labels` | Label keys and values, for completion |
| `POST /node-connection-tests` | Test named nodes or a selector's matches |
| `POST /node-host-keys` | Trust host keys, given each node and the fingerprint the user saw |

Collection operations sit outside `/nodes/{name}`, so every valid node name
reaches its node. Once RFC 003 ships, every path takes `project`. Tests and
trusts answer with a server-sent event stream, one event per node. The
schemas are named `InventoryNode` and so on, since the API's `Node` schema is
a step's status.

#### CLI

```sh
dagu nodes add tokyo-web01 --inventory "Tokyo production (DC-1)" \
  --host 10.0.1.11 --labels web
dagu nodes remove tokyo-web01
dagu nodes forget tokyo-gw          # an offline worker
dagu nodes list --labels 'site=tokyo,web,!canary'
dagu nodes show tokyo-web01
dagu nodes test --labels web
dagu nodes export [--inventory "Tokyo production (DC-1)"] > inventories.json
dagu nodes import inventories.json [--replace] [--dry-run]
```

Each command works against a remote server like the other remote CLI
commands, and takes `--project` once RFC 003 ships. `--labels` is the flag
`dagu history` takes. `--inventory` takes a name or an ID, and `add` may
leave it out when the project has one inventory. A new inventory is created
in the UI or by import. `add` tests the connection and asks before it trusts
the host key.

#### MCP

`dagu_read` gains `target=nodes`, with `labels`, `inventory`, and paging,
`target=node`, with `name`, and `target=inventories`.

### Permissions

| Action | Role |
| --- | --- |
| List nodes and inventories, preview a selector, export | `viewer` |
| Test connection | `operator` |
| Create, change, or delete inventories and nodes, import, trust a host key, restore a version | The role that edits the project's base config |
| Forget a worker, turn Dagu Cloud sync on or off | `admin` |

Editing an inventory decides which hosts receive the project's SSH
credentials, so it takes the same role as editing base config. Workers are
server-wide, so forgetting one takes `admin`. Edits, imports, tests, and
trusted keys are written to the audit log.

The inventory is in the free tier.

## Security

- Inventories, exports, API responses, and MCP results hold secret refs,
  never values.
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
| A change breaks a rule, such as a node name that another inventory holds | The change is refused, naming the rule and the other inventory |
| A change is based on an older version of the inventory | The change is refused, and the UI shows the current inventory |
| Two changes add the same node name to different inventories at once | The first stored wins. The second is refused, naming the other inventory |
| An import breaks any rule | Nothing is imported, and every error is listed |
| The server stops partway through an import | The inventories written so far stay. The same import with `--replace` finishes it |
| A selector is invalid | The API, CLI, and UI report the term and the error. A step with that selector fails before any node starts |
| A worker node is offline | It is listed as `offline`, and RFC 013 fails a run that targets it |
| A worker's labels break the label rules | The worker does not start, and names the label |
| A worker ID names a node of an inventory | The worker is not a node. The Nodes page shows the clash |
| The coordinator is unreachable from a worker | Resolution fails, as secret resolution does |
| Dagu Cloud is unreachable with sync on | Runs and local edits are unaffected. The change is sent later, and the Nodes page shows the delay |
| A pulled version does not validate | It is not applied. The local record stays, and the Nodes page shows the error |

Failing a step that cannot see every node it selects is safer than running
it on part of the fleet.

## Compatibility and rollout

- Additive. An install without inventories behaves as before.
- `inventories/`, `host-keys/`, and `worker-nodes/` are new collections, as
  [Storage](#storage) lists. `cloud.inventory_sync` is a new setting in RFC
  002's `cloud` section.
- DAG list filters change as [Selecting nodes](#selecting-nodes) lists:
  `!key=value` excludes, and an invalid term is an error.
- Worker labels change as [Workers](#workers) lists. `worker_selector` and
  `--worker-label` match without case, a worker label with no value is kept,
  and a worker with an invalid label no longer starts.

Rollout:

1. **Credentials from secrets** for `ssh.run`, `sftp.*`, and DAG-level `ssh`
   (#3769).
2. **Inventory core**: the store, import and export, selectors and the parser
   changes, `GET /nodes`, `dagu nodes list|show|import|export`, and a
   read-only Nodes page. A new spec defines the record and selectors, with
   conformance tests through `dagu nodes import` and `dagu nodes list`.
3. **Host keys**: test connection and trust from the server.
4. **Editing**: the forms, the Inventories view and its JSON editor,
   `dagu nodes add|remove`, and the MCP targets.
5. **Workers**: worker nodes, the worker label changes, `ResolveNodes`, and
   the `node` field on `ResolveSecretReference`, with RFC 013.
6. **Dagu Cloud sync**, once Dagu Cloud accepts inventories.

## Verification and acceptance criteria

The sections above state the rules, and the new spec tests each one. This RFC
is accepted when the following hold. Item 5 needs a step on a node, so it is
checked with RFC 013's first rollout phase.

1. A selector matches the same nodes, across inventories, in
   `dagu nodes list`, `GET /nodes`, the Nodes page, and an RFC 013 step, and
   the same terms filter DAGs the same way.
2. Every case in [Failure behavior](#failure-behavior) ends as stated.
3. No secret value appears in any API response, export, log, or audit entry,
   and a literal credential fails validation.
4. With strict checking, no credential reaches a host whose key is unknown
   or has changed, and a key is trusted only by the fingerprint the user saw.
5. A run on a worker reaches a node with a key from a secret and a host key
   trusted in the UI.
6. `dagu nodes add` and the Add node form give a usable node in one step.
7. An inventory with any name is created, renamed, exported, and imported
   back with `--replace` unchanged, and no DAG that selects its nodes needs
   an edit.
8. Each role can do what [Permissions](#permissions) lists and nothing more.
9. With Dagu Cloud sync on, a rebuilt server gets its inventories back, two
   servers of one workspace share a project's inventories, and runs keep
   working while Dagu Cloud is unreachable.
10. A worker with a set ID is listed with its labels, stays listed while
    offline, and matches a selector as an SSH node with the same labels does.

## Alternatives considered

- **Workers only.** Supported, since workers are nodes. Rundeck and Ansible
  users chose agentless SSH, and installing an agent on hundreds of hosts is
  a migration of its own.
- **One inventory per project.** One set of defaults for every site, and one
  long list that every team edits.
- **Inventories as YAML files that people edit.** A file name would limit an
  inventory's name, a hand edit could leave a file invalid until a run reads
  it, and the UI would rewrite a file a person also edits. Export and import
  cover bulk edits.
- **Nodes in `base.yaml` or in DAG files.** Puts fleet data into the DAG
  schema and gives two DAGs different views of one fleet.
- **Inventories in the DAG directory, synced with the DAGs.** Every server
  that syncs the repository would get the same hosts, and a staging server
  would hold production's.
- **Dagu Cloud as the only place inventories live.** Nothing to keep on the
  server, but runs would stop whenever Dagu Cloud is unreachable.
- **Rundeck's filter language.** A second selector syntax, in which `,`
  means OR and `!` negates a clause, beside a label filter in which `,` means
  AND. Rundeck users convert their filters once instead.
- **Reading Rundeck and Ansible inventories live.** Keeps the foreign file as
  the truth, but needs merge rules, snapshots, and scripts on a timer. A
  one-time conversion suits a migration.

## Consequences

- DAGs and nodes are filtered with one syntax.
- DAGs select hosts by role, so adding a host means editing an inventory,
  not every DAG.
- SSH keys move off the Dagu host into the secret store.
- Rundeck and Ansible users convert their inventory once, and Dagu's
  inventories become the source of truth.
- "Node" now means an inventory entry in the UI and docs.
- With sync on, Dagu Cloud holds host lists for the first time.

## Future enhancements

These are left out to keep the first version small. Each fits on top of the
design above without changing it.

- **Live sources.** An inventory filled by a script or URL that prints nodes
  in Dagu's format, read on a timer, with built-in sources for EC2, Azure,
  GCP, and Kubernetes.
- **Selecting by name or host, and OR,** in a selector.
- **Tests and trust from a worker.** A host that only a worker reaches is
  tested by a run dispatched to that worker.
- **Routing SSH through a worker.** A `via` worker selector on a node opens
  its connection from a matching worker, for hosts in networks the server
  cannot reach.
- **More host key forms.** Several trusted keys per host for planned
  rotation, and a host CA, so rebuilt hosts need no new trust.
- **Limiting which nodes a user may target**, such as by inventory.
- **A server-wide inventory** shared by projects that opt in.
- **Recent runs per node** in the drawer, from an index of runs by node.
- **Other transports**, such as WinRM.
- **Renaming "Remote Nodes"** to "Remote servers" in the UI, so two pages are
  not both called nodes.
