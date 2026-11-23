# Spec: Node Inventory

## Status

Partially implemented. The inventory record, its rules, selectors, import,
export, and changes to one node are implemented through `dagu nodes list`,
`show`, `add`, `remove`, `import`, and `export`, and conformance covers them
through those commands. Testing connections and trusting host keys are
implemented over the API, and conformance covers them through it. Worker
nodes and the `dagu nodes test` command are not implemented yet, and further
sections are added as they land ([RFC 012](../rfcs/012-node-inventory.md)).

## Scope

This spec defines the inventory record, the rules every stored inventory
meets, how a selector matches nodes, and how inventories are imported and
exported, and how the server tests a node's connection and trusts its host
key. It does not define how a step runs on its nodes
([Spec 089](089-node-targeting.md)), worker nodes, or Dagu Cloud sync.

## Goal

The hosts a workflow acts on are kept in records that are always valid,
selected by role with the label filter of [Spec 087](087-label-filters.md),
and moved between installations as JSON without changing on the way or
carrying a secret value.

## Behavior

### Inventories

An inventory is a named list of SSH hosts, the nodes, with defaults they
share. Dagu stores each inventory as one JSON record:

```json
{
  "name": "Tokyo production",
  "description": "Racks A and B",
  "defaults": {
    "user": "deploy",
    "key": { "secret": "ssh/tokyo" },
    "labels": ["site=tokyo", "env=prod"]
  },
  "nodes": [
    { "name": "tokyo-web01", "host": "10.0.1.11", "labels": ["web"] },
    { "name": "tokyo-web02", "host": "10.0.1.12", "labels": ["web", "canary"] },
    { "name": "tokyo-db01", "host": "db01.internal", "port": 2222, "bastion": "tokyo-jump", "labels": ["db"] },
    { "name": "tokyo-jump", "host": "jump.tokyo.example.com" }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `id` | Set by Dagu when the inventory is created: `inv_` and 10 lowercase letters or digits. It never changes |
| `name` | Display name, any text of 1 to 200 characters that is not blank. Unique among the installation's inventories, compared without case |
| `description` | Text |
| `defaults` | The node fields from `port` through `timeout`, and `labels`, applied to the inventory's nodes |
| `nodes` | The inventory's nodes |

A record with a field not listed here or under [Nodes](#nodes) is an error.

### Nodes

| Field | Meaning |
| --- | --- |
| `name` | Node name. Required |
| `host` | Host name or IP address. Required |
| `port` | SSH port, 1 to 65535. Default `22` |
| `user` | SSH user |
| `key` | Private key: a path, `{ "file": <path> }`, or `{ "secret": <ref> }` |
| `password` | Password: `{ "secret": <ref> }` only |
| `passphrase` | Passphrase of an encrypted key: `{ "secret": <ref> }` only |
| `bastion` | Name of another node of the same inventory to connect through |
| `host_key` | Pinned public key, in `authorized_keys` form such as `ssh-ed25519 AAAA…` |
| `strict_host_key` | Default `true` |
| `shell` | Remote shell |
| `timeout` | Connect timeout, a positive duration such as `10s` or `1m30s`. Default `30s` |
| `labels` | Labels, as `key` or `key=value` |
| `description` | Text |
| `disabled` | `true` keeps runs off the node. Default `false` |

Rules:

- A node name is a letter or digit followed by letters, digits, `-`, `_`, or
  `.`, at most 200 characters. Node names are unique across all of the
  installation's inventories, compared without case.
- `host` is a DNS name, an IPv4 address, or an IPv6 address without a zone.
- `user` is a letter, digit, or `_` followed by letters, digits, `.`, `_`,
  or `-`.
- Labels follow the rules of DAG labels. Their keys and values are
  lowercased.
- `password` and `passphrase` given as text are errors, so an inventory
  never holds a secret value.
- A bastion names a node of the same inventory exactly as written, and a
  node is never its own bastion. A bastion connects directly, so a node that
  connects through a bastion, its own or the default one, cannot be one.
- A disabled node is listed and matched like any other.

A default fills a field that a node leaves unset. `defaults.labels` are
added to each node's labels, and a node's label replaces a default label
with the same key. A node that sets `key` takes no `passphrase` from
`defaults`, since a passphrase belongs to the key it unlocks. The default
bastion does not apply to the node it names.

### Selecting nodes

A selector is a label filter of [Spec 087](087-label-filters.md): terms
separated by commas, each of which a node's labels, with defaults applied,
must match. A selector without terms is an error. Matched nodes are in name
order, comparing bytes.

### Commands

| Command | Does |
| --- | --- |
| `dagu nodes list [--labels <selector>] [--inventory <name or id>] [--format table\|json]` | Prints the nodes the selector matches, or every node without `--labels` |
| `dagu nodes show <name> [--format table\|json]` | Prints one node. The name is compared without case |
| `dagu nodes add <name> --host <host> [--inventory <name or id>] [flags]` | Adds a node, or replaces the node of that name, as [Changing inventories](#changing-inventories) defines |
| `dagu nodes remove <name>` | Removes a node from the inventory that holds it |
| `dagu nodes import <file> [--replace] [--dry-run]` | Imports a JSON array of inventories, as [Import](#import) defines |
| `dagu nodes export [--inventory <name or id>]` | Prints inventories, as [Export](#export) defines |

- `--inventory` names an inventory by its ID, or else by its name compared
  without case, and limits the command to that inventory.
- The table of `list` has the columns `NAME`, `INVENTORY`, `HOST`, `LABELS`,
  and `STATUS`, which is `enabled` or `disabled`. Labels are sorted and
  separated by commas.
- `--format json` prints each node as an object with its fields, with
  defaults applied: `name`, `inventory` with its `id` and `name`, `host`,
  `port`, `user`, `key`, `password`, `passphrase`, `bastion`, `host_key`,
  `strict_host_key`, `shell`, `timeout`, sorted `labels`, `description`, and
  `disabled`. A field that is not set is left out, except `port`,
  `strict_host_key`, `timeout`, `labels`, and `disabled`. `list` prints an
  array of them.
- Credentials are printed as `{ "secret": <ref> }` or `{ "file": <path> }`,
  never as values.
- The commands work on the local installation only.

### Import

`dagu nodes import` reads a JSON array of inventory records.

- An inventory whose name is new is created, and Dagu gives it an ID.
- An inventory whose name exists, compared without case, is refused unless
  `--replace` is given, which replaces that inventory as a whole and keeps
  its ID.
- A record that sets `id` is refused, since Dagu assigns IDs.
- The import is checked as a whole before anything is stored. When any
  inventory breaks a rule, nothing is stored. A refused inventory is checked
  on its own, so its own broken rules are listed too.
- `--dry-run` checks the import and stores nothing.
- Inventories are then stored one at a time. When the import stops partway,
  the inventories stored so far stay, and the same import with `--replace`
  finishes it.

It prints one line per inventory, in import order, created ones first:
`created <name>` or `replaced <name>`, or with `--dry-run`, `would create
<name>` or `would replace <name>`.

### Export

`dagu nodes export` prints the inventories as a JSON array in name order,
compared without case. The records leave out `id`, so the output imports
into any installation. Importing it into an installation without
inventories, and exporting again, prints the same JSON.

### Changing inventories

A change is checked against every rule and the other inventories, and a
change that breaks one stores nothing. Each change names the version of the
inventory it is based on. A change based on an older version is refused,
and over the API the refusal carries the inventory as it is now.

A node's trusted host key belongs to its name. A change that removes or
renames a node forgets the key trusted for that name, and a node that is
added starts with none, so a host never inherits the key a user trusted for
another host. A node that keeps its name keeps its key. An import and
`dagu nodes remove` follow the same rule.

**`dagu nodes add`** builds a node from its flags: `--host`, `--port`,
`--user`, `--key` (a path) or `--key-secret`, `--password-secret`,
`--passphrase-secret`, `--bastion`, `--labels` (comma-separated), and
`--description`. `--inventory` may be left out when there is one
inventory. Before it saves, `add` tests the connection as
[Host keys](#host-keys) defines.

- A node that passes is saved, and `add` prints `added <name> in
  <inventory>`, or `replaced` for a node of that name.
- When a hop's host key is unknown, `add` prints `The host key of <hop>
  "<node>" is unknown: <fingerprint>` and asks before it trusts the key. On
  yes it saves the node, trusts the key, tests again, and prints each step.
  Any other answer, or none, saves nothing.
- A node whose test fails otherwise is not saved, unless `--no-test` skips
  the test.

**Over the API**, `POST /inventories` creates an inventory, `PUT` and
`DELETE /inventories/{id}?version=` replace and delete one, and `PUT` and
`DELETE /inventories/{id}/nodes/{name}?version=` add, change, and remove one
node. A request body is decoded as sent: an unknown field, or data after
the record, is refused. Changes need the developer role and are written to
the audit log.

### Host keys

With `strict_host_key` on, the default, a node accepts the key that matches
its `host_key` or the key trusted for it. A project trusts at most one key
per node, kept by node name. Trusted keys are not part of an inventory, so
they are never exported or imported.

**Testing a connection.** `POST /node-connection-tests` tests the named
nodes, or the nodes a selector matches, from the server. A test checks the
host key of each hop, authenticates with the node's credentials, and runs
`true`. At most 16 nodes are tested at once, and a node named twice is
tested once. A disabled node is tested too: disabling keeps runs off a node,
not tests.

- The response is a server-sent event stream: a `result` event per node as
  its test ends, then a `done` event with the counts of nodes that passed
  and failed.
- A result holds the fingerprint each hop presented, the time the test took,
  and any error.
- When a hop presents a key that is unknown or has changed, the test stops
  before any credential reaches that hop. The result names the hop, `node`
  or `bastion`, the node whose key it is, and the presented and expected
  fingerprints.
- A test never trusts a key.

**Trusting a key.** `POST /node-host-keys` takes, for each node, the
fingerprint of the key the user saw. Two entries for one node are refused.

- The server reads the node's key again, through its bastion when it has
  one, and sends the node no credential. A bastion's own key must be
  accepted first.
- The key is stored only when its fingerprint matches. Otherwise the entry
  fails, naming the fingerprints the node presents.
- A node that has another trusted key keeps it, and the entry fails naming
  both fingerprints, unless the entry's `replace` is that key's
  fingerprint. An entry whose `replace` names a key the node no longer
  trusts fails naming the key it trusts now, and stores nothing.
- A key that is already trusted is reported as `unchanged`.
- The response is a `result` event per entry, then a `done` event.

A test needs the operator role, and a trust the developer role. Each test,
and each key a trust stores, is written to the audit log.

## Errors

Each error below exits non-zero and changes nothing.

| Case | Message |
| --- | --- |
| A selector term is invalid | `invalid label filter "<term>"`, as Spec 087 defines |
| `--labels` is given without terms, such as `,` or a blank value | `node selector is empty` |
| `--inventory` names no inventory | `inventory "<name or id>" not found` |
| `show` names no node | `node "<name>" not found` |
| The import file is not JSON, or a record has an unknown field | `invalid inventory JSON: <reason>` |
| An import breaks any rule | `nothing imported:`, then one line per broken rule: `inventory "<name>": node "<node>": <field>: <reason>`, without the node part for a rule of the inventory itself |
| `--format` is neither `table` nor `json` | `invalid --format "<format>": use table or json` |

The broken rules of an import include a node name another inventory holds,
naming that inventory, and an inventory name that exists without
`--replace`, as `name: already exists`.

## Examples

```sh
dagu nodes import inventories.json --dry-run    # check, store nothing
dagu nodes import inventories.json              # create new inventories
dagu nodes list --labels 'site=tokyo,web,!canary'
dagu nodes show tokyo-db01
dagu nodes export > inventories.json            # back up, or move elsewhere
dagu nodes import inventories.json --replace    # apply edits made to the file
```
