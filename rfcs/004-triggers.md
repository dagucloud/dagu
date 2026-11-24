# RFC 004: Triggers

- **Status:** Proposed. Its first release, public URLs for per-DAG webhooks,
  shipped in v2.20 and is specified below.
- **Date:** 2026-10-09, first release added 2026-10-14, revised 2026-11-23 to
  absorb RFC 019 as a per-DAG poll
- **Affected areas:** `persis` (store), `schedulerstate`,
  `service/scheduler`, `service/notification`, `service/frontend` (API, UI),
  `auth` (webhooks),
  `tunnel`, `audit`, `specs`, and `docs`

## Summary

Let a DAG start when new work appears somewhere that sends no event. A
developer gives the DAG a poll: a command the scheduler runs on an
interval. Each new line it prints starts one run. A check that finds
nothing starts no run and leaves no run record.

A poll is server state, like the per-DAG webhook. It is set in the DAG's
page or through the API, and the DAG file does not change:

```json
{
  "dagName": "feed",
  "enabled": true,
  "command": "ssh cloud-vm 'ls ~/feed/jobs'",
  "interval": "1m",
  "backlog": "run"
}
```

```yaml
# feed.yaml
max_active_runs: 3
steps:
  - id: feed
    command: ~/feed/feed-run.sh   # reads the job from $POLL_ITEM
```

Work whose producer can send an HTTP request uses the per-DAG webhook, which
needs nothing new.

This RFC absorbs RFC 019, Poll for Work Without a Run.

## What already ships

A DAG starts today on a schedule, by hand, from a parent DAG, when a file
arrives in a folder it watches (`watch`, [Spec 082](../specs/082-watch.md)),
or when its per-DAG webhook (`POST /webhooks/{fileName}`) is called.

The per-DAG webhook is kept in the data directory, not in the DAG file. It is
created and changed in the DAG's page or through
`/api/v1/dags/{fileName}/webhook`, needs the developer role or above, and
moves with the DAG when the DAG is renamed.

Since v2.20, a per-DAG webhook can also have a public URL through Dagu Cloud
([First release](#first-release-public-urls-for-per-dag-webhooks)). That URL
reaches a server behind a firewall, verifies GitHub, Stripe, Slack, and
Standard Webhooks signatures, takes only the events it lists, and hands large
bodies over as a file.

## Problem statement

Some work sends no event, and nothing that creates it can call a webhook: an
API without webhooks, a queue on another machine, a table, a channel's video
list. The usual way to react to it is a scheduled DAG that checks for work
and exits when there is none. Every check is a full run: a process, a status
record, logs, and a work directory.

One showcased setup runs a live video channel on a 1 GB VM
(https://dagu.sh/showcase/live-channel-on-a-1-gb-vm). Five copies of a feed
DAG run every 5 minutes. In 14 days the feed DAG made 15,166 runs, and 257 did
real work. The rest found no job and ended in about 2 seconds (RFC 006).
Capacity grew by copying the DAG file, from one copy to five, because a
schedule starts runs by the clock, not by the job.

| Option | Fits when | Leaves |
| --- | --- | --- |
| `watch` (Spec 082) | Work arrives as files in a folder the scheduler can read | Nothing to fix |
| Per-DAG webhook | Whatever creates the work can send an HTTP request | Nothing to fix |
| `discard_run_on` (RFC 006) | Only the run can tell whether there was work, for example by claiming a job atomically | A process start per check, and one poller copy per concurrent job |
| `hist_retention_runs` or `hist_retention_days` | Disk is the only concern | Empty runs in every list, and runs that did work removed with them |
| None of the above | The work can be listed but nothing announces it | This RFC |

## Prior art

Most orchestrators keep polling, but separate the cheap check from the run it
may start.

| Tool | What a check that finds nothing leaves |
| --- | --- |
| Dagster sensors | A tick marked skipped, stored apart from runs and purged on its own schedule. |
| Windmill trigger scripts | The script keeps state between checks; when it returns no new items, the flow is skipped. |
| Zapier polling triggers | The trigger compares item IDs with those it has seen; a Zap runs only for a new ID. |
| Jenkins `pollSCM` | A polling log entry, "No changes". A build starts only when changes are found. |

The common choice: the poll is an evaluation, not a run, and only new items
become runs.

## Goals

1. A check that finds no new work leaves no run, no logs, and no work
   directory.
2. A new item starts one run, despite restarts, while that run is in the
   history.
3. Concurrency comes from `max_active_runs` and queues, not from copies of a
   DAG.
4. Every check is visible, and a poll that fails or stops checking says so.
5. Idle cost is bounded and grows with the number of polls, not with DAGs
   or runs.
6. Poll settings need the developer role or above, as the per-DAG webhook
   does.
7. DAG files, their schema, and their validation do not change.

## Non-goals

- Exactly-once execution. A failed run is retried by the DAG's retry policy
  or by hand, not by the poll.
- A poll in the DAG file, or carried by git sync. See
  [Alternatives considered](#alternatives-considered).
- More than one poll per DAG, or one poll shared by several DAGs.
- Conditions and params. The command filters and shapes what it prints.
- Changes to the per-DAG webhook.
- Running checks on workers. See [Where the check runs](#where-the-check-runs).
- Detecting a changed item whose key stays the same. A new version of an
  item needs a new key.
- Reading a listing incrementally from a cursor.

## The poll

### Settings

| Field | Default | Meaning |
| --- | --- | --- |
| `dagName` | required | The DAG the poll starts |
| `enabled` | `true` | A disabled poll is not checked |
| `command` | required | Prints one item per line on stdout |
| `env` | none | Variables the command receives; encrypted at rest and never returned by the API |
| `interval` | `1m` | Time from the end of one check to the start of the next; at least `10s` |
| `timeout` | `30s` | Longest a check may run; at most `5m` |
| `key` | the whole line | A gojq expression; when set, each line is JSON and its key is the expression's string result |
| `backlog` | `skip` | `skip` or `run`: what the first check does with the items it lists |

A DAG's poll is managed in a Poll tab on its page, beside Webhook, and
through `/api/v1/dags/{fileName}/poll` for settings and
`/api/v1/dags/{fileName}/poll/status` for its state and check log. A Polls
page and `GET /api/v1/polls` list every poll with its DAG, command,
interval, state, and last check, so a developer sees every command the
scheduler runs without opening each DAG.

- **Access.** Reading or changing a poll's settings, and trying a check,
  need the developer role or above, as the per-DAG webhook does. A
  developer can already run commands on the scheduler host through a DAG's
  steps. The audit log records each change and try. The status needs only
  the role that reads the DAG, so a viewer still sees a failing poll.
- **Storage.** Settings, state, and Check now requests are JSON records in
  their own folders of the data directory: `polls/`, `poll-state/`, and
  `poll-checks/`, beside `webhooks/`. The API writes settings and the
  scheduler writes state, so no record has two writers. Under
  [RFC 003](003-projects.md) they are project data, as `webhooks/` is.
- **Pickup.** The scheduler rereads the settings periodically. A saved
  change takes effect without a restart.
- **Rename and removal.** A rename or delete done in Dagu moves or deletes
  the poll with its DAG, and a moved poll keeps the keys it remembers. A
  DAG file renamed or deleted outside Dagu, by git sync or on disk, leaves
  its poll with no DAG: it is not checked, the Polls page shows it, and it
  can be pointed at the new name or deleted.
- **A DAG that fails to load** is not checked, and its page says why.

### A check

The scheduler runs `command` with the default shell, in the DAG file's
directory, as the scheduler's user. It gets the base environment that runs
get, plus `env`, not the rest of the scheduler's environment.

- **Items.** Each line of stdout that is not blank is an item. Output is
  capped; items past the cap wait for a later check, so a work queue prints
  its oldest jobs first.
- **Failure.** A non-zero exit or a timeout fails the check. A failed check
  starts nothing and leaves the poll's keys as they were.
- **Keys.** An item's key is the line, or the result of `key`. Of lines
  with the same key, the first is the item.
- **Bad lines.** A line that cannot be an item is skipped and not
  remembered: one too long for an environment variable or holding a NUL
  byte, or, with `key`, one that is not JSON or has no non-empty string
  key. The other items go ahead, so one bad job does not hold up a work
  queue.
- **Concurrency.** Checks share a small pool across the scheduler, one per
  poll at a time. A due check waits for a free slot, oldest first.
- **Backoff.** Failed checks back off from `interval` up to 10 minutes
  apart. A successful check, saving the poll, or resuming the DAG returns
  to `interval`, so a fix is checked soon.

### New items

After a successful check, an item whose key the poll remembers is skipped.
Every other item is new and is queued as a run, in the order printed. The
poll then remembers the keys it listed, except those of items it did not
queue, and forgets keys that are no longer listed.

- **Run ID.** A run's ID is derived from the poll and the key, as `watch`
  derives its run IDs. If a run with that ID exists, nothing starts. So an
  item queued again after its key was lost, as when the scheduler stops
  between queuing a run and saving the keys, starts no second run.
- **Queue limit.** A check never fills the DAG's queue past 100 waiting
  runs, the limit public webhooks use, and an item whose run exists counts
  toward it. Items left out are new again at the next check, so a large
  backlog drains as fast as the DAG runs it.
- **Queuing failure.** If a run cannot be queued, the check fails: it stops
  queuing and leaves the poll's keys as they were. Runs it did queue are
  found by their run IDs at the next check.

So an item that stays listed runs once. An item that leaves the listing and
comes back runs again only after its earlier run has left the history. A
listing whose items change under the same key puts a version in the key.

A failed run is not started again by the poll. A work queue whose jobs
should be retried sets the DAG's retry policy.

### What the run receives

`POLL_ITEM`, the line, and `POLL_KEY`, the key, as runtime params, as
`watch` passes `WATCH_FILE`. The run's trigger type is `poll`, and it uses
the DAG's default profile.

Whatever the command lists chooses both values, so a step reads them from
its environment and does not interpolate them into its command, as Spec 082
says for `WATCH_FILE`.

### The first check

The first check after a poll is created, or after its `command` or `key`
changes, follows `backlog`. With `skip`, it remembers what it lists and
starts nothing, as `watch` does. With `run`, every listed item is new. A
work queue wants `run`; an archive, such as a channel's past videos, wants
`skip`.

Since run IDs come from the poll and the key, a `key` change or a deleted
and recreated poll with `backlog: run` runs every listed item again, while
a `command` change does not rerun items whose runs are still in the
history. If a poll's state is lost, its next check is a first check. The
Poll tab warns before it saves a `command` or `key` change, and before it
deletes a poll.

### Where the check runs

The check runs on the scheduler host, in the scheduler that holds the
scheduler lock. Only the run that does the work is placed on a worker,
through the DAG's `worker_selector` as usual.

With several schedulers for failover, every scheduler host needs what the
commands need: their tools, `ssh` keys, and network access. A host that
lacks them fails the checks, and the alerts below say so.

RFC 006 rejected a guard command in the scheduler because the check would run
on the wrong host for distributed workers. A check differs in what it does:
it lists work from a source the scheduler can reach, such as an API, a remote
queue over `ssh`, or a database, and does no work itself. A source only a
worker can read still needs RFC 006.

### Suspension

A suspended DAG's poll, or a disabled poll, is not checked, and its
remembered keys stay as they were. Items listed when the DAG resumes or the
poll is enabled again, and not remembered, run then, as with `watch`.

### Check log and alerts

- **Check log.** The Poll tab shows when the last check ended, when the
  next is due, and the latest 50 notable checks: those that started a run
  or failed, and those that were cut or skipped lines when the check before
  was not. Each entry shows its counts, exit code, a stderr excerpt with
  `env` values masked, and links to the runs it started. Other checks are
  only counted, so the log of a quiet poll still reaches back to its last
  real work. The log is kept apart from the run history.
- **Failing.** At the third check in a row that failed or skipped a line,
  the DAG's page shows the error, and one notification goes to the targets
  a failed run of the DAG notifies. No more are sent until a check neither
  fails nor skips a line, which clears the error and sends one
  notification.
- **Not running or late.** The DAG's page shows a poll as not running while
  no scheduler holds the scheduler lock, and as late when a check is well
  past due. Neither is notified: whatever stops checks also stops the
  process that would send the notification.

### Trying a check

**Check now** in the Poll tab, and `POST /api/v1/dags/{fileName}/poll/check`,
run the command once with the settings being edited and show the items it
lists, the lines it skips, and which items the poll would take as new. A
try starts no run and changes no state, so a command can be tried before it
is saved.

The scheduler runs the try, so it runs where real checks do, and busy check
slots never hold it up. A try has a short time limit and says so when no
scheduler answers. A slower command is tried by saving it with
`backlog: skip`, whose first check starts nothing.

## Security

- A poll runs a command on the scheduler host as the scheduler's user. Only
  the developer role or above, which can already run commands there through
  a DAG's steps, can set or try one. Each change and try is audited, and
  the Polls page shows developers every command that runs there.
- `POLL_ITEM` and `POLL_KEY` come from whatever the command lists. A value
  interpolated into a step's command can inject a command, so steps read
  them as environment variables, and the docs say so.
- The check gets the base environment that runs get, plus `env`, not the
  scheduler's own. `env` values are encrypted at rest, never returned by
  the API, and masked in the check log. This keeps a token out of git, API
  responses, and viewers' reach. A developer can still print it, with
  Check now or a step, as with any secret a DAG uses.

## Failure behavior

| Situation | Behavior |
| --- | --- |
| Scheduler down | No checks run, and the DAG's page shows the poll is not running. The next check offers every item not remembered. |
| Check fails or times out | It starts nothing, keeps the poll's keys, and backs off. Three in a row notify. |
| A line cannot be an item | It is skipped and not remembered; the other items go ahead. The check counts toward the three that notify. |
| Output past the cap | Later items wait for a later check, and the check is marked cut. |
| Queuing a run fails | The check fails and keeps the poll's keys; the runs it queued are found by their run IDs at the next check. |
| The DAG's queue holds 100 waiting runs | Further items wait for a later check. |
| Scheduler stops between queuing runs and saving keys | The next check derives the same run IDs; nothing new starts. |
| DAG file renamed or deleted outside Dagu | The poll is not checked, and the Polls page shows it. |
| Failover to a host that cannot run the command | Checks fail and alert. |
| DAG suspended or fails to load | Not checked. |
| Settings cannot be read | The scheduler keeps the polls from its last complete read. |

## Performance

- **Checks.** One process per enabled poll per interval, a few at a time.
  Each replaces a full run today, with its status record, logs, and work
  directory, by a process and a small state write. A slow check stretches
  the time between checks rather than adding processes, and a failing poll
  slows to one check every 10 minutes.
- **Idle cost.** Grows with the number of enabled polls of unsuspended
  DAGs, not with DAGs or runs. Picking up settings reads one small record
  per poll periodically.
- **Writes.** A quiet check rewrites only a small status record. The
  remembered keys are rewritten when the listing changes, and the check log
  when a check is listed. State stays under a few hundred KiB per poll.
- **Run history.** Never listed. Each new item costs the run ID lookup that
  every enqueue costs today, which reads the DAG's history folders when no
  run has the ID. A check looks up at most 100 IDs.
- **UI.** The Poll tab and the Polls page subscribe to SSE topics. The
  server fetches each topic once however many tabs subscribe, rate-limits
  it, and stops when the last subscriber leaves. The tab refetches the
  check log only when the status shows a new entry.
- **Upgrade.** None. The poll folders are created on first use.

## First release: public URLs for per-DAG webhooks

The first release gives each existing per-DAG webhook an optional public URL
through Dagu Cloud.

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
  waiting for it survive the rename.
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

- The scheduler sends the whole set on every new connection. It then
  rereads the webhook store every 10 seconds and sends `hooks` when the set
  changes, so turning a URL off takes effect within 10 seconds.
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
- Webhook bodies wait at Dagu Cloud for up to 7 days, less when the server
  is disconnected there.

### Disconnecting in Dagu Cloud

- Disconnecting the server, or revoking its activation or license, in Dagu
  Cloud deletes its public URLs and the deliveries waiting for them. They
  are not counted as missed. The URLs answer `404` from then on, so senders
  see failures rather than a `202` that is never delivered.
- The server keeps its webhooks, tokens, sender secrets, and delivery log,
  and its local endpoint keeps working. Its tickets are refused, and once its
  license heartbeat is refused too, it stops connecting.
- Reconnecting the same server to the same workspace brings back the same
  URLs at its next connection, since the relay names the server by workspace
  and server ID. Deleted deliveries do not come back; GitHub needs a manual
  redelivery, and Stripe's own retries reach the server if it returns within
  3 days.
- A suspended license or a lapsed plan deletes nothing.

## Compatibility and rollout

| Release | Ships |
| --- | --- |
| v2.20 | Public URLs for per-DAG webhooks (above). |
| Next | Polls: their folders, the API, the Poll tab, the list of polls, the check log, alerts, and Check now. |

Documentation explains choosing between `watch`, a poll, the per-DAG webhook,
and `discard_run_on`.

DAG files do not change. An older binary ignores the poll folders and shows
the run trigger type `poll` as unknown. An older worker runs a poll's run
as usual and records its trigger type as unknown. A downgrade is safe:
polls stop until the newer binary returns.

A poll does not travel with the DAG through git sync or to another server. It
is backed up and restored with the data directory, as webhooks are.

## Verification and acceptance criteria

1. 100 checks that print nothing leave no run, no log file, and no work
   directory.
2. A check that prints three new keys starts three runs; the next check with
   the same output starts none.
3. With `max_active_runs: 3`, ten new items run at most three at a time.
4. With `backlog: run` and 1,000 items listed, no check fills the DAG's
   queue past 100 waiting runs, and every item runs once.
5. Killing the scheduler after queuing runs and before saving the poll's
   keys, then restarting it, starts no second run for the same key.
6. A failed or timed-out check starts nothing, keeps the poll's keys, and
   shows its stderr in the check log with `env` values masked.
7. A poll whose command always fails backs off to one check every 10
   minutes; saving the poll returns it to its interval.
8. At the third failure in a row, the DAG's failure targets get one
   notification; later failures send none, and the next success sends one.
9. With the scheduler stopped, the DAG's page shows the poll is not
   running; with every check slot held by hung checks, a due poll shows as
   late.
10. With `backlog: skip`, the first check starts nothing; with
    `backlog: run`, it starts runs for the items listed.
11. A suspended DAG is not checked; after resume, items still listed each
    run once.
12. A bad line is skipped and not remembered while the other items run. A
    line that stays bad does not push checks that started runs out of the
    check log.
13. A run that cannot be queued fails the check and keeps the poll's keys;
    once queuing works, the item runs once.
14. Check now runs on the scheduler host, starts nothing, leaves the poll's
    state unchanged, and is in the audit log.
15. An operator cannot read, change, or try a poll's settings, and a
    developer can; a viewer reads its status. `env` values never appear in
    an API response.
16. Renaming a DAG in Dagu keeps its poll and remembered keys; no item runs
    again.
17. A check does not see a variable of the scheduler's environment that the
    base environment leaves out.
18. A step that reads an item crafted with shell metacharacters from its
    environment runs no command beyond its own.

## Alternatives considered

- **A `poll` field in the DAG file** (the previous draft). It would travel
  with the DAG through git sync, as `schedule` and `watch` do. But a token
  the command needs would sit in git, and the DAG schema and its validation
  would grow for a command that runs outside any run. Server state keeps
  the token encrypted and DAG files as they are, as the per-DAG webhook
  does.
- **Sources and triggers.** A source file in `sources/` that several DAGs
  subscribe to, with conditions, params, and webhook source types. It shares
  one check, or one URL, between DAGs, but adds a new kind of file with state
  kept by name, a second webhook beside the per-DAG one, and a model to
  learn.
- **Persist every event, and match in the scheduler.** Made the event store
  an input of execution, with longer retention for every event kind and
  unbounded state for events that wait.
- **Keep empty runs and bound them.** Dagu has it already
  (`hist_retention_runs`). It bounds disk, but lists stay full of empty runs
  and runs that did work are removed with them.
- **Discard empty runs after they end** (RFC 006). Needed when only the run
  can tell, but each check still starts a process and five concurrent jobs
  still need five pollers.
- **Webhooks only.** Right when the producer can call one. An API without
  webhooks, a channel's video list, or a queue on another machine cannot.
- **A sensor as a special run.** Every check would still write and delete a
  run record and its logs, and every run list would need to filter them.

## Consequences

- One polling DAG replaces N copies of a poller, and the history holds only
  runs that did work.
- A job that fails is not picked up again by the next check, as it was with
  a poller DAG. Retries are the DAG's.
- "Once" lasts as long as the run's history. A short retention shortens it.
- A poll is set per server. Moving DAGs to another server, or deploying them
  through git sync, does not bring their polls.
- The scheduler runs user commands outside runs, and every scheduler host
  that can take the scheduler lock needs what the commands need.
- Debugging a check means reading the poll's check log, not a run's logs.
- Two DAGs that react to one listing run two checks.
