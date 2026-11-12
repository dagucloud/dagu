# RFC 012: Node Inventory

- **Status:** Proposed
- **Date:** 2026-11-12
- **Affected areas:** a new `inventory` package, `persis/file`, `secret`,
  `runtime/builtin/ssh`, `service/frontend` (API, UI), `service/scheduler`,
  `service/coordinator`, `service/worker`, `service/mcp`, `cmd`, `api/v1`,
  `proto`, `ui`, and `docs`

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

RFCs 015 to 018, cited below, and the RFC for DAG-scoped grants are not
written yet. [#3777](https://github.com/dagucloud/dagu/issues/3777) lists
them and their order.

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

| Project | Inventory file | With `paths.inventory_dir` set | Snapshots and trusted host keys |
| --- | --- | --- | --- |
| Default project | `nodes.yaml` beside the global `base.yaml` | `<inventory_dir>/nodes.yaml` | `<data dir>/nodes/` |
| Project | `projects/<name>/nodes.yaml` | `<inventory_dir>/<name>/nodes.yaml` | `projects/<name>/data/nodes/` |

The inventory belongs to the server, not to the DAG files. A DAG is the same
in every environment and names a role, such as `tags: web`. Which hosts have
that role differs between staging and production. So `nodes.yaml` never
lives in a DAG directory, with or without `paths.projects_dags_dir`, and git
sync never reads or writes it. Two servers that sync the same DAG repository
each keep their own inventory.

`paths.inventory_dir` lets a team keep inventories in git and still give
each environment its own. A repository holds one directory per environment,
such as `inventories/prod/` and `inventories/staging/`, and each server's
`config.yaml` names the one it reads. DAGs move between environments
unchanged as long as the inventories use the same tags and attribute names.

A project without `nodes.yaml` has no nodes.

Workspaces do not divide the single inventory that exists before RFC 003:

| Concern | Before RFC 003 |
| --- | --- |
| Secret refs in the inventory | Resolve in the global scope only, whatever the workspace of the DAG that uses the node |
| Roles on the inventory | The caller's server-wide role. A user scoped to workspaces is a server-wide `viewer` |
| `project` in the API, `--project` in the CLI | Not accepted. Once RFC 003 ships, `default` names this inventory |

Before RFC 003, a DAG in any workspace therefore reaches every node with the
global credentials. An install that divides access by workspace should not
add a `nodes.yaml` until its projects exist. See [Security](#security).

RFC 003's migration moves each DAG labelled `workspace=X` into project X,
which has no `nodes.yaml`. A moved DAG that sets `nodes` or `node: required`
would then match no nodes. So the migration plan lists each such project as a
conflict, and `--apply` refuses until the project has an inventory.
`--copy-inventory` resolves the conflict. For each such project it copies:

- `nodes.yaml`, with the `file` sources and relative `command` paths it names,
- the source snapshots and the trusted host keys, and
- each global secret an inventory ref names, into the project's secrets.

A ref that the project already holds with another value stays a conflict,
since the moved DAG used the global value before.

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

- Node names match `^[A-Za-z0-9][A-Za-z0-9_.@:-]*$`, so hostnames, worker
  IDs, and names such as `app_server_01` are valid. `local` is reserved.
- Attribute names match `^[A-Za-z0-9][A-Za-z0-9_.-]*$`, the pattern of DAG
  label keys. `name`, `transport`, `source`, `nodename`, and `username` are
  reserved. An attribute with an invalid or reserved name is dropped, with a
  warning on the node and in its source status.
- Tags match `^[A-Za-z0-9][A-Za-z0-9_.:-]*$`, so Ansible groups such as
  `web_servers` are valid tags. An invalid tag is dropped with a warning, as
  an invalid attribute is.
- `hostname` is a DNS name, an IPv4 address, or a bracketed IPv6 address,
  with an optional `:port`. `user` matches `^[A-Za-z0-9_][A-Za-z0-9._-]*$`.
  Built-in attributes are therefore safe to insert into a command unquoted.
- After merging, a node with transport `ssh` must have a `hostname`.
- A node that breaks any other rule is still listed and still matched by
  filters. It carries a problem message, and RFC 013 fails any step item that
  targets it. Leaving it out would make a filter match fewer hosts without
  saying why.

#### SSH settings

| Field | Meaning |
| --- | --- |
| `key` | Private key: `{ secret: <ref> }`, `{ file: <path> }`, or a path string |
| `password` | Password: `{ secret: <ref> }` only |
| `passphrase` | Passphrase for an encrypted key: `{ secret: <ref> }` only |
| `bastion` | Name of another node to connect through, one hop only. A missing bastion, or one with its own `bastion`, is a problem on the node |
| `host_key` | Pinned public key, such as `ssh-ed25519 AAAA...` |
| `strict_host_key` | Default `true` |
| `known_host_file` | Path of an extra known-hosts file, as in the DAG-level `ssh` field |
| `via` | A worker selector. The connection opens from a worker that matches it, as [RFC 014](014-site-relay.md) defines |
| `shell` | Remote shell, as `ssh.run`'s `shell` |
| `timeout` | Connect timeout for runs. Default `30s` |

`{ secret: <ref> }` names a Dagu-managed entry in the project's secret
registry, the only kind of entry a registry ref resolves to. A key kept in
Vault or another provider is copied into the registry. See
[Open questions](#open-questions). Literal passwords and literal credential
headers are rejected, so `nodes.yaml` is safe to commit.

A `file` key, or a path string, names a file on the host that opens the
connection: the server for a local run or a test from the server, the worker
for a run or a test there. The path is absolute or starts with `~`, the home
of the user that runs Dagu on that host. A relative path is a problem on the
node. A file missing on the connecting host fails the node item or the test,
naming the path and the host.

The same credential form is added to `ssh.run`, `sftp.upload`,
`sftp.download`, and the DAG-level `ssh` field by a change to
[Spec 042](../specs/042-ssh-run.md), which lands before this RFC.

#### Merging

A node can come from several places: a source, the workers list, and the
`nodes` block. Dagu merges them by name in this order, later winning:

```text
sources, in listed order ─▶ workers ─▶ nodes block in nodes.yaml
```

- Each field is taken from the last place that sets it on the node itself.
- `tags` are the union of every place.
- `ssh` merges field by field, except that `key` and `passphrase` merge as
  one unit, as [#3769](https://github.com/dagucloud/dagu/issues/3769)
  defines for base config: a place that sets `key` drops a `passphrase` set
  before it.
- `defaults` apply after merging, only to fields that no place set: first
  the `defaults` of the sources that list the node, later winning, then the
  file's `defaults`. A node whose own places set `key` takes no `passphrase`
  from `defaults`. Defaults do not apply to worker nodes.
- Only the refs the node's effective auth method uses are resolved, as in
  #3769: a `key` leaves `password` unused, and a `password` without `key`
  leaves `passphrase` unused.
- `transport` never changes in a merge. A worker whose ID is the name of a
  node from a source, or of a `nodes` entry that sets `hostname` or `ssh`,
  conflicts with it. The node is listed with a problem naming both, so RFC 013
  fails items that target it instead of choosing a transport.
- A `nodes` entry that sets neither `hostname` nor `ssh` adds tags and
  attributes to the worker of the same name. While that worker is not
  connected, the entry is listed with a problem.

The `nodes` block goes last, so a short entry in `nodes.yaml` can add tags or
credentials to a node that a script discovers, and keeps the user and key the
script set:

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
| `url` | HTTPS or HTTP URL, fetched with `GET`. An HTTP URL with a `{ secret: <ref> }` header is a validation error |
| `format` | `dagu` (default), `rundeck`, or `ansible` |
| `refresh` | For `command` and `url`. Default `15m`, minimum `30s` |
| `timeout` | For `command` and `url`. Default `60s` |
| `headers` | For `url`. Values are strings or `{ secret: <ref> }`. A header whose name contains `auth`, `token`, `key`, `secret`, or `cookie`, in any case, takes `{ secret: <ref> }` only, as `Authorization` and `X-Rundeck-Auth-Token` do. The match is broad on purpose: a harmless header it catches, such as `X-Author`, costs one registry entry |
| `defaults` | Node fields for this source's nodes |
| `required` | Default `true`. See [Failure behavior](#failure-behavior) |

Exactly one of `file`, `command`, or `url` is set.

- **`file`** is read when the inventory is read, and cached until the file
  changes.
- **`command`** runs in the directory of `nodes.yaml`, as the user that runs
  Dagu, on the host that refreshes it: the active scheduler's on the timer,
  the server's or the CLI's on request. Where these are different hosts, each
  needs the command and whatever it reads. The source status names the host
  of the last refresh. Its environment is the one a step process starts
  from, so it holds none of Dagu's own credentials
  ([Spec 006](../specs/006-value-resolution-env.md)). A non-zero exit is an
  error. Its stdout is parsed. Its stderr is kept in the source status,
  truncated and masked.
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

The mapping can give two storage paths one ref: `keys/prod/Web_01.pem` and
`keys/prod/web-01.pem` both become `prod/web-01-pem`. Each node that uses
either path is then listed with a problem naming both, since one ref cannot
hold two keys.

#### Ansible mapping

| Ansible | Dagu |
| --- | --- |
| Host name | `name` |
| `ansible_host`, else `ansible_ssh_host`, else the host name, as Ansible connects | `hostname` |
| `ansible_port`, else `ansible_ssh_port` | `port` |
| `ansible_user`, else `ansible_ssh_user` | `user` |
| `ansible_ssh_private_key_file` | `ssh.key` as `{ file: <path> }` |
| `ansible_connection` unset, `ssh`, `smart`, or `paramiko` | `transport: ssh` |
| `ansible_connection` set to anything else, such as `local` or `winrm` | The node is listed with a problem |
| Every group containing the host, directly or through children, except `all` and `ungrouped` | `tags` |
| Other scalar host variables | Custom attributes |
| Non-scalar variables | Dropped, with a warning in the source status |
| `ansible_password`, `ansible_ssh_pass`, `ansible_become_password` | Not read, with a warning. Use a secret |

### Workers

With `workers: true`, every worker connected to the coordinator is a node:

| Node field | Value |
| --- | --- |
| `name` | The worker ID |
| `transport` | `worker` |
| `hostname` | The host the worker reports in its heartbeat |
| Attributes | The worker's labels. A label whose name is invalid or reserved is dropped, with a warning |

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
| `name: web01,web02` | `name` matches `web01` or `web02` |
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
- A value is a comma-separated list. Each item matches when it equals the
  attribute value or, as an RE2 regular expression, matches the whole value,
  as in Rundeck. Tag terms are exact.
- An item that uses a Java regex construct RE2 lacks is a filter error, with
  its position and a hint: lookaround (`(?=`, `(?!`, `(?<=`, `(?<!`), an
  atomic group (`(?>`), a backreference such as `\1`, or a possessive
  quantifier such as `*+`. Rundeck accepts these, and comparing one for
  equality would make `!name: (?!web).*` exclude nothing. Any other item that
  does not compile is compared for equality only in an include clause, as
  Rundeck does, with a warning that gives its position. In an exclude clause
  it is a filter error, since an exclusion that matches nothing widens the
  selection without saying so.
- Clauses with the same attribute and the same sign join into one list:
  `name: a name: b` is `name: a,b`. Since each item matches on its own,
  `web.* db01` selects every `web` node and `db01`.
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
                    ┌─────────────────────────────────┐
nodes.yaml ────────▶│                                 │
file sources ──────▶│ resolve(project, filter|names)  │──▶ nodes with SSH settings
snapshots ─────────▶│                                 │    (secret refs, not values)
workers list ──────▶│                                 │
                    └─────────────────────────────────┘
         local run: reads the project directory
         run on a worker: asks the coordinator (ResolveNodes RPC)
```

- A run on the server host reads `nodes.yaml`, file sources, and snapshots
  from the project directory.
- The server, scheduler, and coordinator keep the merged inventory in memory
  and match filters against it. They rebuild it when `nodes.yaml`, a `file`
  source, or a snapshot changes, or when connected workers or their labels
  change, so 1,000 bound runs calling `ResolveNodes` parse it once.
- A run on a worker cannot read those files. It calls a new coordinator RPC,
  `ResolveNodes`, with a filter. The response holds the matched nodes, the
  nodes they use as bastions, and the project's trusted host keys for those
  nodes, so a key trusted in the UI also holds on workers.
- Resolution also takes exact node names, at most 10,000, in place of a
  filter. A bound run (RFC 013) and a `node-test` run use them, since
  `name: web01.prod` is a regex that also matches `web01Xprod`, and a name
  list is not bound by the 4 KiB filter limit. Only the named nodes, and
  their bastions, are returned.
- Resolution returns secret refs, never secret values. Each distinct ref
  resolves once per run, when the first step that needs it starts, after
  matching. Every node item and connection reuses the value in memory, and
  the value joins the run's masking list. This is the rule
  [#3769](https://github.com/dagucloud/dagu/issues/3769) sets for refs known
  only during a run, so 1,000 nodes that share `ssh/deploy` cost one
  resolution. A ref that does not resolve fails the node items that use it,
  as RFC 013 defines, and is not tried again in that attempt.
- Inventory secret refs resolve in the inventory's scope: global before
  RFC 003, the run's project after. The workspace of the DAG that uses the
  node is never consulted.
- A worker fetches a value with `ResolveSecretReference`, setting a new
  `node` field in place of `name`. For such a request the coordinator picks
  the scope itself, ignores the request's `workspace`, and skips the checks
  that tie env-var requests to the DAG's workspace and `secrets:`.
- Runs never execute a `command` source or fetch a `url` source. They read
  the latest snapshot.

The coordinator authorizes both RPCs with the lease check that
`ResolveSecretReference` uses today: the worker's lease must be fresh and
match the attempt. `ResolveNodes` also requires the attempt to target nodes,
through a step with `nodes` or a run bound to a node by RFC 013. Each
`ResolveNodes` call adds the distinct refs it returned, with their scope, to a
record for the attempt, and a `node` request is served only for a ref in that
record. The record lives in its own file-backed collection keyed by attempt
key, so every coordinator instance reads it. It is written once per
`ResolveNodes` call, never by heartbeats, and deleted with the lease. The
lease record does not grow. A fetch never reads the inventory again, so a
source refresh during a step does not deny it. A user who can start such a
run can already bind it to any node its `node.filter` allows (RFC 013), so
the check adds no narrower limit.

Limiting targets is the later access work named in [Non-goals](#non-goals).

### Snapshots

The active scheduler refreshes each `command` and `url` source:

- at startup, unless its snapshot is younger than `refresh`,
- every `refresh`, and
- when `nodes.yaml` changes.

A user can also ask for a refresh. The server runs it, and `dagu nodes
refresh` runs it in the CLI process when no remote server is set. A file lock
lets one process refresh a source at a time. A refresh that waited for the
lock is skipped when the snapshot was taken after the request.

Without a scheduler, such as with `dagu start` alone, sources refresh only
when asked.

A snapshot holds the source's nodes, the time it was taken, and the error of
the last attempt, if any. A failed refresh keeps the previous nodes.

Snapshots hold secret refs only. They are written with the permissions of
other files in the data directory.

### Host keys

Strict host key checking stays on by default. A node's host key is accepted
when it matches, in order:

1. the node's `ssh.host_key`,
2. the project's trusted host keys in `<data dir>/nodes/`,
3. the node's `ssh.known_host_file`, or `~/.ssh/known_hosts`.

Trusted keys are stored by node name, not address. Two nodes that share a
private address behind different bastions keep separate keys.

**Test connection** checks the host key, authenticates, runs `true`, and
reports the host key fingerprint, the time taken, and any error. When a key
is unknown or has changed, the test stops after key exchange, before any
credential is sent, and the result names the hop that presented it: the
bastion or the node. A test caps each hop's connect timeout at 10s. One
request tests up to 500 nodes, named or matched by a filter, with at most 16
connections at once. The API rejects a larger set and returns its count; the
UI and CLI split it into 500-node requests and show one combined result. The
response streams each node's result as it finishes, and the server extends
the write deadline after each one, so a request with many unreachable hosts
still returns every result.

**Trust** adds keys to the project's trusted host keys. The request names each
node or bastion and the fingerprint the user saw. The trust runs where the
test ran and reads each key again, and the server stores it under that node
only when the fingerprints match. Otherwise it rejects that node and reports
the fingerprint it found. Nothing is kept between a test and a trust. One
request trusts many nodes, with the same limits and streamed results, so new
hosts from an import take one confirmation. Hosts behind a new bastion take
two: the bastion, then the hosts its connection now reaches. Replacing a
changed key takes the same role, and the UI shows the old and new
fingerprints and asks first. Test connection never trusts a key by itself.

Tests and trusts run from the server by default. A host that only a worker
reaches, such as one in a private network, is tested and trusted from a
worker once RFC 013 runs node items there. The server then starts a one-step
run of the DAG `node-test`, dispatched with `target_worker_id` or
`worker_selector`, whose step targets the tested nodes by exact name. Its
attempt targets nodes, so the lease check, `ResolveNodes`, the attempt's ref
record, and status reporting apply unchanged. Each node item runs the test,
or for a trust only reads the key, and records the result in its item
status. The server streams results from that status and stores each trusted
key whose fingerprint matches. A `node-test` run sends no notifications,
runs no handlers, cannot be retried, and is pruned like other runs. A new
test starts a new run. Until RFC 013 runs node items on workers, such a host
needs `ssh.host_key` or a `known_host_file` on the worker.

`node-test` is a system DAG name: Dagu runs it for itself in each project.
RFC 017's `adhoc` is another. System names are reserved:

- `dagu validate` gives a notice for a user DAG with a reserved name. The DAG
  still loads and runs.
- While such a DAG exists in a project, the system run is refused there,
  with an error that names the DAG to rename. A system run therefore never
  shares run history, per-DAG settings, or retention with a user DAG.
- A selector grant cannot name a system DAG
  ([#3772](https://github.com/dagucloud/dagu/issues/3772)).

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

- The filter box completes attribute names, values, and tags from
  `GET /node-attributes`, explains syntax errors in place, and updates the
  match count once the user stops typing. The table loads one page at a time.
- **Copy filter** puts the current filter on the clipboard for a DAG.
- Selecting nodes and **Test connection** checks them in one request, from
  the server or a chosen worker, and shows each result as it arrives, with
  **Trust** next to unknown or changed host keys, for one node or every
  selected node. **Test all matches** tests the whole match of the current
  filter, and **Trust all unknown keys** sends every unknown key's
  fingerprint from that test's results.
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
| `GET /nodes?filter=` | Matched nodes, one page at a time with `page` and `perPage`, plus the total count and filter errors |
| `GET /nodes/{name}` | One node |
| `GET /node-attributes` | Attribute names and tags, each with its most common values and their counts, capped, for completion |
| `POST /node-connection-tests` | Test connection to named nodes or a filter's matches, from the server, a worker ID, or a `worker_selector` |
| `POST /node-host-keys` | Trust host keys, given each node and the fingerprint the user saw, with the same origin choice |
| `GET /node-sources` | Each source's status: name, kind, node count, last refresh, and error |
| `POST /node-sources/refresh` | Refresh every source, or one with `?source=` |
| `GET /node-inventory`, `PUT /node-inventory` | Read and write `nodes.yaml` |

Collection operations sit outside `/nodes/{name}`, so every valid node name
reaches its node. Once RFC 003 ships, every path takes `project`. Tests and
trusts answer with a server-sent event stream, one event per node, and each
test event has the fields of the existing
`/remote-nodes/{id}/test-connection` result.

#### CLI

```sh
dagu nodes list 'tags: web'         # matched nodes
dagu nodes show web01
dagu nodes test 'tags: web'         # streams results, in 500-node requests
dagu nodes test 'tags: dmz' --worker-selector zone=dmz
dagu nodes refresh [--source ec2]
dagu nodes validate [nodes.yaml]
dagu nodes list 'tags: web' --inventory ./out/nodes.yaml --output json
```

Each command works against a remote server like the other remote CLI
commands, and takes `--project` once RFC 003 ships.

`list` and `validate` take `--output json`, which prints nodes with secret
refs, as the API does, and filter errors with their positions. `list` also
takes `--inventory <file>`, which reads that file and its `file` sources
instead of a project's inventory, with no server, snapshots, or workers, and
reports `command` and `url` sources as not read. The Rundeck import tool
(RFC 018) checks resource files and filters this way, so it uses Dagu's
parser and filter language without copying them.

#### MCP

`dagu_read` gains `target=nodes` and `target=node`. `nodes` takes a new
`filter` field holding the filter as plain text, with `limit` and `cursor` for
paging. `node` takes `name`. Both forbid `query`, so a `+` in a filter is
never decoded into a space.

### Permissions

| Action | Role |
| --- | --- |
| List nodes, preview a filter, view source status | `viewer` |
| Test connection, refresh sources | `operator` |
| Read `nodes.yaml` | The role that reads the project's base config |
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
- Secret values are fetched once per run for the connections that need them,
  are kept in memory, and are masked in every log and status the run writes.
- `command` sources run as the Dagu user, without Dagu's own credentials in
  their environment. Editing the inventory is restricted to the role that
  edits base config.
- `url` sources verify TLS. A header whose name contains `auth`, `token`,
  `key`, `secret`, or `cookie` takes secret refs only, and a secret ref is
  never sent over plain HTTP. Source status never shows a source's command
  or headers.
- Before RFC 003, the inventory and its credentials are global. Any user who
  can write or run a DAG that targets nodes, in any workspace, reaches every
  node with those credentials. Workspaces do not limit this. Projects do,
  once RFC 003 ships.
- Attribute values from scripts, URLs, and Ansible are untrusted text. RFC 013
  inserts them into commands only where the author writes `${node.*}`, and
  its Security section covers quoting.
- Host keys are checked by default. Only a user with the trust role can add a
  key, and only the key whose fingerprint that user saw.
- With strict checking on, no credential is sent to a host whose key is
  unknown or has changed, even by Test connection.

## Failure behavior

| Event | Behavior |
| --- | --- |
| `nodes.yaml` is invalid | Resolution fails with the file, line, and error. The UI refuses to save an invalid file |
| A required `file` source is missing or invalid | Resolution fails with the source name and the error |
| A required `command` or `url` source has never succeeded | Resolution fails with the source name and its last error. When no refresh has run, the error names `dagu nodes refresh` |
| A `command` or `url` source fails after an earlier success | Its last good nodes are used. Resolution returns a warning naming the source and the age of its snapshot |
| A source of any kind with `required: false` fails and has no earlier success to use | Its nodes are left out, with a warning |
| A node breaks a rule, such as a missing hostname | It is listed and matched, with a problem message |
| A filter does not parse, uses a regex construct RE2 lacks, or excludes by an item that does not compile | The API, CLI, and UI report the position and the error |
| The coordinator is unreachable from a worker | Resolution fails, as secret resolution does |
| No connected worker matches a test's worker ID or `worker_selector` | The test fails at once, naming them |

Failing a step that cannot see its whole inventory is safer than running it on
part of the fleet.

## Compatibility and rollout

- Additive. An install without `nodes.yaml` behaves as before.
- `nodes.yaml` is not read from a DAG directory, so no DAG file name is
  reserved. `paths.inventory_dir` is a new setting.
- RFC 003's migration must handle workspace DAGs that target nodes, as
  [Where the inventory lives](#where-the-inventory-lives) says. That work,
  with acceptance item 24, is part of RFC 003 and blocks v3.0.
  [#3777](https://github.com/dagucloud/dagu/issues/3777) tracks it.
- Node name `local` is reserved for RFC 013.
- The UI and docs say "remote servers" for remote nodes. Nothing in
  configuration or the API is renamed.

Rollout:

1. **Credentials from secrets** for `ssh.run`, `sftp.*`, and DAG-level `ssh`,
   with Spec 042 updated.
2. **Inventory core**: `nodes.yaml` with `defaults` and `nodes`,
   `paths.inventory_dir`, the filter
   language, `GET /nodes`, `GET /node-attributes`,
   `dagu nodes list|show|validate`, with `--output json` and `list
   --inventory`, the reserved system DAG names, and a read-only Nodes page.
   A new spec
   defines the file format and the filter language, with conformance tests
   through `dagu nodes list`.
3. **Sources**: `file`, `command`, and `url`, the three formats, snapshots
   refreshed by the active scheduler, and source status.
4. **Workers** as nodes, and the `ResolveNodes` RPC with node secret refs,
   the `node` field on `ResolveSecretReference`, and trusted host keys.
5. **Host keys**: streamed bulk test connection and trust by fingerprint
   from the server, and the project's trusted host keys by node name.
6. **Editing**: the `nodes.yaml` editor, refresh, and the MCP targets.
7. **Rename** remote nodes to remote servers in the UI and docs.
8. **Tests from a worker**: the `node-test` run, and the worker origin in the
   UI, API, and `dagu nodes test --worker-selector`, once RFC 013 runs node
   items on workers.

## Verification and acceptance criteria

Items 14, 16, and 22 connect to a node from a step, so they are checked with
RFC 013's first rollout phase. Item 23 needs its worker nodes.

1. The three Rundeck examples in [Filters](#filters) select the stated nodes.
2. `tags: a+b,x` selects nodes with both `a` and `b`, and nodes with `x`.
3. `name: web01,web02` matches those two names, `name: web0.` matches as a
   regular expression against the whole name, and `web.* db01` selects every
   node whose name starts with `web`, and `db01`. `!name: (?!web).*` is
   rejected with its position. `name: *.example` is compared for equality,
   with a warning, and `!name: *.example` is rejected.
4. An empty filter is an error, and `.*` selects every node.
5. A Rundeck resource document in YAML map form, YAML list form, JSON, and XML
   loads into the same nodes, and `ssh-key-storage-path: keys/prod/Web_01.pem`
   becomes `{ secret: prod/web-01-pem }`. Two nodes whose storage paths map
   to one ref are each listed with a problem naming both paths.
6. `ansible-inventory --list` output gives each host the tags of its groups and
   their parent groups, and its scalar host variables as attributes. A group
   `web_servers` is a tag that filters match. A host without `ansible_host`
   connects to its host name. A host with `ansible_connection: local` is
   listed with a problem.
7. The merge order and tag union hold for a node listed by a source and by the
   `nodes` block. A tag-only entry in the `nodes` block keeps the source's
   `user` and `ssh.key`.
8. A `command` source that fails after a success keeps its nodes and reports a
   warning. A required source that never succeeded fails resolution. A
   missing `file` source with `required: false` leaves its nodes out with a
   warning.
9. With `workers: true`, a worker appears as a node while connected and
   disappears when its heartbeats stop.
10. Test connection reports a fingerprint for an unknown key, names the hop
    that presented it, and sends no credential. Trust with that fingerprint
    makes a strict connection succeed. Trust with a different fingerprint is
    rejected. A changed host key fails. Two nodes with the same address
    behind different bastions keep separate trusted keys.
11. No secret value appears in any API response, snapshot, log, or audit
    entry. A literal `password`, `Authorization` header, or
    `X-Rundeck-Auth-Token` header fails validation, as does an HTTP `url`
    source with a secret header.
12. `dagu nodes list`, `GET /nodes`, and the Nodes page give the same match
    for the same filter.
13. A `viewer` can list nodes and cannot test, refresh, trust, edit, or read
    `nodes.yaml`.
14. A DAG dispatched to a worker connects to an inventory node with a key from
    a secret and a host key trusted in the UI. A node with a `file` key
    reads it on the worker, and fails there, naming the path and the worker,
    when the file is missing. The coordinator denies a ref
    that no `ResolveNodes` response for that attempt returned. A ref returned
    by one coordinator instance is served by another. `ResolveNodes` with the
    name `web01.prod` returns only that node while `web01Xprod` exists.
15. While a worker whose ID is an SSH node's name is connected, that node is
    listed with a problem, and its transport stays `ssh`.
16. Before RFC 003, a DAG in a workspace that has a secret with the same ref
    as an inventory node connects with the global secret, locally and on a
    worker.
17. With only `dagu scheduler` and `dagu coordinator` running, sources
    refresh on time. With two servers and a standby scheduler, a source runs
    once per refresh interval.
18. A `command` source that runs `env` shows none of Dagu's own credentials.
19. With 5,000 nodes, `GET /nodes` returns one page and the total count. A
    test of 500 nodes, 50 of them unreachable, opens at most 16 connections
    at once and streams every result. A test request for 501 nodes is
    rejected with the count, and `dagu nodes test` on 2,000 matches reports
    all 2,000.
20. A worker label named `gpu.type` is an attribute that filters can match.
    An attribute with an invalid name is dropped with a warning, and the node
    stays targetable.
21. `dagu_read target=nodes` with `filter: "tags: web+prod"` matches nodes
    with both tags. A node named `sources` is reachable at `GET /nodes/{name}`.
22. A step on 500 nodes that share one key ref resolves it once. On a worker,
    it sends one `ResolveSecretReference`.
23. A node that only a worker reaches passes a `node-test` run from that
    worker, and a trust from that worker makes runs there connect strictly.
    A failed `node-test` run sends no notification. While a user DAG named
    `node-test` exists in the project, a test from a worker is refused,
    naming that DAG.
24. With RFC 003, migrating a v2 install with a workspace DAG that targets
    nodes refuses until the project has an inventory. After
    `--copy-inventory`, the DAG reaches the same nodes with the same keys and
    host keys.
25. Two servers sync the same DAG repository and set different
    `paths.inventory_dir`. The same DAG runs on each server's own nodes. A
    `nodes.yaml` in a DAG directory is not read as an inventory, and git
    sync never writes one.

## Alternatives considered

- **Install a worker on every host.** Works today with worker labels and
  `worker_selector`, and stays supported through `workers: true`. Rundeck and
  Ansible users chose agentless SSH, and installing an agent on hundreds of
  hosts is a migration of its own.
- **Declare nodes in `base.yaml` or in DAG files.** Puts fleet data into the
  DAG schema, makes `command` sources run while DAGs load, and gives two DAGs
  different views of one fleet.
- **`nodes.yaml` in the DAG directory, synced with the DAGs.** One review
  path for DAGs and hosts, but every server that syncs the repository would
  get the same hosts, and a staging server would hold production's.
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
- The active scheduler runs inventory scripts on a timer, which is new
  background work. It is bounded by `refresh`, `timeout`, and the output
  limit.
- "Node" now means an inventory entry in the UI and docs.

## Open questions

Questions 6, 8, and 9 change the file format. They are settled before the
spec of rollout phase 2 is written.

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
9. Should `key`, `password`, and `passphrase` also accept Spec 069's direct
   `provider` and `key` form, so keys can stay in Vault or AWS?
