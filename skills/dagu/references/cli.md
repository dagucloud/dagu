# Dagu CLI Reference

Global flags on all commands: `--config/-c`, `--dagu-home`, `--quiet/-q`, `--cpu-profile`

Advanced and deprecated flags below remain implemented in `internal/cmd/start.go`, `internal/cmd/enqueue.go`, and `internal/cmd/exec.go`, so this reference keeps them documented even when they are mainly used by automation or backward-compatibility paths.

## Core Commands

### dagu start

Execute a DAG.

```sh
dagu start [flags] <dag> [-- params...]
```

Flags:

- `--params/-p` — Parameters (key=value or positional)
- `--name/-N` — Override DAG name
- `--run-id/-r` — Custom run ID
- `--from-run-id` — Historic dag-run ID to use as the template for a new run
- `--labels` — Additional labels (comma-separated key=value or key-only)
- `--tags` — Deprecated alias for `--labels`
- `--default-working-dir` — Default working directory for DAGs without explicit workingDir
- `--no-reuse` — Recompute reusable build steps while preserving staged, atomic publication
- `--worker-id` — Worker ID executing this DAG run; auto-set in distributed mode and defaults to `local`
- `--trigger-type` — Trigger source (`scheduler`, `manual`, `webhook`, `subdag`, `retry`, `catchup`); defaults to `manual`

### dagu enqueue

Enqueue a DAG run for later execution.

```sh
dagu enqueue [flags] <dag> [-- params...]
```

Flags:

- `--params/-p` — Parameters (key=value or positional)
- `--name/-N` — Override DAG name
- `--run-id/-r` — Custom run ID
- `--queue/-u` — Override the DAG-level queue definition
- `--labels` — Additional labels (comma-separated key=value or key-only)
- `--tags` — Deprecated alias for `--labels`
- `--default-working-dir` — Default working directory for DAGs without explicit workingDir
- `--no-reuse` — Recompute reusable build steps when the queued run starts
- `--trigger-type` — Trigger source (`scheduler`, `manual`, `webhook`, `subdag`, `retry`, `catchup`); defaults to `manual`

### dagu exec

Execute a one-off command as a DAG run without a DAG YAML file.

```sh
dagu exec [flags] -- <command> [args...]
```

Flags:

- `--run-id/-r` — Custom run ID
- `--name/-N` — Override DAG name
- `--workdir` — Working directory for the command (defaults to the current directory)
- `--shell` — Override shell binary for the command
- `--base` — Path to a base DAG YAML whose defaults are applied before inline overrides
- `--env/-E` — Environment variable (`KEY=VALUE`) to include in the run; repeatable
- `--dotenv` — Path to a dotenv file to load before execution; repeatable
- `--worker-label` — Worker label selector (`key=value`) for distributed execution; repeatable

### dagu dequeue

Dequeue a DAG run from a queue (marks it as aborted): `dagu dequeue <queue-name> [--dag-run/-d <dag:run-id>]`

### dagu stop

Stop an active DAG run: `dagu stop <dag-name> [--run-id/-r <id>]`

### dagu restart

Stop and restart a DAG run: `dagu restart <dag-name> [--run-id/-r <id>]`

### dagu retry

Retry a previous DAG run using the same run ID.

```sh
dagu retry <dag> --run-id/-r <id> [--step <name>] [--downstream] [--worker-id <id>]
```

`--step` retries only the selected step. Add `--downstream` to also reset every reachable descendant; unrelated branches keep their current status. `--downstream` requires `--step`.

### dagu human-task complete

Complete a waiting human task in a root DAG run. The run may be local or distributed, but the command operates on the local Dagu data store.

```sh
dagu human-task complete [flags] <root-dag-name>
```

Flags:

- `--run-id/-r` — Root DAG-run ID containing the human task; required
- `--step` — Human task step ID; required and matched against `id`, not the display name
- `--input` — Form input in `key=value` form; repeatable and coerced using the form schema
- `--inputs-json` — Typed form input as one JSON object

`--input` and `--inputs-json` are mutually exclusive. Omit both for an acknowledgement-only task. Completing one of several waiting human tasks leaves the DAG run waiting; completing the last one starts the run resume automatically. Human tasks cannot be used in sub-DAGs. A distributed run is re-queued, so its scheduler must be running. The command only supports the local context.

```sh
dagu human-task complete --run-id=run-1 --step=review --input environment=production deploy
dagu human-task complete --run-id=run-1 --step=review --inputs-json='{"environment":"production","notify":true}' deploy
```

### dagu dry

Dry-run a DAG without executing commands: `dagu dry [--params/-p] [--name/-N] [--no-reuse] <dag> [-- params...]`

For a build DAG, `--no-reuse` previews the decisions with manifest reuse disabled. Dry-run still creates no locks, staging files, manifests, or run history.

### dagu validate

Validate DAG YAML without executing: `dagu validate <dag>`

### dagu status

Show DAG run status: `dagu status <dag-name> [--run-id/-r <id>] [--sub-run-id/-s <id>]`

### dagu history

Show DAG run history.

```sh
dagu history [dag-name]
```

Flags:

- `--from` — Start date/time in UTC (format: `2006-01-02` or `2006-01-02T15:04:05Z`)
- `--to` — End date/time in UTC (same formats as `--from`)
- `--last` — Relative time period (e.g. `7d`, `24h`, `1w`). Cannot combine with `--from`/`--to`
- `--status` — Filter by status: `running`, `succeeded`, `failed`, `aborted`, `queued`, `waiting`, `rejected`, `not_started`, `partially_succeeded`
- `--run-id` — Filter by run ID (partial match supported)
- `--labels` — Filter by labels (comma-separated key=value or key-only, AND logic)
- `--tags` — Deprecated alias for `--labels`
- `--format/-f` — Output format: `table` (default), `json`, `csv`
- `--limit/-l` — Max results (default 100, max 1000)

Default: shows runs from the last 30 days, newest first.

### dagu ls

List DAG definitions.

This command is local-only. If a remote CLI context is selected, use `--context local`.

```sh
dagu ls [flags] [pattern]
```

Flags:

- `--next/-n` — Show next scheduled run time
- `--last/-l` — Show last run status and time
- `--history/-H` — Show a compact recent-history summary
- `--sort-last/-t` — Sort by last run time, newest first
- `--reverse/-r` — Reverse sort order

### dagu rm

Remove DAG run history and/or the DAG YAML definition. At least one of `--history` or `--definition` is required. Active runs are never deleted from history; definition deletion is refused while the DAG has alive processes. With `--definition`, identify the DAG by filename, stem, or configured path.

```sh
dagu rm [--history|-H] [--definition|-d] [-t <duration>] [-f] [--dry-run] <dag>
```

Flags:

- `--history/-H` — Delete run history
- `--definition/-d` — Delete the DAG YAML definition
- `--older-than/-t` — With `--history`: delete runs older than a duration (e.g. `10d`, `24h`, `1w`). Omitted = delete all history
- `--force/-f` — Skip confirmation prompt
- `--dry-run` — Preview deletions without removing history or the definition

### dagu ps

List running DAG processes.

```sh
dagu ps [-d <dag-name>] [-r <run-id>]
```

`-r`/`--run-id` accepts a partial run ID and matches accordingly.

### dagu cleanup

Remove old DAG run history. Active runs are never deleted.

Deprecated: prefer `dagu rm --history`.

```sh
dagu cleanup <dag-name> [--retention-days <n>] [--dry-run] [--yes/-y]
```

### dagu schema

Show JSON schema documentation. Use a dot-separated path to drill into nested sections.

```sh
dagu schema <dag|config> [path]
```

Examples:

- `dagu schema dag` — All DAG root-level fields
- `dagu schema dag steps` — Step definition structure
- `dagu schema dag steps.container` — Container configuration
- `dagu schema dag steps.retry_policy` — Retry policy fields
- `dagu schema dag steps.harness` — Harness step configuration
- `dagu schema dag handler_on` — Lifecycle event hooks
- `dagu schema config` — All config root-level fields
- `dagu schema config auth` — Authentication configuration

### dagu config

Show resolved configuration paths.

```sh
dagu config
```

## Server & Scheduling

### dagu start-all

Start server + scheduler + optionally coordinator in one process. Coordinator enabled by default (disable with `DAGU_COORDINATOR_ENABLED=false`).

```sh
dagu start-all [--host/-s <host>] [--port/-p <port>] [--dags/-d <dir>]
```

Also accepts `--coordinator.*` and `--peer.*` flags for distributed setup.

### dagu server

Start web UI + REST API.

```sh
dagu server [--host/-s <host>] [--port/-p <port>] [--dags/-d <dir>] [--tunnel/-t]
```

### dagu scheduler

Start cron scheduler. Monitors DAGs and triggers runs on schedule; also processes queued runs.

```sh
dagu scheduler [--dags/-d <dir>]
```

## Distributed Execution

### dagu coordinator

Start gRPC coordinator: `dagu coordinator [--coordinator.host/-H <host>] [--coordinator.port/-P <port>] [--peer.*]`

### dagu worker

Start distributed worker: `dagu worker --worker.coordinators <host:port,...> [--worker.id/-w <id>] [--worker.max-active-runs/-m <n>] [--worker.labels/-l <k=v,...>] [--peer.*]`. Coordinator addresses are required. Every worker advertises immutable `os` and `arch` platform labels in addition to configured labels.

## Git Sync

`dagu sync <subcommand>` — Git sync for workflows, Wiki content, and supporting files.

| Subcommand | Description |
| ---------- | ----------- |
| `sync status` | Show repository, branch, and per-item status |
| `sync pull` | Pull changes from remote |
| `sync publish [item-id] [--message/-m] [--all] [--force/-f]` | Publish local changes to remote |
| `sync discard <item-id> [--yes/-y]` | Discard local changes, restore remote version |
| `sync forget <id>... [--yes/-y]` | Remove state entries for missing/untracked items |
| `sync cleanup [--dry-run] [--yes/-y]` | Remove all missing entries from sync state |
| `sync delete <id> [--message/-m] [--force] [--all-missing] [--dry-run] [--yes/-y]` | Delete from remote, local, and sync state |
| `sync mv <old> <new> [--message/-m] [--force] [--dry-run] [--yes/-y]` | Rename across local, remote, and sync state |

## Other Commands

- `dagu example [id]` — Show built-in example DAGs
- `dagu version` — Show version
- `dagu upgrade [--check] [--version/-v <ver>] [--dry-run] [--yes/-y]` — Self-update binary
- `dagu license <activate|deactivate|check>` — Manage license
