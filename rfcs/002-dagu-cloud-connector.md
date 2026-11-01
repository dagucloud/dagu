# RFC 002: Dagu Cloud Connector

- **Status:** Proposed
- **Date:** 2026-10-09
- **Affected areas:** `license`, `eventstore`, `cloudrelay`, `cloudaccess`,
  `serviceregistry`, `service/frontend` (auth, API, UI), `service/mcp`,
  `audit`, `cmn/config`, and `cmd`

## Summary

Let a Dagu server with an online license report its health and run status to
Dagu Cloud, and let the server's administrator open it to remote access from
Dagu Cloud without inbound ports, VPN, SSH, or remote desktop.

Activating an online license is the connection. There is no separate connect
command. Reporting is off until an administrator of the server turns it on,
seeing exactly what is sent, and then sends metadata only. Remote access is
off until an administrator of the server turns it on, on the server itself,
and it never exceeds the role they choose.

Workflows never depend on Dagu Cloud. Losing the connection stops reports and
remote access, nothing else.

## Problem statement

Teams that run Dagu for someone else, such as a consultancy for its clients or
a platform team for remote sites, have no way to watch many servers at once.
Today they:

- reach each server through VPN, SSH, or remote desktop into infrastructure
  they do not own;
- learn about failures from notifications sent by the server itself, so a
  server that is down, or whose scheduler stopped, reports nothing;
- cannot use remote nodes (`internal/remotenode`) across sites, because the hub
  must reach every node's API over the network and must store a credential for
  each one.

## Goals

1. **One step.** Activating an online license connects the server.
2. **Nothing without consent.** Upgrading or connecting sends nothing new.
   Reporting starts only when the server's admin chooses it, and then sends
   metadata: health, version, and run status, and failed steps' error
   messages only at the level that names them. No full logs, outputs,
   parameters, or secrets.
3. **Outbound only.** Remote access rides a connection the server opens.
4. **The server decides.** Remote access is opt-in per server, capped by a
   local role, and can be paused from the server at any time.
5. **No execution dependency.** An unreachable Dagu Cloud changes nothing about
   how workflows run.
6. **Versioned contracts.** Servers upgrade years apart; every message carries
   a protocol version.

## Non-goals

- Running workflows in Dagu Cloud.
- Storing logs, outputs, parameters, or secret values in Dagu Cloud.
- Terminal or shell access through Dagu Cloud.
- Deploying workflow definitions from Dagu Cloud. A later RFC covers it.
- Air-gapped servers. Offline licenses never connect.

## Connection

### License sources

A server is connected while it holds a license that heartbeats.

| Source (`license/discover.go`) | Heartbeats | Connected |
| --- | --- | --- |
| `DAGU_LICENSE_KEY` | yes | yes |
| `license.key` | yes | yes |
| Persisted activation (`<data>/license`) | yes | yes |
| `DAGU_LICENSE` (inline JWT) | no | no |
| `DAGU_LICENSE_FILE`, `<data>/license/license.jwt` | no | no |

### Browser approval

Dagu Cloud already approves servers in the browser (`/servers/connect` and
`POST /api/v1/licenses/connect`). Dagu does not use it yet. This RFC adds it as
a way to activate a license, not as a new concept:

- **UI.** The license settings page gets **Connect to Dagu Cloud** for admins.
- **CLI.** `dagu license activate` with no key starts the same flow and prints
  the approval link.

```text
 Dagu server                           Dagu Cloud console                 Owner (browser)
   │ secret = 32 random bytes                 │                                │
   │ open /servers/connect?code=sha256(secret)&server_id=…&name=…&version=… ─▶ │
   │                                          │ ◀── sign in, pick workspace ── │
   │                                          │ ◀── approve ────────────────── │
   │ POST /api/v1/licenses/connect {connection_secret, server_id} (every 3 s)  │
   │ ◀── {status: pending} … {status: granted, token, heartbeat_secret}        │
   │ persist activation (as `license activate` does today)                     │
```

The approval page states what the server will report. The admin may choose
the report level when starting the connection; the URL then carries the level
in effect as `report`, the approval page states it, and reporting starts once
the license is granted. Approvals expire after 15 minutes, as the console
enforces today.

`version` is the Dagu version, the same string the server sends as
`client_version`: `2.19.2` from a release, `v2.19.2-3-gabc1234` from a source
build, or `dev`. The approval page uses it to turn away servers too old for
the plan on offer (see Free plan).

### Disconnecting

**Disconnect** on the license settings page calls `POST /api/v1/licenses/release`,
clears the activation, and stops reporting and remote access at once. Disconnecting
from the console has the same effect on the next report (`401`).

### Free plan

A workspace can connect a server without a paid plan, so that Dagu Cloud
monitors it: the server's runs, failures, and trends, with an email when a
workflow fails or the server stops reporting.

- **Token.** The license token has `plan` `free` and an empty `features`
  list. Dagu treats it as Community for every paid feature: SSO, RBAC, audit
  logs, incident routing, and API keys beyond two stay off, even if the token
  claims features.
- **Still connected.** The server checks in hourly and reports at the level
  its admin chooses, as any connected server does. Starting another
  connection fails as it does with a paid license, and disconnecting frees the
  server's free slot.
- **Slower reports.** Dagu Cloud may answer a free server with
  `next_report_seconds` `300`, and answer reports that come sooner, early
  reports included, with `429` and `Retry-After`. The reporter honors both
  (see Cadence and Wire contract).
- **Plan changes.** The next check-in replaces the token, so paid features
  turn on or off without reconnecting.
- **Version gate.** Releases before v2.19.2 treat any license as paid. Dagu
  Cloud refuses a free connection when `version` is missing or older than
  v2.19.2, with the message "Update Dagu to v2.19.2 or later to use free
  monitoring."

## Reporting

### What is sent

This table lists what `cloud.report` chooses to send about the server and its
runs. A field is listed here, under its level, before any release sends it.

Every report also carries what identifies and authenticates the server, as
its license check-in does: the protocol version, license ID, server ID, and
heartbeat secret, and the level it was sent at. A report at any level but `off`
carries the time it was built, by the server's clock. At `runs` and `errors` it
also carries the reporter's cursor and, after events were lost, the time they
were lost from.

| Level | Fields | When |
| --- | --- | --- |
| `health` | Dagu version, OS, architecture, process start time, service counts (schedulers holding the scheduler lock, and registered coordinators), the scheduler's last tick and whether it is paused, and the remote access level and whether remote approvals are on | Every report |
| `runs` | The `health` fields, plus the status of each top-level DAG run that finishes, with its failed step names (up to 50) and how it was triggered, and the list of runs queued, running, or waiting. Each run is sent as its DAG name, run ID, attempt ID, status, trigger, and queued, started, and finished times. Also each scheduled DAG: its name, its next run, and its latest start-schedule slot that came due with what the scheduler did with it (dispatched, skipped as running, queued, done, succeeded, or overlapping, failed, planned, or missed while no scheduler ran) | Each finished run once, at most 500 and 512 KiB of them per report; the runs in progress with every report, at most 200 of them and a count of all; within 5 seconds of a failed, aborted, or rejected run or a change to the runs in progress. The scheduled DAGs with every report from the process that reports the runs, those whose slot did not run first, at most 500 and 128 KiB of them and a count of all |
| `errors` | The `runs` fields, plus why each finished run failed: the error messages of its failed steps and handlers, at most 5, or the run's own error when no step has one. Each is sent without terminal escapes, at most 2048 bytes from its end. A command step's error ends with the last 1 KiB of its stderr; the values of workflow secrets are masked in it as in logs | With the finished run it belongs to |

A run is reported unless its event has no valid status snapshot, or it names
a DAG longer than 512 bytes or a run or attempt ID longer than 256 bytes,
which Dagu Cloud would not keep. Those runs are left out.

**Planned, not sent by any level yet:** queue depth, worker counts, and the
list of unscheduled DAGs. Each joins the table above, under its level, before
it is sent.

**Never sent:** full logs, outputs, parameters, environment, step commands, DAG
YAML, schedule expressions, and the values of workflow secrets. Error messages are sent only at
`errors`.

DAG names and step names are sent at `runs` and `errors`. An administrator who
considers them sensitive chooses `health`, which sends neither. Error messages
often contain hosts, paths, or data the step printed; masking covers only the
values of declared secrets. An administrator who would not send them chooses
`runs`.

### Choosing what to report

| `cloud.report` | Sends |
| --- | --- |
| unset or `off` (default) | Nothing beyond the license check-in, except one report that says so, carrying only what identifies the server, when the level becomes `off` after a report at `health`, `runs`, or `errors` |
| `health` | Dagu version, OS, architecture, start time, which services run, and whether the scheduler ticks or is paused |
| `runs` | Also the status of finished runs and the runs in progress: DAG names, run IDs, status, triggers, times, failed step names; and each scheduled DAG's next run and whether its latest scheduled run started |
| `errors` | Also the error messages of failed steps, cut to their end |

- **Upgrading changes nothing.** A server licensed before reporting existed
  keeps sending only its license check-in.
- **The admin chooses in the UI**, at a moment it is useful: right after the
  server connects to Dagu Cloud, or from a one-time notice on a server that
  was already licensed. A server that chose run status before error
  messages could be sent is offered them once, from the same kind of
  notice. The dialog lists what each level sends and what is never sent,
  before anything is. The choice persists in the data directory
  and applies without a restart.
- **Config wins.** Set in configuration, `cloud.report` fixes the level for
  servers managed as code, and the UI shows it read-only.
- **Inspectable and reversible.** The license settings page shows the level,
  when the last report was sent, and the exact last report, with its
  heartbeat secret redacted. Turning it off takes effect at once and stops a
  report in flight; one last report then says that reporting is off, so that
  Dagu Cloud does not take the silence for an outage. Dagu Cloud can delete
  what a server reported.
- **No catching up.** When the level drops below `runs` while the server
  runs, the reporter moves its cursor to the newest event, so choosing `runs`
  again never sends the runs that finished in between. It lists the runs in
  progress anew.
- **The server decides.** Dagu Cloud cannot turn reporting on; it shows a
  server that checks in without reporting as not monitored.

### Cadence

The server reports every 60 seconds. It reports early, within 5 seconds and
at most every 5 seconds, when a run fails, is aborted, or is rejected, and when
the runs in progress changed since the last report Dagu Cloud accepted: a run
was queued, started, began waiting, or finished. Once a report is throttled,
rejected, or fails, nothing is reported early until one is accepted, and news
that an accepted report carried calls for no further early report. Dagu Cloud
can lengthen the interval in its response (`next_report_seconds`) to shed
load, and does for free servers (see Free plan).

Many workflows run at the top of the hour, so reports are spread out:

- the first report waits a random 0 to 60 seconds after start, and changes to
  the runs in progress do not cut it short;
- each interval varies randomly by up to 10 percent;
- an early report waits for the next check, a random 0 to 5 seconds after the
  change.

### Where it runs

The reporter runs in the process that holds the license manager (`server` and
`start-all`, `internal/cmd/context.go`). It is an event store cursor consumer
built like `chatbridge.NotificationMonitor`:

- a cross-process lease, so one process reports when several share a data
  directory;
- a persisted cursor, advanced only after Dagu Cloud acknowledges it;
- `ReadDAGRunEvents` from the persisted cursor
  (`eventstore/dagrun_events.go`).

A resent batch is harmless: Dagu Cloud stores each finished run once, by
server, run ID, and attempt ID.

**Runs in progress.** The reporter follows the runs queued, running, or
waiting through the same events, and saves the list with its cursor. When it
has no saved list, and after a gap, it lists them anew from the run history
(`DAGRunRepository.ListStatuses`, runs of the last 30 days) in the background.
Reports leave the list out until it is known.

**Retention.** The event store keeps one day by default
(`event_store.retention_days`). A server cut off for longer loses the events in
between. The reporter detects a cursor older than retention and reports a gap;
Dagu Cloud shows the gap rather than guessing runs. If the event store is
disabled, reports carry health only.

### Wire contract

`POST {license.cloud_url}/api/v1/servers/report`, authenticated with the
activation's credentials, as the heartbeat is.

```json
{
  "protocol": 2,
  "license_id": "…",
  "server_id": "…",
  "heartbeat_secret": "…",
  "level": "runs",
  "health": { "version": "3.0.0", "os": "linux", "arch": "amd64",
              "started_at": "2026-10-09T12:00:00Z",
              "services": [{ "name": "scheduler", "instances": 1 },
                           { "name": "coordinator", "instances": 0 }],
              "remote_access": "operator", "remote_approvals": false },
  "finished": [{ "workflow": "etl", "dag_run_id": "…", "attempt_id": "…",
                 "status": "failed", "trigger_type": "scheduler",
                 "queued_at": "2026-10-10T01:00:00Z",
                 "started_at": "2026-10-10T01:00:01Z",
                 "finished_at": "2026-10-10T01:02:03Z",
                 "failed_steps": ["load", "notify"] }],
  "active": [{ "workflow": "report", "dag_run_id": "…", "attempt_id": "…",
               "status": "running", "trigger_type": "manual",
               "queued_at": null, "started_at": "2026-10-10T01:01:00Z" }],
  "active_total": 1,
  "cursor": "opaque",
  "gap": { "since": "2026-10-08T00:00:00Z" }
}
```

`level` is the level the report was sent at, `health`, `runs`, or `errors`, or
`off` for the report that says reporting stopped.

A scheduler count of zero means no scheduler holds the lock; leaving
`services` out means the count is unknown. `remote_access` is `off`,
`viewer`, `operator`, or `developer`; it and `remote_approvals` are left out
when the server cannot read its remote access settings.

`finished` holds top-level runs that succeeded, partially succeeded, failed,
were aborted, or were rejected, oldest first. `workflow` is the DAG name.
`status` is `succeeded`, `partially_succeeded`, `failed`, `aborted`, or
`rejected`; `trigger_type` is `scheduler`, `manual`, `webhook`, `retry`, or
`catchup`, and empty when unknown. Times are RFC 3339; `queued_at` and
`started_at` are null when unknown. `failed_steps` names failed steps and
handlers, at most 50 of at most 512 bytes each. At `errors`, a failed run
also carries `errors`: `[{"step": "load", "message": "exit status 1\n…"}]`, at
most 5, with `step` empty for the run's own error; it is left out when the
run has none and at every other level.

`cursor`, at most 4096 bytes, identifies the position after `finished`; it
comes with every `finished`, which is empty when the cursor moves only past
events that end no run. `gap` comes after events were lost, with the time of
the last acknowledged event, and always with a cursor; it repeats until the
console acknowledges that cursor. Reporting continues from the newest event.

`active` lists the runs in progress, with `status` `queued`, `running`, or
`waiting` and the same fields up to `started_at`: at most 200, the most
recently started or queued first. `active_total` counts them all. Each
report's list replaces the one before. Both are left out at `health`, and at
`runs` until the reporter knows the runs in progress; a report without them
says nothing about the runs in progress.

**Reporting off.** When the level becomes `off` after a report at `health` or
`runs`, whether chosen in the UI or set in configuration and applied at the
next start, the next report due is the last one:

```json
{ "protocol": 2, "license_id": "…", "server_id": "…",
  "heartbeat_secret": "…", "level": "off" }
```

It carries no `health` and no runs. It is retried like any report on `429`,
`5xx`, and network errors; once Dagu Cloud answers otherwise, nothing more is
sent until a level that reports is chosen. The server records the level of
its last report in the data directory, so a server stopped before sending
this report sends it after it starts. If that record cannot be saved, which
the server logs, a restart may send the report again or not at all. A level
that cannot be read stops reports without it, since no one chose `off`.

The response is:

```json
{ "ack": "opaque", "next_report_seconds": 60,
  "runs_paused_until": "2026-10-11T00:00:00Z", "reporting": "on" }
```

`runs_paused_until` comes while Dagu Cloud pauses storing finished runs, after
the workspace reached its daily limit. The reporter keeps sending them, since
Dagu Cloud still counts them, and the license settings page shows until when
storing is paused. Dagu ignores `reporting`: what a server reports is decided
on the server.

| Response | Reporter behavior |
| --- | --- |
| `2xx` | Persist the cursor when `ack` equals the cursor sent. Keep `runs_paused_until`, or no pause when it is absent. |
| `401`, `410` | Keep the schedule. The license manager handles the activation as it does for heartbeats. |
| Other `4xx`, such as `400` or `413` | Keep the schedule and the cursor, and log the rejection. Reports stay under 1 MiB, so `413` is not expected. |
| `429` | Wait for `Retry-After`, keeping the cursor. |
| `5xx`, network error | Back off exponentially up to 5 minutes, keeping the cursor. |

New fields are optional in both directions. Dagu Cloud accepts every protocol
version a supported Dagu release sends.

## Remote access

### Shape

```text
 AI app ──MCP──▶ Dagu Cloud console ──POST /servers/call──▶ Relay object for this server
   (OAuth 2.1)    (checks the grant,      (single-use call token,        │
                   mints an assertion)     assertion passed on unread)   │ WebSocket "access", opened by
                                                                         │ each server process (wss, 443)
                                                                         ▼
                                       Dagu server process: cloudaccess ──▶ verify ──▶ MCP handler (in process)
```

- **Dagu Cloud terminates MCP.** An AI app connects to Dagu Cloud's MCP
  endpoint with OAuth; its token never reaches a server. For a live answer,
  Dagu Cloud calls the server through the relay with an assertion bound to
  that one request.
- **Each server process holds its own connection.** While the license is
  online and remote access is not off, every process that serves the UI
  (`server`, `start-all`) asks for a relay ticket with
  `purpose: "access"` and connects with a random instance ID in the
  `Dagu-Instance` header. The relay keeps one connection per instance beside
  the scheduler's webhook connection, and sends each call to the connection
  heard from last. Dagu Cloud refuses the ticket while the server's workspace
  has neither a plan nor a trial; the server then waits 10 minutes, or until
  its remote access settings change.
- **Calls reach the MCP tools only.** The connection carries `POST /mcp` to a
  stateless MCP handler in the process, with the same tools as the server's
  own MCP endpoint and no resource subscriptions. Any other path answers
  `404`. The handler never passes through the public listener, and the public
  listener never accepts assertions.
- **A browser UI over the same connection** is a later step. It will use
  assertions with `use = session`, which servers refuse today.

### Turning it on

Remote access is set on the server, never from Dagu Cloud. The level is the
highest role any remote user gets.

- **UI.** The license settings page has a **Remote access** section for the
  server's admins: off, viewer, operator, or developer, chosen from a dialog
  that says what each allows, and warns that developer runs any command on
  the host, and a switch for **approvals**, which lets
  calls approve, reject, or push back steps waiting for a person. Both start
  off. A change applies at once and is persisted in the data directory; other
  processes sharing it apply it within 6 seconds. Turning remote access off
  closes the connection and cancels the calls in flight.
- **Config.** For servers managed as code, `cloud.remote_access` and
  `cloud.remote_approvals` fix the settings, and the UI shows them read-only.

```yaml
cloud:
  report: off                    # off | health | runs | errors; omit to choose in the UI
  remote_access: off             # off | viewer | operator | developer; omit to choose in the UI
  remote_approvals: false        # omit to choose in the UI
```

| Level | A call may |
| --- | --- |
| `off` | Nothing; every call answers `403` |
| `viewer` | Read DAGs, runs, and logs |
| `operator` | Also start, enqueue, retry, and stop runs of existing DAGs, with any parameters they take, and, with approvals on, decide waiting steps |
| `developer` | Also change DAG definitions and run inline specs; since DAG steps run commands, any command on the host, as the user Dagu runs as |

A `cloud:` identity can never change these settings (see below).

### Identity

Each call carries an assertion minted by Dagu Cloud and signed with the
license key:

| Claim | Value |
| --- | --- |
| `iss` | Dagu Cloud's console URL |
| `aud` | `dagu-server` |
| `use` | `call` |
| `license_id`, `server_id` | This server's; any other value is rejected |
| `tid` | The Dagu Cloud workspace ID |
| `sub`, `email` | The Dagu Cloud user |
| `cloud_workspace` | The Dagu Cloud workspace name, for display and audit |
| `role` | `viewer`, `operator`, or `developer`: the role Dagu Cloud grants this user on this server; any other value is rejected |
| `grant`, `client` | The user's connection from an AI app, and the app's name |
| `req_sha256` | Unpadded base64url SHA-256 of the method, a space, the request target, a newline, and the body |
| `iat`, `exp` | `exp` at most 60 seconds after `iat` |
| `jti` | Unique; the relay and the server each accept it once |

- Verified with the pinned Dagu Cloud key (`license/pubkey.go`), with 60
  seconds of clock skew.
- **Effective role** = the lower of `role` and `cloud.remote_access`.
- The request runs as a synthetic user `cloud:<sub>`, named by the email,
  like API key users (`apikey:<id>`). No local user is created. Its role
  limits it even on a server without users of its own (`auth.mode: none` or
  basic auth).
- **Approvals.** A call decides a waiting step only while remote approvals
  are on, and only with the `attemptId` it read, so a decision made from a
  stale read is refused.
- Audit entries use credential type `cloud`, transport `cloud_relay`, the
  grant as credential ID, the app as credential name, and record the email
  and Dagu Cloud workspace. The license settings page lists the latest of
  them.

**Key hygiene.** Dagu Cloud signs licenses, relay tickets, and assertions with
one key and separates them by audience. Licenses carry no audience; license
verification rejects any token that carries one, and the assertion verifier
requires `aud = dagu-server` and `use = call`.

### What remote users cannot do

Regardless of role, a `cloud:` identity cannot:

- open a terminal;
- manage users, API keys, SSO, the license, remote nodes, or `cloud.*`
  settings.

Calls reach only the MCP tools, and no level reaches manager or admin. Any of
these would let Dagu Cloud widen or keep its own access.

### Relay frames

JSON control frames and binary data frames over one WebSocket. Names follow
the existing relay where they apply.

| Direction | Frame |
| --- | --- |
| Server → relay | `hello{protocol, version, name}` |
| Relay → server | `limits{calls, request_bytes, answer_bytes, call_seconds}` on a connection for calls |
| Relay → server | `req{id, method, path, headers, assertion}` |
| Relay → server | binary request body chunks for `id`, then `end{id}` |
| Server → relay | `res{id, status, headers}` |
| Server → relay | binary response body chunks for `id`, then `end{id}` |
| Relay → server | `cancel{id}` |
| Server → relay | `"ping"` every 15 seconds; the relay answers `"pong"` |
| Both | `hooks`, `hooked`, `delivery`, `delivered` for public triggers (RFC 004), on the scheduler's connection |

A binary chunk is a version byte (`1`), the 16 bytes of the call's UUID, and
up to 64 KiB of the body. The relay sends request bodies of at most 512 KiB,
takes answers of at most 4 MiB, holds at most 16 calls in flight per server,
and ends a call after 100 seconds.

## Failure behavior

| Situation | Behavior |
| --- | --- |
| Dagu Cloud unreachable | Workflows unaffected. Reports back off and resume from the cursor. License grace applies as today. |
| Unreachable longer than event retention | The next report carries a gap. |
| Relay unreachable | Each connection reconnects with backoff, from 1 second to 1 minute. Dagu Cloud answers its users with the server's last report and when it was last seen. |
| License revoked or server disconnected | Reporting and the connections stop at once. |
| Remote access turned off | The connection for calls closes; calls in flight are cancelled. Calls that still arrive answer `403` with `remote_access_off`. |
| Workspace without a plan or a trial | Dagu Cloud refuses the access ticket; the license settings page says so. |
| Assertion invalid, expired, replayed, or for another server or request | `401` with `invalid_assertion`; nothing runs. |

## Observability

- **License settings page:** connected workspace, last report, reporting
  state, remote access level and approvals, whether the connection for calls
  holds or why Dagu Cloud refused it, and the latest calls.
- **Metrics:** reports sent and failed, event lag, connection state, remote
  calls by status.
- **Audit:** every call from Dagu Cloud, with its tool, action, and outcome.

## Compatibility and rollout

| Release | Ships |
| --- | --- |
| v2.19 | Browser approval; reporting; the `cloud` config section. Dagu Cloud's report endpoint must be live first. |
| v2.19.2 | The free plan, and `version` on the connect URL. |
| v2.20 | The `dagucloud` tunnel provider, carrying webhook deliveries for public triggers only (RFC 004). |
| v2.22 | Remote access for MCP calls over each server process's own connection: assertions, the Remote access section, remote approvals, the license audience check, and the remote access level in health reports. |
| Later | A browser UI over the same connection, with `use = session` assertions and the UI bar. |

Older servers keep working as they do: they heartbeat and never report.

## Verification and acceptance criteria

1. A server activated through browser approval appears in Dagu Cloud within
   60 seconds with its version and services.
2. A failed run appears in Dagu Cloud within 10 seconds.
3. Stopping the scheduler shows it as down within 2 minutes.
4. Two hours without network, then reconnecting: no lost and no duplicated
   runs.
5. With remote access set to viewer, a Dagu Cloud admin cannot start, retry,
   stop, or edit, and cannot reach user, API key, license, terminal, or
   remote access settings.
6. A local admin turning remote access off ends the next remote request with
   `403`, and cancels any in flight.
7. An assertion for one server is rejected by another; expired, replayed,
   wrong-audience, and license tokens are rejected.
8. No inbound port is opened. Outbound traffic goes only to the console and
   relay hosts over 443.

## Alternatives considered

- **A separate connect command.** Two steps for one outcome. The license
  already binds a server to a workspace.
- **Remote nodes across the internet.** Needs inbound reachability and a hub
  holding every server's credential.
- **VPN or Tailscale.** The client joins a network and grants network-level
  access, not role-capped access to Dagu.
- **Reports over the relay WebSocket.** One connection for everything, but
  every reporting server would hold a socket that can carry requests, whether
  or not remote access is on.
- **A Dagu Cloud UI over the remote API.** A second UI codebase, and version
  skew against every server it shows.

## Consequences

- Fewer servers report than with reporting on by default; in exchange, no
  server sends anything its admin did not choose.
- Calls from Dagu Cloud are new attack surface. They are bounded by the local
  role cap, the MCP tools as their only path, and short-lived assertions
  bound to one server, one request, and one use.
- License verification gains an audience check.

## Resolved questions

1. Remote access ships in a v2 minor, v2.22, rather than waiting for v3.0.
