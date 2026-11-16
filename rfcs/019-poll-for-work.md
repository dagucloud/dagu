# RFC 019: Poll for Work Without a Run

- **Status:** Draft
- **Date:** 2026-11-16
- **Affected areas:** `spec`, `ir`, `service/scheduler`, `schedulerstate`,
  `service/frontend` (API, UI), `specs`, and `docs`

## Summary

Let the scheduler check for work with a command, and start one run for each
new item the command lists. A check that finds nothing starts no run.

```yaml
poll:
  command: yt-dlp --flat-playlist --print id "$CHANNEL_URL"
  interval: 10m
max_active_runs: 3
steps:
  - id: encode
    command: ~/feed/encode.sh   # reads the video ID from $POLL_ITEM
```

`poll` generalizes the watched folder of Spec 082 from "files in a folder" to
"lines a command prints". Each line is an item. An item seen for the first
time starts one run, named after the item, so it runs once. Each check is
recorded in a short, bounded log of polls, not in the run history.

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
| Triggers and webhooks (RFC 004), `dagu enqueue`, REST API | The source sends an event | Nothing to fix |
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
2. Each new item starts exactly one run, despite overlapping checks, restarts,
   and lost scheduler state.
3. Concurrency comes from `max_active_runs` and queues, not from copies of a
   DAG.
4. Every check stays visible: when it ran, how long it took, what it found,
   and why it failed.
5. Bounded idle cost on the scheduler host.

## Non-goals

- Replacing RFC 006. A run that must claim work atomically still needs it.
- Running the check on a worker. See [Where the check runs](#where-the-check-runs).
- Change detection for items whose key stays the same. A new version of an
  item needs a new key.
- Pushing events. That is RFC 004.

## Design

### Declaring a poll

`poll` is a DAG field, next to `schedule` and `watch`:

| Field | Type | Default | Meaning |
| --- | --- | --- | --- |
| `poll.command` | string | required | Command that prints one item per line on stdout |
| `poll.interval` | duration | `1m` | Time between the end of one check and the start of the next; at least `10s` |
| `poll.timeout` | duration | `30s` | Longest a check may run; at most `5m` |
| `poll.key` | gojq expression | the whole line | When set, each line is parsed as JSON and the key is this expression's string result |
| `poll.backlog` | `skip` or `run` | `skip` | What the first check does with the items it lists |

A DAG may set `poll` with or without `schedule` and `watch`. `dagu validate`
rejects a missing command, an interval under `10s`, a timeout over `5m`, an
invalid gojq expression, and an unknown `backlog`.

### A check

The scheduler runs `poll.command` with the DAG's shell, working directory,
and DAG-level `env`, as the scheduler's user.

- **Output.** Stdout is read up to 1 MiB; each non-empty line is an item, up
  to 10,000 items. Output beyond either limit fails the check. Stderr is kept
  up to 4 KiB for the poll log.
- **Exit code.** A non-zero exit or a timeout fails the check. A failed check
  starts nothing and forgets nothing.
- **Keys.** Each item's key is the line, or the result of `poll.key`. A line
  that is not JSON, or a key that is not a non-empty string, fails the check.
- **New items.** An item is new when its key was not printed by the previous
  successful check. New items are enqueued in the order printed.
- **Overlap.** At most one check per DAG runs at a time. A check due while the
  previous one runs waits for it.

### The run of an item

- **Run ID.** `poll-` and a hash of the DAG name and the key. An item queued
  again after the scheduler's record was lost maps to a run that already
  exists, and nothing new starts. This is the rule `watch` uses.
- **Params.** `POLL_ITEM` holds the line and `POLL_KEY` the key. They come
  from the source, so steps read them as environment variables rather than
  interpolating them into commands, as Spec 082 says for `WATCH_FILE`.
- **Trigger type.** `poll`.
- **Placement.** Like any queued run, including `queue`, `max_active_runs`,
  and `worker_selector`.
- **Backpressure.** While the DAG has 100 or more queued runs, new items are
  not enqueued and not remembered, so the next check offers them again.
- **Once per key.** A key that disappears and comes back maps to the same run
  ID, so it does not run again while that run is in the history. A source
  whose items change under the same key puts a version in the key.

### The first check

With `backlog: skip`, the first check remembers what it lists and starts
nothing, as `watch` and Zapier do. Changing `poll.command` or `poll.key`
counts as a first check again. With `backlog: run`, every listed item is new
on the first check. A work queue wants `run`; a listing of an archive, such as
a channel's past videos, wants `skip`.

### Where the check runs

The check runs on the scheduler host, in the scheduler that holds the
scheduler lock. Only the run that does the work is placed on a worker.

RFC 006 rejected a guard command in the scheduler because the check would run
on the wrong host for distributed workers. A poll differs in what it checks:
it lists work from a source the scheduler can reach, such as an API, a remote
queue over `ssh`, or a database, and does no work itself. A source only a
worker can read still needs RFC 006.

### Suspension and removal

A suspended DAG is not checked. On resume, the next check compares against
the last successful check, so items listed meanwhile run once. A DAG removed
for good keeps its record until the scheduler's state is pruned, as `watch`
records are.

### The poll log

Each DAG keeps its latest 50 checks: start time, duration, exit code, number
of items, number of new items, the run IDs started, and the stderr excerpt.
The log is kept with the scheduler's state, not in the run history, and is
shown on the DAG page beside its runs.

After 3 failed checks in a row, the DAG page shows the latest error. A
successful check clears it.

### What is stored

| Record | Size bound | Location |
| --- | --- | --- |
| Keys from the last successful check | 10,000 keys, stored as 16-byte hashes | Scheduler state, per DAG |
| Poll log | 50 entries, 4 KiB of stderr each | Scheduler state, per DAG |
| Runs | One per new item | Run history, as today |

## Example: the feed DAG

The five copies become one DAG. The check lists job files on the machine that
holds the queue; each job runs once, at most three at a time:

```yaml
poll:
  command: ssh cloud-vm 'ls ~/feed/jobs'
  interval: 1m
  backlog: run
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
15,166, and the poll log would hold the latest 50 checks.

If the jobs were files on the scheduler host, `watch` would already do this.

## Performance

- **Frequency.** One process per polling DAG per interval. With 100 polling
  DAGs at the default 1 minute, about 100 processes a minute. Each replaces a
  full run today, which starts a process and writes a status record, logs,
  and a work directory.
- **Bound.** At most 4 checks run at once across the scheduler; a due check
  waits for a slot.
- **Idle cost.** Proportional to the number of polling DAGs. A DAG without
  `poll` costs nothing.
- **Storage.** Under 400 KiB per DAG at the bounds above; nothing per check
  in the run history.
- **Run history.** Not read on the check path. New runs are enqueued by ID.
- **Upgrade.** None. The state is created on the first check.

## Compatibility and rollout

The field is additive; a DAG without `poll` behaves as today.

1. **Spec and validation**, with a new spec beside Spec 082.
2. **Scheduler checks, keys, enqueue, and state**, sharing the `watch`
   ledger and enqueue path.
3. **Poll log** in the API and on the DAG page.
4. **Documentation**: choosing between `watch`, `poll`, triggers, and
   `discard_run_on`.

An older binary fails to load a DAG with `poll`, as it does any unknown
field, ignores the scheduler state that `poll` adds, and reads trigger type
`poll` as unknown (`ir.ParseTriggerType`).

## Verification and acceptance criteria

1. 100 checks that print nothing leave no run, no log file, and no work
   directory, and 50 entries in the poll log.
2. A check that prints three new keys starts three runs; the next check with
   the same output starts none.
3. With `max_active_runs: 3`, ten new items run at most three at a time.
4. Killing the scheduler after enqueuing and before saving its state, then
   restarting it, starts no second run for the same key.
5. A failing or timed-out check starts nothing, keeps the previous keys, and
   shows its stderr in the poll log; after three in a row, the DAG page shows
   the error.
6. With `backlog: skip`, the first check starts nothing; with `backlog: run`,
   it starts one run per item.
7. A suspended DAG is not checked; after resume, items listed during the
   suspension each run once.
8. Output over 1 MiB or 10,000 lines fails the check and starts nothing.
9. With 100 queued runs on the DAG, new items wait and are offered again.
10. An item with shell metacharacters runs no command beyond the step's own.

## Alternatives considered

- **Keep empty runs and bound them.** The Kubernetes CronJob and Dagster
  retention approach. Dagu has it already (`hist_retention_runs`). It bounds
  disk, but lists stay full of empty runs and runs that did work are removed
  with them.
- **Discard empty runs after they end** (RFC 006). Needed when only the run
  can tell, but each check still starts a process and five concurrent jobs
  still need five pollers.
- **Event-driven only** (RFC 004, Airflow 3, Prefect). Right when the source
  pushes. Many sources do not: an API without webhooks, a channel's video
  list, a queue on another machine.
- **A sensor as a special run.** A run that is hidden or pruned when it finds
  nothing. Every check would still write and delete a run record and its
  logs, and every run list would need to filter them.
- **Airflow-style sensor modes.** They free worker slots during a wait, which
  Dagu does not spend. They do not remove runs.
- **Extending `watch` with a `command`.** One concept instead of two, but a
  folder and a command differ in arrival detection, first-check behavior, and
  keys. They share the ledger and enqueue path internally.

## Consequences

- One polling DAG replaces N copies of a poller, and its run history holds
  only runs that did work.
- The scheduler runs user commands outside runs. A slow or hung check costs a
  scheduler slot until its timeout.
- A check's output decides what runs, so a bug in the command that prints
  stale keys starts nothing, and one that prints new keys every time starts a
  run every time. The poll log shows both.
- Debugging a check means reading the poll log, not a run's logs.

## Open questions

1. Is `poll` the right name, or `sensor`, as Dagster calls it?
2. Should the check run on a worker selected by `worker_selector`, for
   sources only a worker can reach?
3. Should `backlog` default to `run`, since a work queue is the motivating
   case?
4. Should three failed checks in a row send the DAG's failure notification?
5. Should a check receive the DAG's `secrets`, or only `env`?
6. Should a key be allowed to run again after its run leaves the history, or
   once per key forever, using the remembered hashes?
