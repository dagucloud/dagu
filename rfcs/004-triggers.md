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

Let a DAG start when new work appears somewhere that sends no event. A user
who may run commands on the scheduler host gives the DAG a poll: a command
the scheduler runs on an interval. Each new line it prints starts one run. A
check that finds nothing starts no run.

A poll is server state, like the per-DAG webhook. It is kept in the data
directory, set in the DAG's page or through the API, and the DAG file does
not change:

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
4. Every check stays visible: what it listed, what it started, and why it
   failed. A poll that keeps failing notifies, and one that stopped checking
   says so.
5. Bounded idle cost on the scheduler host.
6. Only a user who may already run commands on the scheduler host can set or
   try a poll, and the audit log records it.
7. DAG files, their schema, and their validation do not change.

## Non-goals

- Exactly-once execution. A failed run is retried by the DAG's retry policy
  or by hand, not by the poll.
- A poll in the DAG file, or carried by git sync. See
  [Alternatives considered](#alternatives-considered).
- More than one poll per DAG, or one poll shared by several DAGs.
- Conditions and params. The command filters and shapes what it prints.
- Changes to the per-DAG webhook, such as taking the sender's event ID as the
  run ID, conditions and params over the payload, or one URL for several
  DAGs. A later RFC can add them to the webhook.
- Starting a DAG from another DAG's outcome.
- Running checks on workers. See [Where the check runs](#where-the-check-runs).
- Change detection for items whose key stays the same. A new version of an
  item needs a new key.
- Incremental listing from a cursor, for a listing too large to read whole.

## The poll

### Record

A DAG has at most one poll. Polls are kept as records in a collection in the
data directory, beside webhooks:

| Field | Type | Default | Meaning |
| --- | --- | --- | --- |
| `dagName` | string | required | The DAG the poll starts |
| `enabled` | bool | `true` | A disabled poll is not checked |
| `command` | string | required | Command that prints one item per line on stdout |
| `env` | map | none | Variables the command receives; values are encrypted at rest, as webhook secrets are |
| `interval` | duration | `1m` | Time between the end of one check and the start of the next, longer after failures; at least `10s` |
| `timeout` | duration | `30s` | Longest a check may run; at most `5m` |
| `key` | gojq expression | the whole line | When set, each line is parsed as JSON and the key is this expression's string result |
| `backlog` | `skip` or `run` | `skip` | What the first check does with the items it lists |

The record also holds its ID and who created and last changed it, and when.
A save that sets no command, an interval under `10s`, a timeout over `5m`, an
invalid gojq expression, or an unknown `backlog` is rejected.

- **Managing.** The DAG's page has a Poll tab beside Webhook. The API is
  `GET`, `PUT`, and `DELETE /api/v1/dags/{fileName}/poll`. Reading a poll
  needs the role that reads the DAG; `env` values are never returned.
- **Who may change it.** Creating, changing, deleting, or trying a poll
  needs the developer role or above when `default_execution_mode` is
  `local`, where DAG steps already run on the scheduler host, and an admin
  when it is `distributed`, where DAG authors otherwise run nothing there.
  The audit log records each.
- **All polls.** `GET /api/v1/polls` and an admin page list every poll: its
  DAG, command, interval, state (checking, failing, not running, suspended,
  disabled, or no DAG), and last check. They show what runs on the
  scheduler host without opening each DAG.
- **Projects.** Under [RFC 003](003-projects.md), a poll is project data, as
  a per-DAG webhook is. It names its DAG within the project and moves with
  the DAG to another project.
- **DAG rename and removal.** A rename or delete done in Dagu moves or
  deletes the poll with the DAG, as for the webhook; a moved poll keeps its
  record of seen keys. A DAG file renamed or deleted outside Dagu, by git
  sync or on disk, leaves its poll with no DAG: it is not checked, and the
  list of polls shows it. It can be pointed at the DAG's new name, keeping
  its record, or deleted. A DAG that comes back under the old name takes it
  back.
- **Pickup.** The scheduler rereads the poll collection every 10 seconds, as
  it rereads the webhook store for public URLs. A change takes effect within
  10 seconds.
- **A DAG that fails to load.** Its poll is not checked, and its page says
  why.

### A check

The scheduler runs `command` with the default shell, as a step without
`shell` does, in the DAG file's directory, as the scheduler's user. It gets
the filtered base environment that runs get, plus `env`; it does not inherit
the rest of the scheduler's environment.

- **Output.** Stdout is read up to 1 MiB or 10,000 lines, whichever comes
  first. The rest is not read, a cut last line is dropped, and the check log
  marks the check as cut. Each non-empty line is an item; a line over 64 KiB
  fails the check.
- **Stderr.** Kept up to 4 KiB for the check log, with `env` values masked,
  as secrets are in step logs.
- **Exit code.** A non-zero exit or a timeout fails the check. A failed check
  starts nothing and changes no record.
- **Keys.** Each item's key is the line, or the result of `key`. A line that
  is not JSON, or a key that is not a non-empty string, fails the check.
- **Overlap.** At most one check per poll runs at a time, and at most 4
  across the scheduler. A due check waits.
- **After a failure.** After n failed checks in a row, the next check waits
  `interval` × 2^(n−1), at most 10 minutes, or `interval` when that is
  longer. With the default interval: 1, 2, 4, 8, then every 10 minutes. A
  successful check, saving the poll, and resuming the DAG return the wait to
  `interval`.

### New items

The scheduler remembers the keys of the poll's last check. After a
successful check:

- An item whose key is remembered is skipped.
- Any other item is new, and is queued as a run, in the order printed, up to
  100 runs per check. None is queued while 100 or more runs wait on the DAG's
  queue. The run ID is `poll-` and a hash of the poll ID and the key; if a
  run with that ID exists, nothing starts.
- The scheduler then remembers every listed key, except those of items it
  did not queue. Those are new again at the next check.
- A key that is no longer listed is forgotten.

With 10,000 jobs listed at once and `backlog: run`, the poll queues 100 per
check, about 6,000 an hour at the default interval, and none while the DAG's
queue is full.

Items past the output limit wait until earlier ones leave the listing, so a
work queue prints its oldest jobs first. A remembered item pushed past the
limit is forgotten; when it is listed again, its derived run ID keeps it
from running twice while its run is in the history.

So an item that stays listed runs once. An item that leaves the listing and
comes back derives the same run ID, and runs again only after its earlier run
has left the history (`hist_retention_days`, `hist_retention_runs`, or an RFC
006 discard). A listing whose items change under the same key puts a version
in the key.

A failed run is not started again by the poll. A work queue whose jobs should
be retried sets the DAG's retry policy, or removes a job from the listing only
when it is done and retries by hand.

### What the run receives

- `POLL_ITEM`, the line, and `POLL_KEY`, the key, as runtime params, as
  `watch` passes `WATCH_FILE`.
- The run's trigger type is `poll`. It uses the DAG's default profile.

Both are chosen by whatever the command lists. A step reads them as
environment variables and does not interpolate them into its command, as Spec
082 says for `WATCH_FILE`. With `key`, a step parses `$POLL_ITEM` as JSON.

### The first check

The first check is the first after the poll is created, or after its
`command` or `key` changes. With `backlog: skip`, it remembers what it lists
and starts nothing, as `watch` and Zapier do. With `backlog: run`, every
listed item is new. A work queue wants `run`; a listing of an archive, such as
a channel's past videos, wants `skip`.

If the scheduler's state is lost, the next check is a first check again.
With `backlog: run`, items whose runs have left the history run again.

### Where the check runs

The check runs on the scheduler host, in the scheduler that holds the
scheduler lock. Only the run that does the work is placed on a worker,
through the DAG's `worker_selector` as usual.

With several schedulers for failover, the one that takes the lock runs the
checks. Every scheduler host needs what the commands need: their tools,
`ssh` keys, and network access. A host that lacks them fails the checks, and
the alerts below say so.

RFC 006 rejected a guard command in the scheduler because the check would run
on the wrong host for distributed workers. A check differs in what it does:
it lists work from a source the scheduler can reach, such as an API, a remote
queue over `ssh`, or a database, and does no work itself. A source only a
worker can read still needs RFC 006.

### Suspension

A suspended DAG's poll is not checked, and its record of keys stays as it
was. Items listed when the DAG resumes, and not remembered, run then. This is
what `watch` does.

### The check log

Each poll keeps its latest 50 checks that started a run, failed, or were
cut: start time, duration, exit code, number of items, number of new items,
links to up to 20 runs started, and the stderr excerpt. Checks that found
nothing new are not listed one by one; the log keeps their count since the
last listed check and when the latest ended. So a log of a quiet poll still
reaches back to its last real work. The log is kept with the scheduler's state, not in the run history,
and is shown in the DAG's Poll tab with when the last check ended and when
the next is due.

### Alerts

- **Failing.** At the third failed check in a row, the DAG's page shows the
  latest error, and one notification goes to the targets a failed run of the
  DAG notifies, as a backfill's summary does; a command target is left out.
  It names the DAG, the number of failed checks, the exit code, and the
  stderr excerpt. No further notification is sent while the checks keep
  failing.
- **Recovered.** The first successful check after that clears the error and
  sends one notification to the same targets.
- **Not running.** The scheduler records when the poll's next check is due.
  When that time is more than the interval plus the timeout past, for an
  enabled poll of an unsuspended DAG, the DAG's page shows that the poll is
  not running and when its last check ended. This is shown, not notified:
  what stops checks, such as a stopped scheduler, also stops the process
  that would send the notification.

### Trying a check

**Check now** in the Poll tab, and `POST /api/v1/dags/{fileName}/poll/check`,
run the command once with the settings being edited and show the items it
lists and which of them are new. They start no run and change no record, so
a command can be tried before it is saved.

The scheduler runs it, so it runs where real checks do. The server writes a
request; the scheduler that holds the lock takes it at its next pickup,
within 10 seconds, runs it in one of the 4 check slots, and writes back the
result. The call waits up to the timeout plus 20 seconds, and says so if no
scheduler took the request. The audit log records each try. A request is
removed once answered, or after 10 minutes.

### What is stored

| Record | Size bound | Location |
| --- | --- | --- |
| Poll settings | One small record per polling DAG | Poll collection, data directory |
| Keys of the last check | 10,000 keys as 16-byte hashes, 160 KiB per poll; written only when it changes | Scheduler state |
| Check log | 50 entries, about 5 KiB each, and a counter of quiet checks | Scheduler state, per poll |
| Check now requests | Removed when answered or after 10 minutes | Data directory |
| Runs | One per new item | Run history, as today |

### Example: the feed DAG

The five copies become one DAG, `feed.yaml` above, with one poll whose
command lists job files on the machine that holds the queue. Each job runs
once, at most three at a time. Over the same 14 days the history would hold
about 257 feed runs instead of 15,166, and the check log the latest 50
checks that started them.

If the jobs were files on the scheduler host, `watch` would already do this.
If the script that creates a job could call the DAG's webhook, the webhook
would.

## Security

- A poll runs a command on the scheduler host as the scheduler's user. Only
  a user who may already run commands there can create, change, or try one:
  the developer role or above in local mode, an admin in distributed mode.
  Each is audited, and the list of polls shows every command that runs
  there. A DAG file that arrives through git sync cannot add a poll.
- `POLL_ITEM` and `POLL_KEY` are controlled by whatever the command lists. A
  value interpolated into a step's command can inject a command, so steps
  read them as environment variables. The docs say so, as Spec 082 does for
  `WATCH_FILE`.
- The check gets the base environment that runs get, plus `env`, not the
  scheduler's own. `env` values are encrypted at rest, never returned by the
  API, and masked in the stderr excerpt, so a token the command needs stays
  out of git, responses, and the check log.

## Failure behavior

| Situation | Behavior |
| --- | --- |
| Scheduler down | No checks run, and the DAG's page shows the poll is not running. The next check offers every item not remembered. |
| Check fails or times out | It starts nothing and changes no record, and the next check waits longer, up to 10 minutes. At 3 in a row, the DAG's page shows the error and one notification is sent; the first success sends another. |
| Output over 1 MiB or 10,000 lines | The rest is not read; the check goes on with what it read and is marked as cut. |
| Over 100 new items, or 100 runs waiting on the DAG's queue | Items not queued are not remembered and are new again at the next check. |
| DAG file renamed or deleted outside Dagu | The poll has no DAG and is not checked; the list of polls shows it. |
| Scheduler fails over to a host that cannot run the command | Checks fail and alert as above. |
| Scheduler stops between queuing runs and saving its record | The next check derives the same run IDs; nothing new starts. |
| DAG suspended | Not checked; items still listed when it resumes run then. |
| DAG fails to load | Not checked; the DAG's page says why. |
| Poll collection cannot be read | The scheduler keeps the polls from its last complete read. |

## Performance

- **Checks.** One process per enabled poll per interval, at most 4 at once.
  With 100 polls at the default 1 minute, about 100 processes a minute. Each
  replaces a full run today, which starts a process and writes a status
  record, logs, and a work directory. A failing poll slows to one check
  every 10 minutes.
- **Runs queued.** At most 100 per check, and none while 100 wait on the
  DAG's queue.
- **Notifications.** At most two per run of failures: one when it starts,
  one when it ends.
- **Pickup.** One read of the poll collection and one of pending Check now
  requests every 10 seconds. Both grow with the number of polls, not with
  DAGs or runs.
- **Idle cost.** Proportional to the number of enabled polls of unsuspended
  DAGs, plus the pickup read.
- **Writes.** A check that finds nothing new updates one small state
  record: its quiet-check counter, its end time, and when the next check is
  due.
- **Storage.** Under 450 KiB of scheduler state per poll. Event store
  retention does not change.
- **Run history.** Looked up by derived run ID on enqueue; never listed.
- **Upgrade.** None. The collection and state are created on first use.

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
| Next | Polls: the collection, the API, the Poll tab, the list of polls, the check log, alerts, and Check now. |

Documentation explains choosing between `watch`, a poll, the per-DAG webhook,
and `discard_run_on`.

DAG files do not change. An older binary ignores the poll collection and the
scheduler state polls add, and reads the run trigger type `poll` as unknown
(`ir.ParseTriggerType`). A downgrade is safe: polls stop until the newer
binary returns.

A poll does not travel with the DAG through git sync or to another server. It
is backed up and restored with the data directory, as webhooks are.

## Verification and acceptance criteria

1. 100 checks that print nothing leave no run, no log file, and no work
   directory; the check log counts them and keeps its earlier entries.
2. A check that prints three new keys starts three runs; the next check with
   the same output starts none.
3. With `max_active_runs: 3`, ten new items run at most three at a time.
4. With `backlog: run` and 1,000 items listed, the first check queues 100
   runs and later checks queue the rest, 100 at a time; none is queued while
   100 runs wait on the DAG's queue.
5. Killing the scheduler after queuing runs and before saving its record,
   then restarting it, starts no second run for the same key.
6. A failing or timed-out check starts nothing, keeps the record, and shows
   its stderr in the check log, with `env` values masked.
7. With a 1-minute interval and a command that always fails, checks start
   1, 2, 4, 8, and then 10 minutes apart; saving the poll returns them to 1
   minute.
8. At the third failure in a row, the DAG's page shows the error and the
   DAG's failure targets get one notification; later failures send none, and
   the next success sends one more.
9. With the scheduler stopped, the DAG's page shows the poll is not running
   once its due time is more than the interval plus the timeout past.
10. With `backlog: skip`, the first check starts nothing; with
    `backlog: run`, it starts runs for the items listed.
11. A suspended DAG is not checked; after resume, items still listed each
    run once.
12. Output over 1 MiB or 10,000 lines is cut: runs are queued for the items
    read, the check log marks the check as cut, and later items run as
    earlier ones leave the listing.
13. Check now runs on the scheduler host, lists the items and which are new,
    and is in the audit log; a check right after it still finds the same
    items new.
14. In distributed mode a developer cannot create, change, delete, or try a
    poll, and an admin can; in local mode a developer can. The audit log
    records each. `env` values are never in an API response.
15. Renaming a DAG in Dagu keeps its poll and its record of keys; no item
    runs again. Deleting or renaming the DAG file through git sync leaves
    the poll unchecked and listed as having no DAG; pointing it at the new
    name keeps its record.
16. A poll saved through the API is checked within 10 seconds, without a
    scheduler restart.
17. A check does not see a variable of the scheduler's environment that the
    base environment leaves out.
18. A step that reads an item crafted with shell metacharacters from its
    environment runs no command beyond its own.

## Alternatives considered

- **A `poll` field in the DAG file** (the previous draft). It would travel
  with the DAG through git sync, as `schedule` and `watch` do. But anyone who
  can change a DAG file could run a command on the scheduler host, which in
  distributed mode DAG authors otherwise cannot; it needed a config switch
  to keep it off there, and a token for the command would sit in git. Unlike
  `schedule` and `watch`, a poll runs user code in the scheduler, so it
  follows the per-DAG webhook: server state that an authorized user sets.
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
- A job that fails is not picked up again by the next check, as it was with a
  poller DAG. Retries are the DAG's.
- "Once" lasts as long as the run's history. A short retention shortens it.
- A poll is set per server. Moving DAGs to another server, or deploying them
  through git sync, does not bring their polls.
- The scheduler runs user commands outside runs. A slow or hung check
  costs one of 4 slots until its timeout.
- Every scheduler host that can take the scheduler lock needs what the
  commands need.
- A large backlog drains at 100 runs per check.
- Debugging a check means reading the DAG's check log, not a run's logs.
- Two DAGs that react to one listing run two checks.
