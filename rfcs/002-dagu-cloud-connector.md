# RFC 002: Dagu Cloud Connector

- **Status:** Proposed
- **Date:** 2026-10-09
- **Affected areas:** `license`, `eventstore`, `tunnel`, `serviceregistry`,
  `service/frontend` (auth, API, UI), `audit`, `cmn/config`, and `cmd`

## Summary

Let a Dagu server with an online license report its health and run status to
Dagu Cloud, and let the server's administrator open it to remote access from
Dagu Cloud without inbound ports, VPN, SSH, or remote desktop.

Activating an online license is the connection. There is no separate connect
command. Reporting starts with the license and sends metadata only. Remote
access is off until an administrator of the server turns it on, on the server
itself, and it never exceeds the role they choose.

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
2. **Metadata by default.** Health, version, workflow list, and run status
   transitions. No logs, outputs, parameters, or secrets.
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
   │ open /servers/connect?code=sha256(secret)&server_id=…&name=… ───────────▶ │
   │                                          │ ◀── sign in, pick workspace ── │
   │                                          │ ◀── approve ────────────────── │
   │ POST /api/v1/licenses/connect {connection_secret, server_id} (every 3 s)  │
   │ ◀── {status: pending} … {status: granted, token, heartbeat_secret}        │
   │ persist activation (as `license activate` does today)                     │
```

The approval page states what the server will report. Approvals expire after
15 minutes, as the console enforces today.

### Disconnecting

**Disconnect** on the license settings page calls `POST /api/v1/licenses/release`,
clears the activation, and stops reporting and remote access at once. Disconnecting
from the console has the same effect on the next report (`401`).

## Reporting

### What is sent

| Part | Fields | When |
| --- | --- | --- |
| Health | Dagu version, OS and architecture, process start time, services from the service registry (scheduler, coordinator, workers: count and last heartbeat), queue depth per queue, event lag, remote access level | Every report |
| Inventory | Per DAG: name, schedules (cron and time zone), suspended, last run status and time | When its hash changes, and at least daily |
| Runs | Per `dag.run.*` event: event ID, type, DAG name, DAG-run ID, attempt ID, status, `occurred_at`, and `run_created_at` (when the DAG run was created, the same in every event of the run and its retries); on terminal events, failed step names and exit codes | Batched with each report |

**Never sent:** logs, outputs, parameters, environment, step commands, DAG YAML,
secret values, and error message text. Error text is opt-in
(`cloud.report_error_messages`) because it often contains hosts, paths, or
data.

DAG names and step names are sent. An administrator who considers them
sensitive turns reporting off.

### Cadence

The server reports every 60 seconds, and within 5 seconds of a `failed`,
`aborted`, `rejected`, or `waiting` event. Dagu Cloud can lengthen the interval
in its response (`next_report_seconds`) to shed load.

Many workflows run at the top of the hour, so reports are spread out:

- the first report waits a random 0 to 60 seconds after start;
- each interval varies randomly by up to 10 percent;
- an early report after a failure waits a random 0 to 5 seconds.

### Where it runs

The reporter runs in the process that holds the license manager (`server` and
`start-all`, `internal/cmd/context.go`). It is an event store cursor consumer
built like `chatbridge.NotificationMonitor`:

- a cross-process lease, so one process reports when several share a data
  directory;
- a persisted cursor, advanced only after Dagu Cloud acknowledges it;
- `ReadDAGRunEvents` from the persisted cursor
  (`eventstore/dagrun_events.go`).

Event IDs are deterministic (`DAGRunEventID`), so a resent batch is harmless:
Dagu Cloud de-duplicates by ID.

**Retention.** The event store keeps one day by default
(`event_store.retention_days`). A server cut off for longer loses the events in
between. The reporter detects a cursor older than retention and reports a gap;
Dagu Cloud shows the gap rather than guessing runs. If the event store is
disabled, reports carry health and inventory only.

### Wire contract

`POST {license.cloud_url}/api/v1/servers/report`, authenticated with the
activation's credentials, as the heartbeat is.

```json
{
  "protocol": 1,
  "license_id": "…",
  "server_id": "…",
  "heartbeat_secret": "…",
  "health": { "version": "3.0.0", "os": "linux", "arch": "amd64",
              "started_at": "2026-10-09T12:00:00Z",
              "services": [{ "name": "scheduler", "instances": 1 },
                           { "name": "coordinator", "instances": 0 }],
              "queues": [{ "name": "default", "queued": 3, "running": 1 }],
              "event_lag_seconds": 2, "remote_access": "off" },
  "inventory": { "hash": "…", "dags": [] },
  "events": [],
  "gap": null,
  "cursor": "opaque"
}
```

`inventory` is omitted when unchanged. A scheduler count of zero means no
scheduler holds the lock; leaving `services` out means the count is unknown.
The response is:

```json
{ "ack": "opaque", "next_report_seconds": 60, "inventory_wanted": false }
```

| Response | Reporter behavior |
| --- | --- |
| `200` | Persist `ack` as the cursor. Send inventory next time if `inventory_wanted`. |
| `401`, `410` | Stop. The license manager handles the activation as it does for heartbeats. |
| `413` | Halve the batch and retry. |
| `429` | Wait for `Retry-After`, keeping the cursor. |
| `5xx`, network error | Back off exponentially up to 5 minutes, keeping the cursor. |

New fields are optional in both directions. Dagu Cloud accepts every protocol
version a supported Dagu release sends.

## Remote access

### Shape

```text
 Browser ──HTTPS──▶ Relay (per-server origin) ──▶ Relay object for this server
                                                         ▲
                                                         │ WebSocket, opened by the server (wss, 443)
                                                         │
                            Dagu server: dagucloud tunnel provider ──▶ HTTP handler (in process)
```

- A `dagucloud` provider joins `tailscale` behind `tunnel.Provider`. It runs
  while the license is online and either remote access is on or a trigger is
  `public` (RFC 004). With remote access off, it answers every proxied request
  with `403` and carries only webhook deliveries. It reports its state through
  `GET /services/tunnel`.
- The provider receives the server's HTTP handler, not its listen address.
  Tunnel requests never pass through the public listener, and the public
  listener never accepts Dagu Cloud assertions.
- The server gets a relay ticket from `POST /api/v1/servers/relay` with its
  activation credentials and reconnects with a fresh ticket when the relay
  closes the connection.

### Turning it on

Remote access is set on the server, never from Dagu Cloud. The level is the
highest role any remote user gets.

- **UI.** The license settings page has a **Remote access** control for the
  server's admins: off, viewer, operator, developer, manager, or admin. It
  starts off. A change applies at once and is persisted in the data directory;
  turning it off closes the tunnel and cancels requests in flight.
- **Config.** For servers managed as code, `cloud.remote_access` fixes the
  level, and the UI shows it read-only.

```yaml
cloud:
  report: true                   # health, workflow list, run status
  report_error_messages: false
  remote_access: off             # off | viewer | operator | developer | manager | admin
                                 # omit to set it in the UI
```

A `cloud:` identity can never change the level (see below).

### Identity

Each request from the relay carries an assertion minted by Dagu Cloud:

| Claim | Value |
| --- | --- |
| `iss` | Dagu Cloud |
| `aud` | `dagu-server` |
| `server_id` | This server's ID; any other value is rejected |
| `sub`, `email` | The Dagu Cloud user |
| `workspace` | The Dagu Cloud workspace name, for display and audit |
| `role` | The Dagu role Dagu Cloud grants this user on this server |
| `exp` | At most 15 minutes after `iat` |
| `jti` | Unique; replays inside the validity window are rejected |

- Verified with the pinned Dagu Cloud key (`license/pubkey.go`), with 60
  seconds of clock skew.
- **Effective role** = the lower of `role` and `cloud.remote_access`.
- The request runs as a synthetic user `cloud:<sub>`, like API key users
  (`apikey:<id>`). No local user is created.
- Audit entries use credential type `cloud` and record the email and workspace.
- The UI served through the tunnel starts signed in as that identity and shows
  a bar: *Opened from Dagu Cloud · workspace · role*. When the assertion
  expires, the UI reloads to obtain a new one.

**Key hygiene.** Dagu Cloud signs licenses, relay tickets, and assertions with
one key and separates them by audience. License verification today checks no
audience (`license/verify.go`). The assertion verifier requires
`aud = dagu-server`, and license verification is changed in the same release to
reject tokens that carry any audience other than its own.

### What remote users cannot do

Regardless of role, a `cloud:` identity cannot:

- open a terminal;
- manage users, API keys, SSO, the license, remote nodes, or `cloud.*`
  settings.

Any of these would let Dagu Cloud widen or keep its own access.

### Relay frames

JSON control frames and binary data frames over one WebSocket. Names follow
the existing relay where they apply.

| Direction | Frame |
| --- | --- |
| Server → relay | `hello{protocol, version, server_id}` |
| Relay → server | `req{id, method, path, headers, assertion}` |
| Relay → server | binary request body chunks for `id`, then `end{id}` |
| Server → relay | `res{id, status, headers}` |
| Server → relay | binary response body chunks for `id`, then `end{id}` |
| Either | `cancel{id}` |
| Server → relay | `"ping"` every 15 seconds; the relay answers `"pong"` |
| Both | `hooks`, `hooked`, `delivery`, `delivered` for public triggers (RFC 004) |

Streaming responses carry server-sent events and live logs. Limits are set by
the relay and announced in its reply to `hello`.

## Failure behavior

| Situation | Behavior |
| --- | --- |
| Dagu Cloud unreachable | Workflows unaffected. Reports back off and resume from the cursor. License grace applies as today. |
| Unreachable longer than event retention | The next report carries a gap. |
| Relay unreachable | The tunnel reconnects with backoff. Remote users see the server as offline. |
| License revoked or server disconnected | Reporting and the tunnel stop at once. |
| Remote access turned off | The tunnel closes, unless public triggers keep it open for deliveries; requests in flight are cancelled. |
| Assertion invalid, expired, replayed, or for another server | `401`; nothing runs. |

## Observability

- **License settings page:** connected workspace, last report, reporting
  state, tunnel state, remote access level.
- **Metrics:** reports sent and failed, event lag, tunnel state, remote
  requests by status.
- **Audit:** every state-changing remote request, and every remote session
  opened.

## Compatibility and rollout

| Release | Ships |
| --- | --- |
| v2.19 | Browser approval; reporting; the `cloud` config section. Dagu Cloud's report endpoint must be live first. |
| v2.20 | The `dagucloud` tunnel provider, carrying webhook deliveries for public triggers only (RFC 004). |
| v3.0 | Remote access over the same connection: assertions, the Remote access control, the UI bar. License audience check. |

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

- A licensed server sends metadata to Dagu Cloud by default. This is stated at
  approval, documented, and switchable.
- The tunnel ingress is new attack surface. It is bounded by the local role
  cap, blocked admin endpoints, short-lived server-bound assertions, and a
  separate origin per server.
- License verification gains an audience check.

## Open questions

1. Should reporting default to on for activations made before v2.19, or wait
   for an admin to turn it on?
2. Is error text worth sending by default, for more useful alerts?
3. Should remote access ship in a v2 minor rather than wait for v3.0?
