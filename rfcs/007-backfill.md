# RFC 007: Backfill

- **Status:** Proposed
- **Date:** 2026-11-10
- **Affected areas:** `service/scheduler`, `intake`, `persis` (backfill
  store), `service/frontend` (API, UI), `service/mcp`, `cmd`, `audit`,
  `specs`, and `docs`

## Summary

Run a scheduled DAG for every slot in a past range with one command:

```sh
dagu backfill etl --from 2026-10-01 --to 2026-10-31
```

A backfill lists the schedule's fire times in the range, skips the slots that
already have a run, and queues one run per remaining slot, oldest first. Each
run carries its slot's scheduled time and window (RFC 006), so the DAG needs
no date params. The backfill is a record: it can be previewed, watched,
cancelled, and submitted again without starting anything twice.

## Problem statement

Catchup replays the slots a scheduler missed while it was down, inside
`catchup_window`. It does not cover what operators ask for most:

- **A new DAG** that must load the last 90 days before its first scheduled
  run.
- **A fixed bug.** Three weeks of runs produced wrong output and must run
  again.
- **A paused scheduler or a suspended DAG.** Both discard the slots that
  passed. Nothing replays them.
- **An outage longer than `catchup_window`.**

Today each case is a shell loop around `dagu start` with a date param. The
loop has no preview, no record of which days are done, no protection against
running a day twice, and no way to stop.

dagucloud/dagu#2645 asks for partitions and resumable backfills, and
dagucloud/dagu#695 for runs between a start and an end date. This RFC covers
the time-range part of both.

## Goals

1. One operation runs every slot in a range, in order.
2. Submitting the same backfill twice starts nothing twice.
3. A backfill can be previewed before it starts and cancelled while it runs.
4. A backfill survives a scheduler restart.
5. Backfill runs respect the DAG's queue, so they cannot overload the server.

## Non-goals

- Non-time partitions: static keys, dynamic keys, and several dimensions
  (dagucloud/dagu#2645). A later RFC can add them on the backfill record.
- Backfilling a DAG that has no schedule.
- Automatic backfill when a DAG's definition changes.
- Running a slot with the DAG definition that was current at the slot's
  time. A backfill uses the definition current when each run starts.

## Model

A backfill is a record with an ID, the DAG, the range, the options, the actor,
and one entry per slot.

- **Slots** are the fire times of the DAG's active start schedule entries in
  `[from, to]`, with business calendars applied. One-off `at` entries, and
  `stop` and `restart` schedules, produce no slots.
- **A slot is identified** by the DAG and its scheduled time.
- **Selection.** `--rerun` decides which slots get a run:

  | Value | Slots that get a run |
  | --- | --- |
  | `missing` (default) | Slots with no run. |
  | `failed` | Also slots whose latest run did not succeed. |
  | `all` | Every slot. |

  A slot with a queued or running run is never selected.
- **Each selected slot** gets one new queued run with that scheduled time and
  window. Earlier runs of the slot stay in history.
- **Run IDs** are derived from the backfill ID and the slot time:
  `backfill-{backfill id}-{timestamp}`. Resuming a backfill after a crash
  finds the runs it already created.
- **Trigger type.** The run's `context.trigger.type` is `backfill`, a new
  value. The actor is the user who submitted the backfill.

### Finding the runs of a slot

Selection needs every run of a slot, whatever started it: the scheduler,
catchup, a manual run with `--scheduled-at`, or an earlier backfill.

**To verify before shipping:** whether the run store can find runs by DAG and
scheduled time. Catchup finds its own runs by a derived run ID only. If the
store cannot, this RFC adds an index from slot to run IDs, written when a run
with a scheduled time is created.

## Commands

```sh
dagu backfill <DAG> --from <time> [--to <time>] [flags]
dagu backfill ls [<DAG>]
dagu backfill status <backfill id>
dagu backfill cancel <backfill id>
```

| Flag | Meaning | Default |
| --- | --- | --- |
| `--from` | First slot, inclusive. A date or an RFC 3339 time. | required |
| `--to` | Last slot, inclusive. | now |
| `--rerun` | `missing`, `failed`, or `all`. | `missing` |
| `--max-active` | Backfill runs of this backfill allowed at once. | `1` |
| `--params` | Params for every run. | the DAG's defaults |
| `--profile` | Runtime profile for every run. | the DAG's default |
| `--stop-on-failure` | End the backfill at the first slot that fails. | off |
| `--dry-run` | Print the plan and start nothing. | off |
| `--yes` | Skip the confirmation. | off |

- A date without a time means the start of that day for `--from` and the end
  of that day for `--to`, in the schedule's zone.
- A range that ends in the future is rejected.
- A backfill of more than 1,000 slots is rejected, the same bound as the
  catchup buffer. Larger ranges are split.
- The plan lists each slot with one of `run`, `skip: succeeded`,
  `skip: active`, or `skip: failed`, and the totals. Without `--yes`, the
  command prints the plan and asks before starting.

The API mirrors the commands under `/dags/{fileName}/backfills`. The MCP
execute tool gains a `backfill` action with the same fields, where `dry_run`
returns the plan.

## Execution

```text
submit ─▶ plan ─▶ backfill record ─▶ scheduler ─▶ enqueue slot runs ─▶ queue
                                        ▲                                │
                                        └──────── run finished ◀─────────┘
```

- **The scheduler owns dispatch.** The scheduler that holds the scheduler
  lock reads open backfills each tick. It enqueues the next slots, oldest
  first, while fewer than `--max-active` runs of the backfill are queued or
  running.
- **Order.** With `--max-active 1`, slot N+1 is enqueued only after slot N
  finishes. Incremental loads depend on this.
- **Queues.** Backfill runs go through the DAG's queue and count against its
  `max_concurrency`. Queues must be enabled, as for catchup.
- **Scheduled runs keep running.** A backfill does not block the DAG's live
  schedule. The two share the queue.
- **`skip_if_successful` and `overlap_policy` do not apply** to backfill
  runs. The backfill's own selection and `--max-active` replace them.
- **Failure.** A failed slot does not stop the backfill unless
  `--stop-on-failure` is set. The DAG's own retry policy applies to each run
  first.
- **Cancel** stops enqueueing and removes the backfill's queued runs. Runs
  already executing finish.
- **Pause.** A paused scheduler enqueues no backfill runs. The backfill
  continues after the resume; nothing is discarded.
- **A suspended DAG** can be backfilled. Suspension stops its schedule, not
  an operator's request.
- **A DAG renamed or deleted** during a backfill ends the backfill with the
  remaining slots marked `cancelled`.

A backfill ends as `succeeded` when every selected slot succeeded, `failed`
when any did not, or `cancelled`.

## UI

- **Backfill** on a DAG opens a dialog with the range, `rerun`, and
  concurrency. The plan updates as the fields change.
- The DAG's history gains a **Backfills** tab: each backfill with its actor,
  range, progress, and a grid of slots linking to their runs.
- Run lists show a `backfill` badge and link to the backfill.

## Security

- Starting a backfill needs the permission to run the DAG. Cancelling needs
  the same.
- The audit log records submit and cancel with the range and the slot count.
- The slot bound keeps one request from queueing an unbounded number of runs.

## Compatibility and rollout

| Release | Ships |
| --- | --- |
| First | CLI, API, backfill record, scheduler dispatch, `--dry-run`. Requires the first release of RFC 006. |
| Second | UI dialog, Backfills tab, and the MCP action. |

- `backfill` is a new value of `context.trigger.type` in spec 017.
- Catchup is unchanged. A slot that catchup already ran is skipped by a
  `missing` backfill.

## Verification and acceptance criteria

1. A daily DAG backfilled over 31 days gets 31 runs, each with its own
   `scheduled_at` and window, started in date order.
2. Submitting the same range again with `--rerun missing` starts nothing.
3. With `--rerun failed`, only the slots whose latest run did not succeed
   run again.
4. `--dry-run` starts nothing and prints the same plan the real submit then
   executes.
5. Killing the scheduler mid-backfill and restarting it finishes the
   backfill with no slot run twice.
6. `dagu backfill cancel` leaves no queued run of the backfill.
7. With a weekday calendar, weekend slots do not appear in the plan.
8. A slot that catchup already ran is listed as `skip: succeeded`.
9. A backfill of 1,001 slots is rejected with the count and the bound.
10. A distributed DAG backfills through the coordinator like any queued run.

## Alternatives considered

- **Raise `catchup_window`.** Catchup replays only what the scheduler missed
  since its watermark. It cannot rerun a slot that ran, or reach before the
  watermark.
- **A shell loop over `dagu start --scheduled-at`.** Works for a few days.
  It has no plan, no record, no resume, and no cancel.
- **Derive run IDs from the slot alone,** as catchup does. A slot could then
  have one run only, which rules out `--rerun failed` and `all`.
- **Retry the slot's failed run instead of creating a new run.** A retry
  reuses the DAG snapshot of the failed run. Backfills after a bug fix need
  the current definition.
- **Partitions first** (dagucloud/dagu#2645). A larger model. Time ranges on
  an existing schedule cover the common case and need no new DAG syntax.

## Consequences

- Loading history and repairing a bad range become one operation with a
  record.
- A slot can have several runs. Views that show a slot must show its latest
  run.
- The scheduler gains a second dispatch source beside schedules and catchup.

## Open questions

1. Should a DAG with no schedule be backfilled with `--every 1d`?
2. Should `--max-active` above 1 be allowed to run slots out of order, or
   only start them in order?
3. Should a backfill pin the DAG definition at submit, so an edit during a
   long backfill does not change later slots?
4. Should backfill runs get a lower queue priority than scheduled runs?
