# RFC 019: Poll for Work Without a Run

- **Status:** Draft
- **Date:** 2026-11-16, revised 2026-11-23 as a source type of RFC 004
- **Depends on:** [RFC 004](004-triggers.md): sources, triggers, and the
  matcher
- **Affected areas:** `spec`, `persis`, `service/scheduler`,
  `schedulerstate`, `eventstore`, `service/frontend` (API, UI), `cmd`,
  `specs`, and `docs`

## Summary

Let the scheduler check for work with a command, and start one run for each
new item the command lists. A check that finds nothing starts no run.

A `command` source is a pull source of RFC 004. It runs a command at an
interval, and each new line it prints becomes one event. DAGs subscribe to it
with triggers.

```json
{
  "name": "channel-videos",
  "type": "command",
  "command": "yt-dlp --flat-playlist --print id $CHANNEL_URL",
  "env": { "CHANNEL_URL": "https://www.youtube.com/@example" },
  "interval": "10m"
}
```

```yaml
# encode.yaml
triggers:
  - source: channel-videos
max_active_runs: 3
steps:
  - id: encode
    command: ~/feed/encode.sh   # reads the video ID from $POLL_ITEM
```

An item seen for the first time becomes one event, named after the item, so
it runs once in each subscribing DAG. Each check is recorded in a short,
bounded log on the source, not in the run history. A source that several DAGs
subscribe to runs one check for all of them.

## Problem statement

The usual way to react to work that sends no event is a scheduled DAG that
checks for work and exits when there is none. Every check is a full run: a
process, a status record, logs, and a work directory.

One showcased setup runs a live video channel on a 1 GB VM
(https://dagu.sh/showcase/live-channel-on-a-1-gb-vm). Five copies of a feed
DAG run every 5 minutes. In 15 days the nine DAGs made 16,029 runs. The feed
DAG made 15,166 of them, and 257 did real work. Most of the rest found no job
and ended in about 2 seconds (RFC 006).

Capacity grew by copying the DAG file, from one copy to five. Dagu already
caps concurrency with `max_active_runs` and queues, but a schedule starts runs
by the clock, not by the job, so more capacity means more pollers and more
empty runs.

Current and proposed options:

| Option | Fits when | Leaves |
| --- | --- | --- |
| `watch` (Spec 082) | Work arrives as files in a folder the scheduler can read | Nothing to fix |
| Push sources (RFC 004), `dagu enqueue`, REST API | The source sends an event | Nothing to fix |
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

1. A check that finds no new work leaves no run, no logs, and no work
   directory.
2. Each new item starts exactly one run per subscribing DAG, despite
   overlapping checks, restarts, and lost scheduler state.
3. Concurrency comes from `max_active_runs` and queues, not from copies of a
   DAG.
4. Every check stays visible: when it ran, how long it took, what it found,
   and why it failed.
5. Bounded idle cost on the scheduler host.
6. One check per source, however many DAGs subscribe.

## Non-goals

- Replacing RFC 006. A run that must claim work atomically still needs it.
- Running the check on a worker. See [Where the check runs](#where-the-check-runs).
- Change detection for items whose key stays the same. A new version of an
  item needs a new key.
- Incremental listing. A source too large to list whole, such as a feed read
  from a cursor, needs a later option that passes the last cursor to the
  check. Event IDs do not change with it.
- Other pull types. An `http` type, which needs no process per check, can
  follow on the same fields.
- Pushing events. That is RFC 004.

## Design

### Declaring a source

A `command` source is a source of RFC 004, kept by the server. Only an admin
creates or changes one, since its command runs on the scheduler host.

| Field | Type | Default | Meaning |
| --- | --- | --- | --- |
| `name` | string | required | The source's name, which triggers use |
| `type` | string | required | `command` |
| `command` | string | required | Command that prints one item per line on stdout |
| `env` | map | none | Variables the command receives |
| `working_dir` | path | the DAG directory | Where the command runs |
| `interval` | duration | `1m` | Time between the end of one check and the start of the next; at least `10s` |
| `timeout` | duration | `30s` | Longest a check may run; at most `5m` |
| `key` | gojq expression | the whole line | When set, each line is parsed as JSON and the key is this expression's string result |
| `backlog` | `skip` or `run` | `skip` | What the first check does with the items it lists |

`interval`, `timeout`, `key`, and `backlog` belong to every pull type;
`command`, `env`, and `working_dir` belong to this one. The API and
`dagu source` reject a missing command, an interval under `10s`, a timeout
over `5m`, an invalid gojq expression, and an unknown `backlog`.

A DAG subscribes with a trigger that names the source, as in the
[Summary](#summary). The trigger's `condition` and `params` read the item's
payload:

```yaml
triggers:
  - source: channel-videos      # with key: .id and one JSON object per line
    condition: '.item.duration > 60'
    params:
      VIDEO_ID: .key
```

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
- **Runs.** The matcher of RFC 004 starts one run per subscribing trigger,
  placed like any queued run, including `queue`, `max_active_runs`, and
  `worker_selector`. A DAG that is suspended or has 100 queued runs gets the
  event later, within event store retention.
- **Once per key.** A key that disappears and comes back has the same event
  ID, so it does not run again while that run is in the history. A source
  whose items change under the same key puts a version in the key.

### The first check

With `backlog: skip`, the first check remembers what it lists and writes no
event, as `watch` and Zapier do. Changing `command` or `key` counts as a first
check again, and so does deleting the source and creating it again. With
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

A source is checked while some enabled trigger of an unsuspended DAG names
it. When the last such trigger goes, checks stop. When one comes back, the
next check compares against the last successful check, so items listed
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

## Example: the feed DAG

The five copies become one source and one DAG. The check lists job files on
the machine that holds the queue; each job runs once, at most three at a time:

```json
{
  "name": "feed-jobs",
  "type": "command",
  "command": "ssh cloud-vm 'ls ~/feed/jobs'",
  "interval": "1m",
  "backlog": "run"
}
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

## Performance

- **Frequency.** One process per source per interval, however many DAGs
  subscribe. With 100 sources at the default 1 minute, about 100 processes a
  minute. Each replaces a full run today, which starts a process and writes a
  status record, logs, and a work directory.
- **Bound.** At most 4 checks run at once across the scheduler; a due check
  waits for a slot.
- **Idle cost.** Proportional to the number of sources with an active
  subscriber. A source nobody subscribes to costs nothing.
- **Storage.** Under 400 KiB per source at the bounds above, and nothing per
  check in the run history. One event per new item, holding its line, kept
  for the event store's retention.
- **Run history.** Not read on the check path. The matcher enqueues runs by
  derived ID.
- **Upgrade.** None. The state is created on the first check.

## Compatibility and rollout

Sources and triggers are additive; a DAG without triggers behaves as today.
`command` is the first source type on RFC 004's model, so the model ships
with it.

1. **RFC 004's model:** the source collection with its API, UI, and CLI,
   triggers, the matcher, and `DAGU_EVENT_FILE`, each with a spec.
2. **The `command` type:** checks, keys, events, and state, sharing the
   `watch` ledger code.
3. **The check log and `dagu source check`** in the API, the CLI, and on the
   source's page.
4. **Documentation:** choosing between `watch`, `command` sources, push
   sources, and `discard_run_on`.

An older binary fails to load a DAG with `triggers`, as it does any unknown
field, ignores the source collection and the scheduler state this type adds,
and reads the run trigger type `trigger` as unknown (`ir.ParseTriggerType`).

## Verification and acceptance criteria

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
11. An item with shell metacharacters runs no command beyond the step's own.
12. `dagu source check` lists the items and the runs they would start, and a
    check right after it still finds the same items new.

## Alternatives considered

- **Keep empty runs and bound them.** The Kubernetes CronJob and Dagster
  retention approach. Dagu has it already (`hist_retention_runs`). It bounds
  disk, but lists stay full of empty runs and runs that did work are removed
  with them.
- **Discard empty runs after they end** (RFC 006). Needed when only the run
  can tell, but each check still starts a process and five concurrent jobs
  still need five pollers.
- **Push sources only** (RFC 004, Airflow 3, Prefect). Right when the source
  pushes. Many sources do not: an API without webhooks, a channel's video
  list, a queue on another machine.
- **A sensor as a special run.** A run that is hidden or pruned when it finds
  nothing. Every check would still write and delete a run record and its
  logs, and every run list would need to filter them.
- **Airflow-style sensor modes.** They free worker slots during a wait, which
  Dagu does not spend. They do not remove runs.
- **A `poll` field on the DAG** (the first draft of this RFC). The check and
  its state would belong to one DAG: two DAGs that react to one listing would
  run two checks against the same API, and `poll` would be a third way to
  start runs beside schedules and triggers.
- **Extending `watch` with a `command`.** One concept instead of two, but a
  folder and a command differ in arrival detection, first-check behavior, and
  keys. They share the ledger code.

## Consequences

- One source replaces N copies of a poller, and the history holds only runs
  that did work.
- The scheduler runs user commands outside runs. A slow or hung check costs a
  scheduler slot until its timeout.
- A check's output decides what runs, so a bug in the command that prints
  stale keys starts nothing, and one that prints new keys every time starts a
  run every time. The check log shows both.
- Debugging a check means reading the source's check log, not a run's logs.
- New items wait in the event store while a DAG is saturated. A DAG that stays
  saturated longer than the event store's retention misses them, and they are
  counted as missed.

## Open questions

1. Is `command` the right type name, or `poll`?
2. Should a source take `worker_selector`, to run its check on a worker for
   sources only a worker can reach?
3. Should `backlog` default to `run`, since a work queue is the motivating
   case?
4. Should three failed checks in a row notify, and through what: the
   subscribing DAGs' failure notification, or the source's own?
5. Should a source take `secrets`, as a DAG does, or only `env`?
6. Should a key be allowed to run again after its run leaves the history, or
   once per key forever, using the remembered hashes?
