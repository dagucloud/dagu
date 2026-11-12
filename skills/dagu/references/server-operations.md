# Operating a Running Dagu Server

Use this file when a request targets a running Dagu instance rather than local files: listing or changing DAGs, starting, stopping or retrying runs, or reading run status and logs. A request to do something "with Dagu" that names no context targets the running server.

## Routing order

Use the first tier that supports the operation. Fall back only when it is unavailable, unauthorized, or unsupported, and say which tier was used when it affects the result.

1. **MCP** (`<server>/mcp`): tools `dagu_read`, `dagu_change`, `dagu_execute`.
2. **REST API** (`<server>/api/v1`).
3. **CLI remote context** (`dagu --context <name> ...`), only for commands that support it.
4. **Local CLI or file edits**, last resort. These act on local files, not on the server.

Setting `DAGU_HOME` or `DAGU_DAGS_DIR` only points a local CLI at the server's files. It is not a server request, and a server-requested action must never silently become a local one.

## Prerequisites (check once per session)

- **Endpoint:** find the server's listen address from its configuration or process (`ss -ltnp`). Do not dump a process environment; it may hold secrets.
- **Credential:** check by name only, never print the value. MCP and REST take `Authorization: Bearer <key>`. API keys can be limited to surfaces, so a REST-only key can be rejected by MCP.
- **Reachability:** MCP `initialize` then `tools/list`; REST `GET /api/v1/health` (no auth) and `GET /api/v1/dags`. Close any MCP session you open with `DELETE` and the session id.
- **Remote context:** `dagu context list`. Creating one changes CLI state, so ask first. Use `dagu context add <name> --server <url>/api/v1` and let the CLI prompt for the key instead of passing it in argv.
- **Write permission:** a successful read does not prove writes are allowed, and `dagu_change` with `mode=preview` never writes. Write permission is known only when an apply succeeds; report an authorization error as is.
- **No credential found:** ask the user. Never search files or shell history for keys.

## Capability matrix

| Operation | MCP | REST | CLI remote context |
|---|---|---|---|
| List or read DAGs | `dagu_read` (`dags`, `dag_spec`) | `GET /dags`, `GET /dags/{fileName}/spec` | not supported (`dagu ls` is local-only) |
| Validate a spec | `dagu_change` `mode=preview` | `POST /dags/validate` | not supported (`dagu validate` is local-only) |
| Create or update a DAG | `dagu_change` `upsert_dag`: preview, then apply | `POST /dags`, `PUT /dags/{fileName}/spec` | no |
| Rename or delete a DAG (ask first) | `dagu_change` `rename_dag` / `delete_dag` | `POST /dags/{fileName}/rename`, `DELETE /dags/{fileName}` | no |
| Start, enqueue, retry, stop | `dagu_execute` | DAG and DAG-run endpoints | `start`, `enqueue`, `retry`, `stop`, `restart`, `dequeue` |
| Run status, history, logs | `dagu_read` (`runs`, `run`, `run_logs`) | `GET /dag-runs`, `GET /dag-runs/{name}/{id}` | `history`, `status` |
| Approve, reject, push back a waiting step | `dagu_execute` | where supported | not supported |
| Schedule | `schedule:` via `dagu_change` | edit the DAG spec | no separate command |

The live MCP `tools/list` schema and the server's OpenAPI document are authoritative over this table. Commands that cannot run remotely fail with `only supports the local context`; do not retry them locally without saying so.

## Practical notes

- MCP is JSON-RPC over HTTP: `initialize`, `notifications/initialized`, then `tools/call`, sending the returned `Mcp-Session-Id` header on every later request and `Accept: application/json, text/event-stream`. Use REST for simple reads.
- For create and update, preview, check `valid`, apply, then read the DAG back.
- Report a run's `statusLabel` (such as `succeeded`), not the numeric `status`.
- Pass the key to `curl` through a config file on stdin (`-K -`) so it stays out of argv and output.

## Ask first

Deleting DAGs, purging run history, editing the DAG directory by hand, or an unclear target. Otherwise proceed without asking which context to use.

## Local-only fallback

- `dagu ls`, `validate`, `dry` and `rm` are local-only. Use them for local inspection or an intentionally managed DAG directory.
- If tiers 1-3 are unavailable, say so and state that the action will be local before taking it; ask first if it changes anything.
- On an authorization denial, report it. Do not look for a more privileged key.
- `dagu start <path>` proves only that a file can run, not that the server discovered it.
- Before local CLI use against the server's data, match its paths and verify with `dagu config`.
- To schedule a DAG by file, place it in the directory the running scheduler reads, which can differ from the CLI's: check the server's `--dags` flag, `DAGU_HOME`, `DAGU_DAGS_DIR` and `dag_discovery.symlinks` (`dagu schema config`), and confirm the DAG shows a next run on the server.
