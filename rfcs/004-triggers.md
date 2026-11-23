# RFC 004: Triggers

- **Status:** Proposed; its first release, public URLs for per-DAG webhooks,
  is specified below
- **Date:** 2026-10-09, first release added 2026-10-14, sources split from
  triggers 2026-11-23
- **Affected areas:** `spec`, `eventstore`, `service/scheduler`,
  `service/frontend` (API, UI), `auth` (webhooks), `secret`, `tunnel`,
  `gitsync`, and `cmn/config`

## Summary

Let a DAG start when something happens: a GitHub issue opens, a Stripe invoice
is paid, another DAG finishes, any system posts to a URL, or a command finds
new work.

Events come from sources. A source that needs settings, such as a webhook URL
with its signing secret or a command that checks for work, is its own file in
`sources/`. A DAG subscribes with a trigger that names the source, the events
it takes, an optional condition, and the params it maps from the event. Every
event, whatever its source, goes through the event store and one matcher. The
matcher starts at most one run per event and trigger, so one event starts one
run in each DAG that subscribes.

Push sources receive webhooks. A server can receive them on its own URL. A
server connected to Dagu Cloud can also get a public URL that works behind a
firewall. Dagu Cloud then holds deliveries while the server is unreachable.
Signatures are verified on the server, so provider secrets never leave it.

Pull sources check for work on an interval. [RFC 019](019-poll-for-work.md)
defines the first one, `command`.

## Problem statement

The per-DAG webhook (`POST /webhooks/{fileName}`) runs a DAG on any request
that carries its token. That is not enough to react to third-party services:

- **Authentication.** The token must be in `Authorization`. GitHub, Stripe,
  and most services cannot set request headers.
- **Signatures.** It checks Dagu's own `X-Dagu-Signature` only, not GitHub's
  `X-Hub-Signature-256`, Stripe's `Stripe-Signature`, or Slack's
  `X-Slack-Signature`.
- **Filtering.** Every request starts a run. A GitHub repository sends dozens
  of event types; each becomes a run that exits early.
- **Duplicates.** Senders retry, and each retry starts another run.
- **Payload size.** The body reaches the run as one environment variable,
  `WEBHOOK_PAYLOAD`, which fails for large bodies on some platforms
  (dagucloud/dagu#3020).
- **Reachability.** The server must accept requests from the internet. Most
  on-prem servers cannot.

There is also no way to start a DAG when another DAG reaches a status without
polling from a scheduled DAG. Work that sends no event at all is found by a
scheduled DAG whose every check is a run ([RFC 019](019-poll-for-work.md)).

Each per-DAG webhook belongs to one DAG. A repository that three DAGs react to
needs three webhooks set up in GitHub, each with its own URL and secret.

## Goals

1. One model for every source: declare, subscribe, match, map, start.
2. At most one run per event and trigger, despite sender retries and
   redelivery.
3. Verify each sender's own signature scheme.
4. Work behind a firewall without inbound ports, when connected to Dagu Cloud.
5. Keep provider secrets on the server.
6. Show every delivery: verified or not, matched or not, and the run it
   started.
7. One URL or one check per source, however many DAGs subscribe.

## Non-goals

- Exactly-once execution. A run can still fail and be retried as today.
- An event archive. Events follow the event store's retention.
- Binary webhook bodies. Bodies are UTF-8 text, as most senders send.
- Message brokers (Kafka, RabbitMQ) as sources. A later RFC can add them as
  source types.
- Folders. `watch` ([Spec 082](../specs/082-watch.md)) stays a DAG field. A
  `folder` source type can join when one folder must feed several DAGs.

## Sources and triggers

A source produces events. A trigger in a DAG names a source and starts runs
from its events. A source does not know which DAGs subscribe to it.

```text
sources/app-repo.yaml ──▶ event ──┬─▶ triage.yaml     triggers: [source: app-repo]
                                  └─▶ changelog.yaml  triggers: [source: app-repo]
```

### Source files

A source that needs settings is a YAML file named `<name>.yaml` in `sources/`,
directly in the DAG directory, as `wiki/` is. Under
[RFC 003](003-projects.md), each project's DAG directory has its own. Source
files travel with the DAGs through git sync and deploys, and DAG discovery
never reads them as DAGs.

```yaml
# sources/app-repo.yaml
type: github
secret: GITHUB_WEBHOOK_SECRET
public: true
```

| Field | Meaning |
| --- | --- |
| `type` | Required. Push: `webhook`, `github`, `stripe`, or `slack`. Pull: `command` ([RFC 019](019-poll-for-work.md)). |
| `description` | Text shown in listings. |
| `enabled` | Defaults to `true`. A disabled source receives and checks nothing. |
| `secret` | Push types. The name of a Dagu secret holding the sender's signing secret. |
| `public` | Push types. Also receive through Dagu Cloud. Ignored when the server is not connected. |

- **Name.** Letters, digits, `-`, and `_`. The name is the source's identity:
  its URL, its state, and the triggers that name it. Renaming the file makes
  a new source.
- **Implicit sources.** Dagu produces some events itself. Their names, `dag`
  today, are reserved, and a file with such a name is reported as invalid.
- **Types.** Each type has a schema, a validator, and either an ingress
  (push) or a check (pull), so a new type adds no field to DAGs or triggers.
  The JSON schema of source files is served at `/assets/source.schema.json`
  and shown by `dagu schema source`.
- **Subscribers.** A push source has one URL and a pull source runs one check,
  however many triggers name it. A pull source that no enabled trigger of an
  unsuspended DAG names is not checked. A push source with no subscriber
  accepts deliveries and records them as unmatched.

### Triggers

A trigger lives in the DAG, so the DAG says what starts it:

```yaml
# triage.yaml
triggers:
  - name: bug-triage
    source: app-repo
    events: issues.opened
    condition: '.issue.labels[]?.name == "bug"'
    params:
      TITLE: .issue.title
      URL: .issue.html_url
```

| Field | Meaning |
| --- | --- |
| `name` | Unique within the DAG. Defaults to the source's name. |
| `source` | A source file's name, or an implicit source. |
| `events` | Event name, or a list. A name also takes the events under it, so `issues` takes `issues.opened`. Omitted means every event of the source. |
| `condition` | A gojq expression over the event payload; the trigger matches when it yields `true`. |
| `params` | Param name to gojq expression over the payload. |
| `dag`, `status` | For `source: dag`: the DAG whose runs to watch and the statuses that count. |
| `enabled` | Defaults to `true`. |

- **Validation.** An invalid trigger, including one that names a missing or
  invalid source, is shown in the UI and `dagu validate`, and is ignored by
  the matcher. The rest of the DAG is unaffected.
- **Credentials stay out of YAML.** Inbound URL tokens are generated by the
  server, stored hashed in a credential store keyed by source, and rotated
  from the UI.
- **Where it runs.** A triggered run is placed like any other run, including
  `worker_selector`.

### What the run receives

- Mapped `params`, as params.
- `DAGU_EVENT_FILE`: the path to a JSON file with the source, event name,
  event ID, receipt time, selected headers, and the full payload. This is how
  large payloads reach a step.
- Run metadata records the trigger name, source, event ID, and, for push
  sources, the delivery ID. The run's trigger type is `trigger`, separate from
  manual, schedule, and webhook.

## Source types

| Type | Verification | Event ID (for duplicates) | Event name |
| --- | --- | --- | --- |
| `github` | `X-Hub-Signature-256`, HMAC-SHA256 of the raw body | `X-GitHub-Delivery` | `X-GitHub-Event` plus `.action`, e.g. `issues.opened` |
| `stripe` | `Stripe-Signature`, HMAC over `t.body`, 5-minute tolerance | `.id` | `.type`, e.g. `invoice.paid` |
| `slack` | `X-Slack-Signature`, `v0` HMAC over `v0:ts:body`, 5-minute tolerance | `.event_id` | `.event.type` |
| `webhook` | Token in the URL path, or `Authorization`; optional HMAC: `X-Dagu-Signature` or Standard Webhooks | `Idempotency-Key` or `webhook-id` header when present; otherwise the delivery ID | `webhook` |
| `command` | none; the check runs on the scheduler host | Hash of the source and the item's key | `item` |
| `dag` (implicit) | none | The DAG-run event ID | The event store's `dag.run.*` types |

- **Timestamps are checked against receipt time,** not the time the server
  processes the event, because a delivery through Dagu Cloud can wait days.
- **Handshakes.** Slack's `url_verification` and a `GET` with `hub.challenge`
  are answered at once with the challenge, and start nothing.

## Ingress

### Local

```text
POST {base}/api/v1/sources/{source}/{token}
```

- The endpoint needs no login. The token in the path and the source's
  signature authenticate it. Under RFC 003, the path names the project before
  the source, as per-DAG webhook URLs do.
- It answers `202 {"id": "<delivery id>"}` once the event is persisted, before
  matching. Senders want a fast answer.
- Body limit: `webhooks.max_payload_size`. Each source allows 60 requests a
  minute.

### Dagu Cloud

```text
Sender ── POST https://hooks.dagu.sh/<object id>/<token> ──▶ Dagu Cloud relay
          ◀── 202 at once; the raw request is kept up to 7 days
Relay ─── delivery frame over the server's outbound WebSocket ──▶ Dagu server
          ◀── delivered, once the event is persisted
```

- **Same connection as remote access** (RFC 002). The `dagucloud` tunnel
  provider runs when remote access is on, or when any source is `public`.
  With remote access off, the server answers every proxied request with `403`
  and handles only delivery frames.
- **Frames:** `hooks`, `hooked`, `delivery`, and `delivered`, unchanged from
  the relay's existing webhook contract. `hooks` replaces the whole set. A
  hook ID names the source; the built-in webhook source keeps
  `webhook/<webhook ID>`.
- **The token is made by the server** (32 random bytes). The relay keeps only
  its SHA-256.
- **Raw bytes and headers pass through unchanged,** so the server verifies
  the sender's signature with a secret only the server holds.
- **Relay limits** (enforced by the relay today): 512 KiB bodies, UTF-8 only,
  16 KiB of headers, 60 requests a minute per URL, and 1,000 waiting
  deliveries per server. Senders get `413`, `415`, `431`, `429`, or `503`
  past them.

## Matching

```text
push: local ingress ─┐
push: cloud delivery ┴─▶ verify ─┐
pull: check (RFC 019) ───────────┼─▶ persist event ─▶ matcher ─▶ enqueue, one run
implicit: dag.run.*  ────────────┘   (kind: trigger)  (scheduler)  per trigger
```

- **Persist first.** Each push ingress verifies, then writes an event of kind
  `trigger` to the event store, with the payload in a file beside it. Events
  that fail verification are recorded as rejected deliveries and never
  matched. A pull check writes an event for each new item it finds.
- **One matcher.** It runs in the scheduler, reading the event store by cursor
  like `chatbridge.NotificationMonitor`. It holds a cross-process lease and
  persists its cursor.
- **For each event,** the matcher finds the enabled triggers that name its
  source and take its event name, evaluates each condition with a 100 ms
  bound, maps params, and enqueues one run per matching trigger.
- **The run ID is derived** from the event ID, DAG, and trigger. An event seen
  twice, from a sender retry, a redelivery, or a pull source that lost its
  state, maps to a run that already exists, and nothing new starts.
- **Waiting.** While a trigger's DAG is suspended or has 100 or more queued
  runs, the matcher keeps the event waiting for that trigger, goes on with the
  other triggers, and offers it again a minute later.
- **Retention.** Triggers make the event store an input of execution. When
  any trigger exists and `event_store.retention_days` is not set, retention is
  7 days instead of 1. The matcher must stay within it; events older than that,
  waiting ones included, are lost and counted as missed.

## Building a trigger in the UI

Writing gojq is not required. On a DAG, **Add trigger**:

1. Pick a source, or create one: its type, then its settings.
2. Pick events from a list: the sender's documented events for `github`,
   `stripe`, and `slack`; the events seen in recent deliveries for `webhook`.
3. For a new push source, copy its URL: the local one, and the public one
   when the server is connected to Dagu Cloud. For a preset type, Dagu
   generates the signing secret to paste into the sender and stores it as a
   Dagu secret.
4. Click **Send test**, or wait for a real delivery.
5. Click a field in the delivery to add a condition (equals, contains,
   exists) or to map it to a param. The result against that delivery shows as
   each one is added.

The UI writes the trigger into the DAG's YAML and a new source into
`sources/`. Both stay editable for anything the builder does not cover.

## Deliveries view

Each push source shows its recent deliveries:

| Column | Content |
| --- | --- |
| Received | Receipt time, and whether it came through Dagu Cloud |
| Verified | Yes, or the reason it was rejected |
| Triggers | Each subscribing trigger: matched, waiting, or which check failed (event name or condition) |
| Runs | Links to the runs it started |

- A DAG's page shows the deliveries of the sources its triggers name.
- **Replay** re-enqueues a delivery for one trigger with a new run ID and
  marks the run as a replay.
- **Send test** posts the source's sample payload.
- **Editing YAML by hand** shows the result of the condition and params
  against the last delivery as they are typed.
- A pull source shows its log of checks instead ([RFC 019](019-poll-for-work.md)).

## Existing per-DAG webhooks

In v3.0, the per-DAG webhook becomes a built-in `webhook` source that belongs
to its DAG. It needs no source file, and only its DAG subscribes to it:

- `POST /webhooks/{fileName}`, its tokens, HMAC modes, and profile tokens keep
  working unchanged.
- Its requests go through the event store and appear in Deliveries.
- Runs get `DAGU_EVENT_FILE`. `WEBHOOK_PAYLOAD` and `WEBHOOK_HEADERS` remain
  set while the body fits in an environment variable.

## Security

- Payload-derived params are untrusted: anyone who can open a GitHub issue
  controls `TITLE` above.
  - **To verify before shipping:** how `${PARAM}` in a step's `command` is
    expanded today. If Dagu substitutes the value into the command text
    before a shell parses it, payload-derived params allow command injection.
  - In that case, triggered params must reach steps only as environment
    variables, and the docs and the UI must say so.
- Provider secrets are Dagu secrets on the server. Neither Dagu Cloud nor the
  relay ever sees them.
- Conditions run in gojq with a time bound and no I/O.
- Inbound endpoints are rate limited per source, locally and at the relay.
- A source file is trusted as a DAG file is: a `command` source runs on the
  scheduler host. Whoever can edit DAG files can edit source files.

## Failure behavior

| Situation | Behavior |
| --- | --- |
| Server offline, `public` source | The relay answers `202` and keeps deliveries up to 7 days; the server takes them in order on reconnect. |
| Offline longer than 7 days | The relay drops expired deliveries and reports their count, which Deliveries and Dagu Cloud show. |
| Scheduler down | Events persist; the matcher catches up within event store retention. |
| Signature invalid | `401` locally. Through Dagu Cloud: `202` from the relay, then a rejected delivery on the server. |
| Condition errors or times out | The delivery shows the error; nothing starts. |
| Same event delivered twice | The derived run ID already exists; nothing new starts. |
| Trigger names a missing or invalid source | The trigger is invalid; nothing starts from it. |
| Subscribing DAG suspended, or 100 runs queued | The event waits for that trigger; other triggers go on. |

## First release: public URLs for per-DAG webhooks

A public URL is useful on its own, before sources, conditions, the matcher,
and the builder. The first release gives each existing per-DAG webhook an
optional public URL through Dagu Cloud. The trigger model follows with the
first source type that needs it.

### Turning it on

- Off by default. An admin turns it on per webhook, and the audit log records
  it. The server must be connected to Dagu Cloud.
- Turning it on makes a 32-byte token. The webhook store keeps it encrypted,
  like the HMAC secret, so the page can show the URL again. Dagu Cloud gets
  only its SHA-256. **New URL** replaces it; requests Dagu Cloud already
  took through the old URL are still delivered. Turning the URL off drops
  them.
- The hook ID is `webhook/<webhook ID>`, so it stays the same when the DAG
  is renamed: the webhook moves with the DAG, and its URL and anything
  waiting for it survive the rename. The built-in webhook source keeps
  this ID when triggers arrive, so URLs survive that move too.
- The local endpoint keeps its token and `X-Dagu-Signature` checks.

### Sender and events

- **Sender:** anyone with the URL (the default), GitHub, Stripe, Slack, or
  Standard Webhooks. The sender's secret is encrypted at rest. Timestamps are
  checked against the time Dagu Cloud received the request, within 5
  minutes, since a delivery can wait days. For GitHub, Dagu makes the secret.
- **Slack** has Dagu Cloud answer its URL check, which starts no run.
- **Events:** an optional list of event names. A name also takes the events
  under it, so `issues` takes `issues.opened`; an empty list takes every
  event. The name is GitHub's `X-GitHub-Event` and `.action` joined by a dot,
  the body's `type` for Stripe and Standard Webhooks, and the wrapped
  event's type for Slack.
- GitHub's `ping`, sent when a webhook is created, never starts a run.

### Connection

- The scheduler that holds the scheduler lock holds the connection, so one
  process per server connects. It connects only while the server is
  connected to Dagu Cloud and some webhook is public, or Dagu Cloud still
  holds URLs since turned off, which the server then drops.
- It asks Dagu Cloud for a five-minute ticket with the license's
  credentials (`POST /api/v1/servers/relay`), which also names the most
  public URLs the plan allows, and connects to `wss://relay.dagu.sh/server`.
  Every connection uses a fresh ticket.
- Reconnects back off from 1 second to 1 minute. A connection closed with
  reason `dropped` means Dagu Cloud disconnected the server; a refused ticket,
  or one that allows none of the public URLs, waits 10 minutes before the
  next try.

### Hook set

- The scheduler rereads the webhook store every 10 seconds and sends `hooks`
  when the set changes, so turning a URL off takes effect within 10 seconds.
- A new set makes Dagu Cloud delete every waiting delivery whose hook it
  leaves out. So the set is sent only from a complete read: never before the
  first read succeeds, and never from a read that failed or could not decode
  a record. On such a read the last set stays.
- It holds at most as many URLs as the ticket allows, the oldest first; the
  page shows the others as over the plan's limit.

### A delivery

| Situation | Answer | Logged as |
| --- | --- | --- |
| Hook unknown, webhook removed, disabled, or not public | `ignored` | `ignored` |
| Sender chosen and signature missing or invalid | `ignored` | `rejected`, with the reason |
| GitHub's `ping`, or an event the list leaves out | `ignored` | `skipped` |
| Body larger than `webhooks.max_payload_size` | `failed` | `failed` |
| 100 runs already queued on the DAG's queue | `retry` after 60 seconds | `waiting` |
| DAG fails to load or validate | `failed` | `failed`, with the error |
| Storage error while enqueueing | `retry` after 30 seconds | `waiting` |
| Enqueued, or a run with the delivery ID already exists | `started` | `started`, with the run |

- The run ID is the delivery ID, so a delivery Dagu Cloud sends again starts
  one run; a request the sender sends again is a new delivery.
- The run's trigger type is `webhook`, and it uses the DAG's default
  profile.

### What the run receives

- `WEBHOOK_PAYLOAD` is the body as it came, whatever its content type. It is
  not read as the local endpoint's JSON request.
- `WEBHOOK_HEADERS` holds the headers named in `webhook.forward_headers`, as
  for local requests.
- `WEBHOOK_PAYLOAD_FILE` is the path of a file holding the body, written by
  the process that runs the DAG, so it works on workers too.
  `WEBHOOK_PAYLOAD` stays set only while the body fits in one environment
  variable where the run executes (128 KiB on Linux and macOS, 32,767
  characters on Windows). Local requests get the same.

### Deliveries and data

- The scheduler keeps each public webhook's latest 20 deliveries: arrival,
  size, event, how it was logged, and the run or the error. A delivery Dagu
  Cloud sends again replaces its earlier entry. Bodies are not kept there.
- It also keeps the counts Dagu Cloud reports of deliveries that waited longer
  than 7 days or were refused while 1,000 waited, until an admin dismisses
  them.
- Webhook bodies wait at Dagu Cloud for up to 7 days.

## Compatibility and rollout

| Release | Ships |
| --- | --- |
| v2.20 | Public URLs for per-DAG webhooks (above). |
| Next | Source files, triggers, the matcher, `DAGU_EVENT_FILE`, and 7-day event retention when triggers exist, with `command` ([RFC 019](019-poll-for-work.md)) as the first source type. |
| Later | The `webhook` type and the `dag` source; local and public ingress per source; the UI builder and Deliveries. |
| Later | `github`, `stripe`, and `slack` types. |
| v3.0 | The per-DAG webhook becomes a built-in `webhook` source. |

Public ingress ships first because receiving webhooks behind a firewall is
useful to a single server; it does not wait for remote access. The model
ships with `command` because work that sends no event has the most empty runs
today.

`sources/` is a new folder in the DAG directory. An older binary ignores it
with flat discovery, and with recursive discovery lists its files as invalid
DAGs. A DAG directory that already keeps DAGs in a `sources/` folder must
rename it; the new binary reports those files as invalid sources.

## Verification and acceptance criteria

1. A GitHub `issues` event with the label `bug` starts one run with `TITLE`
   and `URL` set; one without the label starts nothing and shows
   "condition false".
2. GitHub redelivering the same event starts nothing new.
3. A bad signature is rejected and never matched.
4. With the server offline for an hour, `public` deliveries arrive in order on
   reconnect, each starting one run.
5. Two DAGs subscribe to one `github` source: GitHub is set up with one URL,
   and one delivery starts one run in each DAG.
6. A 400 KiB payload reaches a step through `DAGU_EVENT_FILE` on Linux, macOS,
   and Windows.
7. An existing per-DAG webhook URL keeps working after the upgrade to v3.0.
8. A payload crafted with shell metacharacters runs no command.
9. A trigger that names a missing source fails `dagu validate` and starts
   nothing.
10. With one of two subscribing DAGs suspended, a delivery starts a run in the
    other at once, and in the suspended one when it resumes.

## Alternatives considered

- **A separate relay service with long polling** (the earlier
  `DESIGN_EVENT_TRIGGER.md` draft): a second relay, a cluster to run it in,
  and webhook bodies in Cloud SQL. The existing relay already stores and
  delivers webhooks over a socket the server holds.
- **Triggers in a store, edited only in the UI.** They would not travel with
  the DAG through git sync or deploys.
- **Verifying signatures in Dagu Cloud.** Requires giving Dagu Cloud every
  provider secret.
- **Keeping the per-DAG webhook separate.** Two concepts for one job, and its
  requests would stay out of Deliveries.
- **Source settings inside each trigger** (the first draft of this RFC). A
  repository that three DAGs react to would need three URLs and three secrets
  in GitHub, and three DAGs reading one listing would run three checks.
- **Sources that name the DAGs they start.** Adding a DAG would edit a shared
  file, and a DAG would no longer say what starts it.
- **A `kind` field that marks source files anywhere in the tree.** Discovery
  would parse every file to tell sources from DAGs; a folder needs no parse.

## Consequences

- Third-party services can start DAGs without a shim, on-prem included.
- The event store becomes an input of execution, not only a record of it.
  Its retention, 7 days by default once triggers exist, bounds how long the
  scheduler can be down without missing events.
- Dagu Cloud's relay carries deliveries for servers that never enable remote
  access.
- A source is a new kind of file with its own page and state. Renaming it
  makes a new source, with a new URL and no memory of the items it has seen.

## Open questions

1. Is 512 KiB enough? Large GitHub `push` payloads can exceed it.
2. Should `public` default to `true` on connected servers?
3. Should replay need a separate permission from running the DAG?
4. Should the server layer of RFC 003 hold sources that every project can
   subscribe to?
