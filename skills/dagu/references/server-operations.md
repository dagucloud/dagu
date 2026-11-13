# Operating a Running Dagu Server

Use this file when a request targets a running Dagu instance rather than local files: listing or changing DAGs, starting, stopping or retrying runs, or reading run status and logs.

## Detect the target (once per session, without asking)

Do not ask whether the user means a server or local files. Decide in this order:

1. The user names a context, URL, or "local": use it. If they name a remote server without a context or URL, ask for it.
2. Dagu MCP tools (`dagu_read`, `dagu_change`, `dagu_execute`) are already connected: use them.
3. `dagu context list` shows a current remote context: use it. If `local` is current and exactly one remote context exists, use that; if several remote contexts could apply (for example staging and production), ask which.
4. Probe `GET <url>/api/v1/health` on the first candidate that exists: a configured URL, else the address of a listening `dagu` process (`ss -ltnp`), else `localhost:8080`. The port is often not 8080. If it is healthy, use it, and before the first call list credential variable names (see Prerequisites) to get a key for MCP and REST; a 401 without a key means the key was not checked yet. If it is not healthy, report the server as unavailable and ask before any local change; do not move on to another candidate. A failed probe of the `localhost:8080` guess alone means no server (step 5).
5. No candidate exists: no server. Use the local CLI and file edits as usual, saying once that no server was found.

Ask only if a server is detected and the request could equally mean local DAG files.

## Routing order

Once a server is detected, use the first tier that supports the operation and reaches the selected target; a tier pointing at a different server (for example connected MCP tools for production when staging was named) is not eligible. Fall back only when it is unavailable, unsupported, or its key is rejected for that whole surface (after a timed-out write, check the run list before retrying so it does not start twice), and say which tier was used when it affects the result.

1. **MCP**, either already-connected Dagu tools or the server's `<server>/mcp` endpoint: tools `dagu_read`, `dagu_change`, `dagu_execute`.
2. **REST API** (`<server>/api/v1`).
3. **CLI remote context** (`dagu --context <name> ...`), only for commands that support it.
4. **Local CLI or file edits**, last resort when a server was detected. These act on local files, not on the server.

Setting `DAGU_HOME` or `DAGU_DAGS_DIR` only points a local CLI at the server's files. It is not a server request, and once a server is detected, a server-requested action must never silently become a local one.

## Prerequisites (check once per session)

- **Endpoint:** find the server's listen address from its configuration or process (`ss -ltnp`). Do not dump a process environment; it may hold secrets.
- **Credential:** check by name only, never print the value; list environment variable names that contain `DAGU` or `API_KEY`. MCP and REST take `Authorization: Bearer <key>`. API keys can be limited to surfaces, so a REST-only key can be rejected by MCP.
- **Reachability:** MCP `initialize` then `tools/list`; REST `GET /api/v1/health` (no auth) and `GET /api/v1/dags`. Close any MCP session you open with `DELETE` and the session id.
- **Remote context:** `dagu context list`. Creating one changes CLI state, so ask first. Use `dagu context add <name> --server <url>/api/v1` and let the CLI prompt for the key instead of passing it in argv. The prompt needs an interactive terminal; without one the command fails with `--api-key is required`, so ask the user to run it.
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

- MCP is JSON-RPC over HTTP: `initialize`, `notifications/initialized`, then `tools/call`, sending the returned `Mcp-Session-Id` header on every later request and `Accept: application/json, text/event-stream`. Tool arguments are flat, for example `dagu_change` `{type, name, spec, mode}` and `dagu_execute` `{action, name, dagRunId}`, `dagu_read` `{target: "dags"}`; `mode` defaults to `preview`, so send `mode=apply` to write.
- For create and update, preview, check `valid`, apply, then read the DAG back.
- Report a run's `statusLabel` (such as `succeeded`), not the numeric `status`.
- Pass the key to `curl` through a config file on stdin (`-K -`) so it stays out of argv and output.

## Ask first

Deleting DAGs, purging run history, editing the DAG directory by hand, or an unclear target. Otherwise proceed without asking which context to use.

## Local-only fallback

- `dagu ls`, `validate`, `dry` and `rm` are local-only. Use them for local inspection or an intentionally managed DAG directory.
- If tiers 1-3 are unavailable, say so and state that the action will be local before taking it; ask first if it changes anything.
- On a denial of a specific operation, report it and stop. Do not look for a more privileged key.
- `dagu start <path>` proves only that a file can run, not that the server discovered it.
- Before local CLI use against the server's data, match its paths and verify with `dagu config`.
- To schedule a DAG by file, place it in the directory the running scheduler reads, which can differ from the CLI's: check the server's `--dags` flag, `DAGU_HOME`, `DAGU_DAGS_DIR` and `dag_discovery.symlinks` (`dagu schema config`), and confirm the DAG shows a next run on the server.
