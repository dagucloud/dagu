# RFC 004: Triggers

- **Status:** Proposed. Its first release, public URLs for per-DAG webhooks,
  shipped in v2.20 and is specified below.
- **Date:** 2026-10-09, first release added 2026-10-14, revised 2026-11-23 to
  define sources and absorb RFC 019
- **Affected areas:** `spec`, `ir`, `eventstore`, `persis`, `schedulerstate`,
  `service/scheduler`, `service/frontend` (API, UI), `auth` (webhooks),
  `secret`, `tunnel`, `gitsync`, `cmd`, `cmn/config`, `specs`, and `docs`

## Summary

Let a DAG start when something happens outside it: a command finds new work,
another DAG finishes, or a service such as GitHub sends a webhook. A check that
finds nothing, and an event no DAG takes, start no run.

Three parts do this:

- A **source** is where events come from. A source with settings is a YAML
  file in `sources/`, beside the DAGs: a command the scheduler runs on an
  interval to list work, or a URL that receives webhooks. DAG runs are a
  built-in source.
- A **trigger** in a DAG names a source, and says which of its events start
  the DAG, on what condition, and with what params.
- The **matcher** runs in the scheduler. It reads each new event and queues
  one run for each trigger that takes it.

```yaml
# sources/feed-jobs.yaml
type: command
command: ssh cloud-vm 'ls ~/feed/jobs'
interval: 1m
```

```yaml
# feed.yaml
triggers:
  - source: feed-jobs
max_active_runs: 3
steps:
  - id: feed
    command: ~/feed/feed-run.sh   # reads the job from $POLL_ITEM
```

An event starts at most one run per trigger, however often it arrives. A
source runs one check, or has one URL, however many DAGs subscribe to it.

Definitions never refer to server state. The data directory keeps only a
source's state, by its name: what a command source has seen, its log of
checks, URL tokens, and whether its public URL is on. A DAG directory
validates and loads alone.

This RFC absorbs RFC 019, Poll for Work Without a Run, as the `command`
source.

## What already ships

A DAG starts today on a schedule, by hand, from a parent DAG, when a file
arrives in a folder it watches (`watch`, [Spec 082](../specs/082-watch.md)),
or when its per-DAG webhook (`POST /webhooks/{fileName}`) is called.

Since v2.20, a per-DAG webhook can also have a public URL through Dagu Cloud
([First release](#first-release-public-urls-for-per-dag-webhooks)). That URL
reaches a server behind a firewall, carries its token in the path so senders
need no header, verifies GitHub, Stripe, Slack, and Standard Webhooks
signatures, takes only the events it lists, hands large bodies over as a file,
and keeps its latest deliveries.

## Problem statement

Three needs are still unmet.

**Work that sends no event.** The usual way to react to it is a scheduled DAG
that checks for work and exits when there is none. Every check is a full run:
a process, a status record, logs, and a work directory.

One showcased setup runs a live video channel on a 1 GB VM
(https://dagu.sh/showcase/live-channel-on-a-1-gb-vm). Five copies of a feed
DAG run every 5 minutes. In 15 days the nine DAGs made 16,029 runs. The feed
DAG made 15,166 of them, and 257 did real work. Most of the rest found no job
and ended in about 2 seconds (RFC 006). Capacity grew by copying the DAG file,
from one copy to five, because a schedule starts runs by the clock, not by
the job.

**Another DAG's outcome.** A DAG cannot start when another DAG reaches a
status. The producer has to call it, as a sub-DAG or from a handler, which
ties the two DAGs together.

**What public URLs leave.**

- A sender's own retry starts a second run. The run ID is Dagu Cloud's
  delivery ID, not the sender's event ID, so GitHub redelivering an event is
  a new delivery.
- No condition on the payload. Taking only issues labelled `bug` costs a run
  that exits early for every other issue.
- No params from the payload. The run gets the whole body.
- One URL starts one DAG. Three DAGs that react to one repository need three
  webhooks set up in GitHub, each with its own secret.
- The local endpoint, without Dagu Cloud, verifies only `X-Dagu-Signature`
  and takes every event.

Current options:

| Option | Fits when | Leaves |
| --- | --- | --- |
| `watch` (Spec 082) | Work arrives as files in a folder the scheduler can read | Nothing to fix |
| Public URL (v2.20) | A service posts to one DAG | The gaps above |
| `discard_run_on` (RFC 006) | Only the run can tell whether there was work, for example by claiming a job atomically | A process start per check, and one poller copy per concurrent job |
| `hist_retention_runs` or `hist_retention_days` | Disk is the only concern | Empty runs in every list, and runs that did work removed with them |
| None of the above | The source can be listed but sends no event: an API without webhooks, a remote queue, a table, a channel's video list | This RFC |

## Prior art

Most orchestrators and automation tools keep polling, but separate the cheap
check from the run it may start.

| Tool | What a check that finds nothing leaves |
| --- | --- |
| Dagster sensors | A tick marked skipped, with an optional `SkipReason`. Ticks are stored apart from runs and purged on their own schedule, per outcome (`retention.sensor.purge_after_days.skipped`). |
| Windmill trigger scripts | The script keeps state between checks; when it returns no new items, the flow is skipped. |
| Zapier polling triggers | The trigger compares item IDs with those it has seen; a Zap runs only for a new ID. |
| Jenkins `pollSCM` | A polling log entry, "No changes". A build starts only when changes are found. |
| n8n polling triggers | No execution is logged when nothing new is found, according to n8n staff; some users report otherwise. |

Event-driven tools remove the check where the source can push:

- Airflow 3 schedules a DAG from a message queue through an `AssetWatcher`
  wrapping a `MessageQueueTrigger` (SQS, Kafka, Redis, and others). Airflow's
  sensor modes (`poke`, `reschedule`, deferrable) free worker slots while
  waiting; they do not reduce the number of DAG runs.
- Prefect deployment triggers start a flow run when an event arrives.
- Temporal workers long-poll a task queue; an empty poll creates no workflow
  execution.

Tools that keep a record per check bound it instead. A Kubernetes CronJob
creates a Job for every schedule and keeps the latest 3 successful and 1
failed Job by default.

The common choice is the first one: the poll is an evaluation, not a run, and
only new items become runs.

## Goals

1. A check that finds no new work, and an event no trigger takes, leave no
   run, no logs, and no work directory.
2. An event starts at most one run per trigger, despite sender retries,
   redelivery, restarts, and lost scheduler state.
3. One check or one URL per source, however many DAGs subscribe.
4. Concurrency comes from `max_active_runs` and queues, not from copies of a
   DAG.
5. Every check and delivery stays visible: what it found, what it matched,
   what it started, and why it failed.
6. Bounded idle cost on the scheduler host.
7. A DAG directory validates and loads without server state.
8. Provider secrets stay on the server.

## Non-goals

- Exactly-once execution. A run can still fail and be retried as today.
- An event archive. Events follow the event store's retention.
- Replacing RFC 006. A run that must claim work atomically still needs it.
- Running checks on workers. See [Where the check runs](#where-the-check-runs).
- Change detection for items whose key stays the same. A new version of an
  item needs a new key.
- Incremental listing. A source too large to list whole, such as a feed read
  from a cursor, needs a later option that passes the last cursor to the
  check. Event IDs do not change with it.
- Binary webhook bodies. Bodies are UTF-8 text, as most senders send.
- Message brokers (Kafka, RabbitMQ) and an `http` pull type. Later RFCs can
  add them as source types.
- Folders. `watch` stays a DAG field. A `folder` source type can join when one
  folder must feed several DAGs.

## Model

```text
 sources/feed-jobs.yaml  the scheduler runs its check ─┐
 sources/app-repo.yaml   a sender posts to its URL ────┼─▶ event store ─▶ matcher ─▶ runs
 dag (built-in)          a DAG run ends ───────────────┘                     ▲
 feed.yaml  triggers: [source: feed-jobs] ───────────────────────────────────┘
```

References run one way: server state names definitions, never the reverse.

### Source files

A source with settings is a YAML file named `<name>.yaml` in `sources/`,
directly in the DAG directory, as `wiki/` is. Under
[RFC 003](003-projects.md), each project's DAG directory has its own. Source
files travel with the DAGs through git sync and deploys, as a git sync item
of their own, and DAG discovery never reads them as DAGs.

| Field | Meaning |
| --- | --- |
| `type` | Required. `command`, `webhook`, `github`, `stripe`, or `slack`. |
| `description` | Text shown in listings. |
| `enabled` | Defaults to `true`. A disabled source checks and receives nothing. |

Each type adds its own fields, given in its section below.

- **Name.** The file name: letters, digits, `-`, and `_`. The server keeps a
  source's state by its name, so renaming the file makes a new source, with a
  new URL and no memory of the items it has seen.
- **Built-in sources.** `dag` is reserved for DAG runs; a file with that name
  is reported as invalid.
- **Types.** Each type has a schema, a validator, and either a check or an
  ingress, so a new type adds no field to DAGs or triggers. The JSON schema of
  source files is served at `/assets/source.schema.json` and shown by
  `dagu schema source`.
- **Subscribers.** A `command` source that no enabled trigger of an
  unsuspended DAG names is not checked. A webhook source with no subscriber
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
| `source` | A source file's name, or `dag`. |
| `events` | Event name, or a list. A name also takes the events under it, so `issues` takes `issues.opened`. Omitted means every event of the source. |
| `condition` | A gojq expression over the event payload; the trigger matches when it yields `true`. |
| `params` | Param name to gojq expression over the payload. |
| `enabled` | Defaults to `true`. |

- **Validation.** An invalid trigger, including one that names a missing or
  invalid source, is shown in the UI and `dagu validate`, and is ignored by
  the matcher. The rest of the DAG is unaffected.
- **Where it runs.** A triggered run is placed like any other run, including
  `queue`, `max_active_runs`, and `worker_selector`.

### What the run receives

- Mapped `params`, as params.
- `DAGU_EVENT_FILE`: the path to a JSON file with the source, event name,
  event ID, receipt time, selected headers, and the full payload. This is how
  large payloads reach a step.
- The params a source type adds, such as `POLL_ITEM` for `command`.
- Run metadata records the trigger name, source, event ID, and, for webhook
  sources, the delivery ID. The run's trigger type is `trigger`, separate from
  manual, schedule, and webhook.

### Matching

```text
command check ──────────────────┐
webhook ingress ─▶ verify ──────┼─▶ persist event ─▶ matcher ─▶ enqueue, one run
dag.run.* ──────────────────────┘   (kind: trigger)  (scheduler)  per trigger
```

- **Persist first.** A command check writes an event for each new item it
  finds. A webhook ingress verifies, then writes an event with the payload in
  a file beside it; a delivery that fails verification is recorded as
  rejected and never matched.
- **One matcher.** It runs in the scheduler, reading the event store by cursor
  like `chatbridge.NotificationMonitor`. It holds a cross-process lease and
  persists its cursor.
- **For each event,** the matcher finds the enabled triggers that name its
  source and take its event name, evaluates each condition with a 100 ms
  bound, maps params, and enqueues one run per matching trigger.
- **The run ID is derived** from the event ID, DAG, and trigger. An event seen
  twice, from a sender retry, a redelivery, or a command source that lost its
  state, maps to a run that already exists, and nothing new starts.
- **Waiting.** While a trigger's DAG is suspended or has 100 or more queued
  runs, the matcher keeps the event waiting for that trigger, goes on with the
  other triggers, and offers it again a minute later.
- **Retention.** Triggers make the event store an input of execution. When
  any trigger exists and `event_store.retention_days` is not set, retention is
  7 days instead of 1. The matcher must stay within it; events older than
  that, waiting ones included, are lost and counted as missed.

## The `command` source

A `command` source runs a command on the scheduler host at an interval and
turns each new line it prints into one event.

### Fields

| Field | Type | Default | Meaning |
| --- | --- | --- | --- |
| `command` | string | required | Command that prints one item per line on stdout |
| `env` | map | none | Variables the command receives |
| `working_dir` | path | the DAG directory | Where the command runs |
| `interval` | duration | `1m` | Time between the end of one check and the start of the next; at least `10s` |
| `timeout` | duration | `30s` | Longest a check may run; at most `5m` |
| `key` | gojq expression | the whole line | When set, each line is parsed as JSON and the key is this expression's string result |
| `backlog` | `skip` or `run` | `skip` | What the first check does with the items it lists |

`dagu validate` rejects a missing command, an interval under `10s`, a timeout
over `5m`, an invalid gojq expression, and an unknown `backlog`.

`sources.allow_command` in `config.yaml` decides whether `command` sources
run. It defaults to `true` when `default_execution_mode` is `local`, where DAG
steps already run on the scheduler host, and to `false` when it is
`distributed`, where DAG authors otherwise run nothing there. A `command`
source that is not allowed is invalid.

### A check

The scheduler runs `command` with the default shell, as a step without
`shell` does, in `working_dir`, with `env`, as the scheduler's user.

- **Output.** Stdout is read up to 1 MiB; each non-empty line is an item, up
  to 10,000 items. Output beyond either limit fails the check. Stderr is kept
  up to 4 KiB for the check log.
- **Exit code.** A non-zero exit or a timeout fails the check. A failed check
  writes nothing and forgets nothing.
- **Keys.** Each item's key is the line, or the result of `key`. A line that
  is not JSON, or a key that is not a non-empty string, fails the check.
- **New items.** An item is new when its key was not printed by the previous
  successful check. Each new item becomes one event, written to the event
  store in the order printed.
- **Overlap.** At most one check per source runs at a time. A check due while
  the previous one runs waits for it.

### The event of an item

- **Event ID.** A hash of the project, the source name, and the key. An item
  listed again after the source's record was lost has the same event ID, so
  the matcher derives the same run IDs and nothing new starts. This is the
  rule `watch` uses for files.
- **Event name.** `item`.
- **Payload.** `{"key": <key>, "item": <item>}`. The item is the line, or,
  when `key` is set, the line parsed as JSON.
- **Run params.** Besides the trigger's params, a run receives `POLL_ITEM`,
  the line, and `POLL_KEY`, the key. They come from the source, so steps read
  them as environment variables rather than interpolating them into commands,
  as Spec 082 says for `WATCH_FILE`.
- **Once per key.** A key that disappears and comes back has the same event
  ID, so it does not run again while that run is in the history. A source
  whose items change under the same key puts a version in the key.

A trigger reads the payload in its condition and params:

```yaml
triggers:
  - source: channel-videos      # with key: .id and one JSON object per line
    condition: '.item.duration > 60'
    params:
      VIDEO_ID: .key
```

### The first check

With `backlog: skip`, the first check remembers what it lists and writes no
event, as `watch` and Zapier do. Changing `command` or `key` counts as a first
check again, and so does renaming the source, which makes a new source. With
`backlog: run`, every listed item is new on the first check. A work queue
wants `run`; a listing of an archive, such as a channel's past videos, wants
`skip`.

### Where the check runs

The check runs on the scheduler host, in the scheduler that holds the
scheduler lock. Only the run that does the work is placed on a worker.

RFC 006 rejected a guard command in the scheduler because the check would run
on the wrong host for distributed workers. A check differs in what it does:
it lists work from a source the scheduler can reach, such as an API, a remote
queue over `ssh`, or a database, and does no work itself. A source only a
worker can read still needs RFC 006.

### Subscribers and suspension

A `command` source is checked while some enabled trigger of an unsuspended DAG
names it. When the last such trigger goes, checks stop. When one comes back,
the next check compares against the last successful check, so items listed
meanwhile become events once. A disabled source is not checked. A source
removed for good keeps its record until the scheduler's state is pruned, as
`watch` records are.

### The check log

Each source keeps its latest 50 checks: start time, duration, exit code,
number of items, the event IDs of new items, which link to the runs they
started, and the stderr excerpt. The log is kept with the scheduler's state,
not in the run history. It is shown on the source's page, and each
subscribing DAG's page links to it.

After 3 failed checks in a row, the source's page and the pages of its
subscribing DAGs show the latest error. A successful check clears it.

### Trying a check

`dagu source check <name>`, and **Check now** on the source's page, run the
check once and show the items it lists, which of them are new, and which
triggers would start a run. They write no event and change no state, so a
command can be tried before any DAG subscribes.

### What is stored

| Record | Size bound | Location |
| --- | --- | --- |
| Keys from the last successful check | 10,000 keys, stored as 16-byte hashes | Scheduler state, per source |
| Check log | 50 entries, 4 KiB of stderr each | Scheduler state, per source |
| Events | One per new item, holding its line | Event store, within its retention |
| Runs | One per new item and subscribing trigger | Run history, as today |

### Example: the feed DAG

The five copies become one source and one DAG. The check lists job files on
the machine that holds the queue; each job runs once, at most three at a time:

```yaml
# sources/feed-jobs.yaml
type: command
command: ssh cloud-vm 'ls ~/feed/jobs'
interval: 1m
backlog: run
```

```yaml
# feed.yaml
triggers:
  - source: feed-jobs
max_active_runs: 3
steps:
  - id: feed
    action: exec
    timeout_sec: 10800
    with:
      command: /bin/sh
      args: ["~/feed/feed-run.sh"]   # takes the job named in $POLL_ITEM
```

Over the same 15 days the history would hold about 257 feed runs instead of
15,166, and the check log would hold the latest 50 checks. A second DAG that
reports each job subscribes to `feed-jobs` and adds no check.

If the jobs were files on the scheduler host, `watch` would already do this.

## Webhook sources

`webhook`, `github`, `stripe`, and `slack` sources receive HTTP requests.

```yaml
# sources/app-repo.yaml
type: github
secret:
  ref: github/app-repo
```

| Field | Meaning |
| --- | --- |
| `secret` | The sender's signing secret, as a [Spec 069](../specs/069-secrets-providers.md) secret entry without `name`: `ref`, or `provider` and `key`. Required except for `webhook`, where an HMAC is optional. |

| Type | Verification | Event ID (for duplicates) | Event name |
| --- | --- | --- | --- |
| `github` | `X-Hub-Signature-256`, HMAC-SHA256 of the raw body | `X-GitHub-Delivery` | `X-GitHub-Event` plus `.action`, e.g. `issues.opened` |
| `stripe` | `Stripe-Signature`, HMAC over `t.body`, 5-minute tolerance | `.id` | `.type`, e.g. `invoice.paid` |
| `slack` | `X-Slack-Signature`, `v0` HMAC over `v0:ts:body`, 5-minute tolerance | `.event_id` | `.event.type` |
| `webhook` | Token in the URL path, or `Authorization`; optional HMAC: `X-Dagu-Signature` or Standard Webhooks | `Idempotency-Key` or `webhook-id` header when present; otherwise the delivery ID | `webhook` |

- **The sender's event ID is the event ID,** so a sender's own retry maps to
  the run it already started, whichever way it arrives.
- **Timestamps are checked against receipt time,** not the time the server
  processes the event, because a delivery through Dagu Cloud can wait days.
- **Handshakes.** Slack's `url_verification` and a `GET` with `hub.challenge`
  are answered at once with the challenge, and start nothing.
- **Public URLs.** An admin turns a source's public URL on in the UI, as for
  per-DAG webhooks today. It is server state, not a field, so a file that
  arrives through git never exposes a URL by itself.
- **Credentials stay out of YAML.** Inbound URL tokens are generated by the
  server, stored hashed by source name, and rotated from the UI.

### Local ingress

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
  provider runs when remote access is on, or when any source has its public
  URL on. With remote access off, the server answers every proxied request
  with `403` and handles only delivery frames.
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

### Deliveries view

Each webhook source shows its recent deliveries:

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

## The `dag` source

DAG runs are a built-in source named `dag`; it needs no file. Its events are
the event store's `dag.run.*` types, such as `dag.run.succeeded` and
`dag.run.failed`, and its event ID is the DAG-run event's ID. The payload
names the DAG as `.dag`, the run as `.run_id`, and the status as `.status`.

```yaml
# report.yaml
triggers:
  - source: dag
    events: dag.run.succeeded
    condition: '.dag == "etl-daily"'
```

## Building a trigger in the UI

Writing gojq is not required. On a DAG, **Add trigger**:

1. Pick a source, or create one: its type, then its settings. For a
   `command` source, **Check now** shows what the command lists.
2. Pick events from a list: the sender's documented events for `github`,
   `stripe`, and `slack`; the events seen in recent deliveries for `webhook`;
   `item` for `command`; the `dag.run.*` types for `dag`.
3. For a new webhook source, copy its URL: the local one, and the public one
   once an admin turns it on. For a preset type, Dagu generates the signing
   secret to paste into the sender, stores it in the secret registry, and
   writes its `ref` into the source file.
4. Click **Send test**, or wait for a real delivery or check.
5. Click a field in the event to add a condition (equals, contains, exists)
   or to map it to a param. The result against that event shows as each one
   is added.

The UI writes a new source into `sources/` and the trigger into the DAG's
YAML. Both stay editable for anything the builder does not cover.

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
  controls `TITLE` above, and whatever a `command` source lists controls
  `POLL_ITEM`.
  - **To verify before shipping:** how `${PARAM}` in a step's `command` is
    expanded today. If Dagu substitutes the value into the command text
    before a shell parses it, payload-derived params allow command injection.
  - In that case, triggered params must reach steps only as environment
    variables, and the docs and the UI must say so.
- Provider secrets live in the server's secret registry or provider. Neither
  Dagu Cloud nor the relay ever sees them.
- Conditions run in gojq with a time bound and no I/O.
- Inbound endpoints are rate limited per source, locally and at the relay.
- A `command` source runs on the scheduler host. `sources.allow_command`
  keeps it off where DAG authors otherwise run nothing there.
- A public URL is turned on by an admin on the server, never by a file, so a
  source that arrives through git sync is reachable only locally until then.

## Failure behavior

| Situation | Behavior |
| --- | --- |
| Server offline, public URL on | The relay answers `202` and keeps deliveries up to 7 days; the server takes them in order on reconnect. |
| Offline longer than 7 days | The relay drops expired deliveries and reports their count, which Deliveries and Dagu Cloud show. |
| Scheduler down | Events persist; the matcher catches up within event store retention. The next check compares against the last successful one. |
| Signature invalid | `401` locally. Through Dagu Cloud: `202` from the relay, then a rejected delivery on the server. |
| Check fails, times out, or prints too much | It writes nothing and keeps the previous keys; after 3 in a row, the source's page and its DAGs' pages show the error. |
| Condition errors or times out | The event shows the error; nothing starts. |
| Same event twice | The derived run ID already exists; nothing new starts. |
| Trigger names a missing or invalid source | The trigger is invalid; nothing starts from it. |
| Subscribing DAG suspended, or 100 runs queued | The event waits for that trigger; other triggers go on. |

## Performance

- **Checks.** One process per `command` source per interval, however many
  DAGs subscribe. With 100 sources at the default 1 minute, about 100
  processes a minute. Each replaces a full run today, which starts a process
  and writes a status record, logs, and a work directory.
- **Bound.** At most 4 checks run at once across the scheduler; a due check
  waits for a slot.
- **Idle cost.** Proportional to the number of `command` sources with an
  active subscriber. The matcher reads only events after its cursor; with no
  new events and none waiting, it does no work.
- **Storage.** Under 400 KiB of scheduler state per `command` source. One
  event per new item or delivery, with a webhook payload of up to 512 KiB
  beside it, kept for the event store's retention: 7 days instead of 1 once
  any trigger exists.
- **Run history.** Not read on the check or match path. Runs are enqueued by
  derived ID.
- **Upgrade.** None. State is created on the first check or delivery.

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
| Next | Source files, triggers, the matcher, `DAGU_EVENT_FILE`, and 7-day event retention when triggers exist, with the `command` source, its check log, and `dagu source check`. |
| Later | The `dag` source; the `webhook` type with local and public ingress; the UI builder and Deliveries. |
| Later | `github`, `stripe`, and `slack` types. |
| v3.0 | The per-DAG webhook becomes a built-in `webhook` source. |

The model ships with `command` because work that sends no event leaves the
most empty runs today, and public URLs already cover the common webhook
cases. Documentation explains choosing between `watch`, `command` sources,
webhook sources, and `discard_run_on`.

`sources/` is a new folder in the DAG directory. An older binary ignores it
with flat discovery, and with recursive discovery lists its files as invalid
DAGs. A DAG directory that already keeps DAGs in a `sources/` folder must
rename it; the new binary reports those files as invalid sources.

An older binary fails to load a DAG with `triggers`, as it does any unknown
field, ignores the scheduler state sources add, and reads the run trigger
type `trigger` as unknown (`ir.ParseTriggerType`).

## Verification and acceptance criteria

`command` sources:

1. 100 checks that print nothing leave no run, no event, no log file, and no
   work directory, and 50 entries in the check log.
2. A check that prints three new keys writes three events and starts three
   runs in a subscribing DAG; the next check with the same output writes none.
3. Two DAGs subscribe to one source: one check runs per interval, and each new
   item starts one run in each DAG.
4. With `max_active_runs: 3`, ten new items run at most three at a time.
5. Killing the scheduler after writing events and before saving its state,
   then restarting it, starts no second run for the same key.
6. A failing or timed-out check writes nothing, keeps the previous keys, and
   shows its stderr in the check log; after three in a row, the source's page
   and the subscribing DAG's page show the error.
7. With `backlog: skip`, the first check writes nothing; with `backlog: run`,
   it writes one event per item.
8. A source whose only subscriber is suspended is not checked; after resume,
   items listed during the suspension each run once.
9. Output over 1 MiB or 10,000 lines fails the check and writes nothing.
10. With 100 queued runs on the DAG, new items wait and start as the queue
    drains.
11. `dagu source check` lists the items and the runs they would start, and a
    check right after it still finds the same items new.
12. With `default_execution_mode: distributed` and `sources.allow_command`
    unset, a `command` source is invalid and never checked.

Webhook and `dag` sources:

13. A GitHub `issues` event with the label `bug` starts one run with `TITLE`
    and `URL` set; one without the label starts nothing and shows
    "condition false".
14. GitHub redelivering the same event, with the same `X-GitHub-Delivery`,
    starts nothing new, locally and through Dagu Cloud.
15. A bad signature is rejected and never matched.
16. With the server offline for an hour, public deliveries arrive in order on
    reconnect, each starting one run.
17. Two DAGs subscribe to one `github` source: GitHub is set up with one URL,
    and one delivery starts one run in each DAG.
18. A 400 KiB payload reaches a step through `DAGU_EVENT_FILE` on Linux,
    macOS, and Windows.
19. An existing per-DAG webhook URL keeps working after the upgrade to v3.0.
20. A `github` source that arrives through git sync has no public URL until
    an admin turns it on.
21. A trigger on `dag.run.succeeded` with a condition naming another DAG
    starts one run for each successful run of that DAG.

All sources:

22. `dagu validate` on a DAG directory, with no data directory, reports a
    trigger that names a missing source, and the trigger starts nothing.
23. With one of two subscribing DAGs suspended, an event starts a run in the
    other at once, and in the suspended one when it resumes.
24. An item or payload crafted with shell metacharacters runs no command
    beyond the step's own.

## Alternatives considered

- **Keep empty runs and bound them.** The Kubernetes CronJob and Dagster
  retention approach. Dagu has it already (`hist_retention_runs`). It bounds
  disk, but lists stay full of empty runs and runs that did work are removed
  with them.
- **Discard empty runs after they end** (RFC 006). Needed when only the run
  can tell, but each check still starts a process and five concurrent jobs
  still need five pollers.
- **Extend only the per-DAG webhook.** It would close the webhook gaps, but
  work that sends no event and another DAG's outcome would each need a
  mechanism of their own.
- **Webhooks only** (Airflow 3, Prefect). Right when the source pushes. Many
  sources do not: an API without webhooks, a channel's video list, a queue on
  another machine.
- **A sensor as a special run.** A run that is hidden or pruned when it finds
  nothing. Every check would still write and delete a run record and its
  logs, and every run list would need to filter them.
- **Airflow-style sensor modes.** They free worker slots during a wait, which
  Dagu does not spend. They do not remove runs.
- **A `poll` field on the DAG** (RFC 019's first draft). The check and its
  state would belong to one DAG: two DAGs that react to one listing would run
  two checks against the same API.
- **Extending `watch` with a `command`.** One concept instead of two, but a
  folder and a command differ in arrival detection, first-check behavior, and
  keys. They share the ledger code.
- **Source settings inside each trigger** (the first draft of this RFC). A
  repository that three DAGs react to would need three URLs and three secrets
  in GitHub, and three DAGs reading one listing would run three checks.
- **Sources that name the DAGs they start.** Adding a DAG would edit a shared
  file, and a DAG would no longer say what starts it.
- **Sources kept in the data directory and edited in the UI.** Triggers would
  name server state, while that state depends on the DAGs that subscribe to
  it. A DAG directory would no longer validate or load alone, and git sync,
  copies, and restores would change what starts a DAG.
- **A `kind` field that marks source files anywhere in the tree.** Discovery
  would parse every file to tell sources from DAGs; a folder needs no parse.
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

## Consequences

- One `command` source replaces N copies of a poller, and the history holds
  only runs that did work.
- Several DAGs share one webhook URL, and conditions and params replace runs
  that exit early.
- The event store becomes an input of execution, not only a record of it.
  Its retention, 7 days by default once triggers exist, bounds how long the
  scheduler can be down, or a DAG saturated, without missing events.
- The scheduler runs user commands outside runs. A slow or hung check costs a
  scheduler slot until its timeout.
- A check's output decides what runs, so a bug in the command that prints
  stale keys starts nothing, and one that prints new keys every time starts a
  run every time. The check log shows both.
- Debugging a check means reading the source's check log, not a run's logs.
- A source is a new kind of file with its own page and state. Renaming it
  makes a new source, with a new URL and no memory of the items it has seen.
- Dagu Cloud's relay carries deliveries for servers that never enable remote
  access.

## Open questions

1. Is 512 KiB enough? Large GitHub `push` payloads can exceed it.
2. Should replay need a separate permission from running the DAG?
3. Should a DAG be able to subscribe to a source in another project?
4. Is `command` the right type name, or `poll`?
5. Should a `command` source take `worker_selector`, to run its check on a
   worker for sources only a worker can reach?
6. Should `backlog` default to `run`, since a work queue is the motivating
   case?
7. Should three failed checks in a row notify, and through what: the
   subscribing DAGs' failure notification, or the source's own?
8. Should a `command` source take `secrets`, as a DAG does, or only `env`?
9. Should a key be allowed to run again after its run leaves the history, or
   once per key forever, using the remembered hashes?
