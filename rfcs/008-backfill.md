# RFC 008: Backfill

- **Status:** Proposed
- **Date:** 2026-11-10
- **Affected areas:** `service/scheduler`, `intake`, `queue`, `persis`
  (backfill store, slot index), `notification`, `service/frontend` (API, UI),
  `service/mcp`, `cmd`, `audit`, `specs`, and `docs`

## Summary

Run a scheduled DAG for every slot in a past range with one command:

```sh
dagu backfill start etl --from 2026-10-02 --to 2026-11-01
```

A backfill lists the schedule's fire times in the range, skips the slots that
already have a run, and queues one run per remaining slot, oldest first. Each
run carries its slot's scheduled time and window (RFC 007), so the DAG needs
no date params.

The range selects fire times. A job that fires at 02:00 and loads the previous
day covers October with the fires of October 2 through November 1. The plan
shows the dates each slot will load before anything starts.

The backfill is a record: it can be previewed, watched, resumed, cancelled,
and submitted again without starting anything twice.

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
2. Submitting the same backfill twice, even while the first is open, starts
   nothing twice.
3. A backfill can be previewed before it starts, cancelled while it runs, and
   resumed after it stops.
4. A backfill survives a scheduler restart.
5. Backfill runs respect the DAG's queue, so they cannot overload the server.
6. Live runs are neither skipped because of a backfill nor queued behind one.
7. The plan states what each slot will load.

## Non-goals

- Non-time partitions: static keys, dynamic keys, and several dimensions
  (dagucloud/dagu#2645). A later RFC can add them on the backfill record.
- Backfilling a DAG that has no schedule. A later release can add
  `--schedule '<cron>'`, which uses the same slot model.
- Automatic backfill when a DAG's definition changes.
- Running a slot with the DAG definition that was current at the slot's
  time. A backfill pins the definition at submit.

## Model

A backfill is a record with an ID, the DAG, the range, the options, the actor,
the pinned definition, and one entry per slot.

- **Slots** are the fire times of the DAG's active start schedule entries
  (RFC 007) in `[from, to]`, with business calendars applied. One-off `at`
  entries, and `stop` and `restart` schedules, produce no slots.
- **The range selects fire times, not data dates.** What a slot loads depends
  on the DAG's `dates`. With `target_day: { shift: -1d }` on a 02:00 schedule,
  the fires of October 1 through 31 load September 30 through October 30. The
  plan shows this, so an off-by-one is visible before the submit.
- **A slot is identified** by the DAG and its scheduled time.
- **Selection.** `--rerun` decides which runs of a slot are redone:

  | Value | Slots that get a run |
  | --- | --- |
  | `none` (default) | Slots with no run. |
  | `failed` | Also slots whose latest run did not succeed. |
  | `all` | Every slot. |

  A slot is never selected while it has a queued, running, or waiting run, or
  while it is pending in another open backfill. A partially succeeded run
  counts as succeeded. A run that never started counts as no run: one that was
  dequeued, cancelled while queued, or dropped by a pause or suspension.
- **Each selected slot** gets one new queued run with that scheduled time and
  window. Earlier runs of the slot stay in history.
- **Pinned at submit.** The record holds the DAG definition and each slot's
  window, so an edit or a git sync during a backfill does not change later
  slots. Sub-DAG definitions resolve when each run starts. To change a pinned
  definition, use `resume` or submit again.
- **IDs.** A backfill ID is 8 characters, short enough to type and to keep run
  IDs within the 64-character limit. Run IDs are
  `backfill-{backfill id}-{generation}-{timestamp}`. The generation counts
  resumes, so a resumed slot gets a new run. After a crash, the scheduler finds
  the runs it already created.
- **Trigger type.** The run's `context.trigger.type` is `backfill`, a new
  value. The actor is the user who submitted the backfill.

### Slot index

The run store lists runs by DAG and start time, not by scheduled time, and
retention removes runs that selection still needs. A per-DAG slot index records
the runs of each slot, whatever started them: the scheduler, catchup, a manual
run on a chosen or default slot (RFC 007), or a backfill.

- **Written** when a top-level run with a scheduled time is created, and
  updated as its status changes. An entry holds the run ID, status, and trigger
  type. A sub-DAG run takes its parent's slot and is not indexed.
- **Outlives run history.** Run retention does not touch the index. Entries
  last `slot_retention_days`, 730 by default, which sits beside
  `hist_retention_days`. A slot whose run was pruned keeps its status.
- **Earlier schedules.** A slot with no exact match uses the latest run of an
  earlier recurring schedule, fired live, by catchup, or by a backfill, whose
  scheduled time falls strictly between the slot's previous fire and its own.
  After a schedule moves from 02:00 to 03:00, the 02:00 runs still cover their
  days. A manual run or a one-off `at` fire at another time covers nothing.
- **No record.** When the range starts before the earliest indexed slot of a
  DAG that has one, the plan warns that older slots have no record and will
  run.
- **Catchup and backfill dispatch check it** under the scheduler lock, so
  neither starts a slot the other holds.

## Commands

```sh
dagu backfill start <DAG> (--from <time> [--to <time>] | --last <duration>) [flags]
dagu backfill ls [<DAG>]
dagu backfill status <backfill id> [--watch]
dagu backfill set <backfill id> --max-concurrency <n>
dagu backfill resume <backfill id> [--yes]
dagu backfill cancel <backfill id> [--stop-running]
```

| Flag | Meaning | Default |
| --- | --- | --- |
| `--from` | First slot, inclusive. A date, a wall-clock time, or an RFC 3339 time. | required without `--last` |
| `--to` | Last slot, inclusive. | now |
| `--last` | The range ending now, such as `90d`. Units are `min`, `h`, `d`, `w`, `mo`, and `y`. Excludes `--from` and `--to`. | none |
| `--rerun` | `none`, `failed`, or `all`. | `none` |
| `--max-concurrency` | Runs of this backfill allowed at once. | `1` |
| `--on-failure` | `stop` or `continue`. | `stop` at concurrency 1, else `continue` |
| `--params` | Params for every run. | the DAG's defaults |
| `--profile` | Runtime profile for every run. | the DAG's default |
| `--dry-run` | Print the plan and start nothing. | off |
| `--format` | Plan output: `text` or `json`. | `text` |
| `--yes` | Skip the confirmation. | off |

- A date without a time means the start of that day for `--from` and the end
  of that day for `--to`. Forms without an offset follow the zone rules of
  `--scheduled-at` (RFC 007).
- A `--to` in the future, including today's date, is clamped to now, and the
  plan prints the clamped end. Fires in the current minute or later are left to
  the live schedule. A `--from` in the future is rejected.
- A backfill over `scheduler.backfill_max_slots` slots, 10,000 by default, is
  rejected with the count and the bound. Slot progress is appended to the
  record, so a large backfill does not rewrite it on each run.
- Without `--yes`, the command prints the plan and asks before starting. With
  no terminal to ask on, it fails and names `--yes`.
- `backfill start` exits 0 when the backfill is accepted or nothing needs to
  run. `status --watch` follows the backfill and exits 0 only when it ends
  `succeeded`.

### Plan

```text
Backfill etl, Asia/Tokyo, --rerun none

Fires       2026-10-02 02:00 to 2026-11-01 02:00
Windows     2026-10-01 02:00 to 2026-11-01 02:00
target_day  2026-10-01 to 2026-10-31

31 slots: run 28, skip succeeded 2, skip active 1
Concurrency 1 (queue limit 1), on failure stop
Estimate 2h 20m from recent runs

SLOT              WINDOW START      target_day  ACTION
2026-10-02 02:00  2026-10-01 02:00  2026-10-01  run
2026-10-03 02:00  2026-10-02 02:00  2026-10-02  skip: succeeded
2026-10-04 02:00  2026-10-03 02:00  2026-10-03  run
...
2026-10-30 02:00  2026-10-29 02:00  2026-10-29  run
2026-10-31 02:00  2026-10-30 02:00  2026-10-30  skip: active
2026-11-01 02:00  2026-10-31 02:00  2026-10-31  run

Start 28 runs? [y/N]
```

- The header gives the range of fire times, of windows, and of each entry in
  `dates`. The rows show the first and last three slots. `--format json` prints
  every slot.
- Each slot has one of `run`, `skip: succeeded`, `skip: failed`, or
  `skip: active`. A skip that relies on a run from an earlier schedule says so.
- **Concurrency** is the lower of `--max-concurrency` and the queue's limit.
  The plan warns when the queue caps it.
- **The estimate** uses the median duration of the DAG's recent successful
  runs. It is omitted when there is no history.
- **Warnings** appear above the prompt: no scheduler running, a capped
  concurrency, and slots with no record.

### API and MCP

| Endpoint | Purpose |
| --- | --- |
| `POST /dags/{fileName}/backfills` | Submit. `dry_run` returns the plan. |
| `GET /backfills` | List, with an optional DAG filter. |
| `GET /backfills/{id}` | Status. |
| `PATCH /backfills/{id}` | Change concurrency. |
| `POST /backfills/{id}/resume` | Resume. `dry_run` returns the plan. |
| `POST /backfills/{id}/cancel` | Cancel, with an optional `stop_running`. |

The MCP execute tool gains `backfill`, `backfill_set`, `backfill_resume`, and
`backfill_cancel`, where `dry_run` returns the plan. The read tool gains a
`backfills` target, so an agent can watch and stop what it starts.

## Execution

```text
submit ─▶ plan ─▶ backfill record ─▶ scheduler ─▶ enqueue slot runs ─▶ queue
                                        ▲                                │
                                        └──────── run finished ◀─────────┘
```

- **Submit is atomic.** Planning and writing the record happen under a per-DAG
  lock, so two submits cannot select the same slot. A submit with the same
  range and options as an open backfill returns that backfill's ID and starts
  nothing. A submit that selects no slots says so and creates no record.
- **The scheduler owns dispatch.** The scheduler that holds the scheduler
  lock reads open backfills each tick. It enqueues the next slots, oldest
  first, while fewer than `--max-concurrency` runs of the backfill are queued,
  running, or waiting.
- **Recheck at enqueue.** A slot that gained a run after planning, and that the
  backfill's `--rerun` would now skip, is marked `skipped`.
- **No scheduler.** A backfill waits until a scheduler runs. Submit warns and
  names `dagu scheduler` and `dagu start-all`, and `status` reports
  `waiting for scheduler`.
- **Order.** With `--max-concurrency 1`, slot N+1 is enqueued only after slot N
  finishes. Above 1, slots start in order and may finish out of order.
- **Queues.** Backfill runs go through the DAG's queue and count against its
  limit. Queues must be enabled, as for catchup. Otherwise submit fails and
  names `queues.enabled`.
- **Live runs come first.** A queued or running backfill run does not hold back
  a live slot, and `skip_if_successful` ignores backfill runs. Backfill runs are
  low priority in the queue. All other runs are high priority, so they keep
  their order among themselves and start before queued backfill runs. A running
  backfill run is not preempted.
- **`skip_if_successful` and `overlap_policy` do not apply** to backfill
  runs. The backfill's own selection and `--max-concurrency` replace them.
- **Failure.** `--on-failure stop` enqueues nothing more after a slot fails.
  Active runs finish and the remaining slots are marked `not run`. `continue`
  goes on to the next slot. The default is `stop` at concurrency 1, because a
  gap breaks incremental loads. The DAG's own retry policy applies to each run
  first.
- **Waiting.** A run waiting for a person counts against concurrency and holds
  the slot order. `status` and the UI show `blocked: waiting approval`.
- **Set** changes the concurrency of an open backfill from the next dispatch.
  Lowering it does not stop active runs.
- **Cancel** stops enqueueing and removes the backfill's queued runs. Their
  slots count as missing again. Runs already executing finish, unless
  `--stop-running` stops them too.
- **Resume** applies to a backfill that ended `failed` or `cancelled`. It keeps
  the record and options, pins the current definition so a fix reaches the
  remaining slots, shows the plan, and runs the slots that have not succeeded,
  in order.
- **Pause.** A paused scheduler enqueues no backfill runs and holds those
  already queued. They are neither started nor aborted, do not block other runs
  in the queue, and start when the scheduler resumes. Nothing is discarded.
- **A suspended DAG** can be backfilled. Suspension stops its schedule, not an
  operator's request. The dispatcher must tell a pause from a suspension, which
  the existing check reports alike.
- **A DAG renamed or deleted** during a backfill ends the backfill with the
  remaining slots marked `cancelled`.
- **Notifications.** `mail_on` and notification routes do not fire per backfill
  run, except for `wait`, where a person must act. The backfill sends one
  summary through the same channels when it ends, with the totals and the
  failed slots. `handler_on` steps still run because they can hold cleanup. A
  DAG can branch on `context.trigger.type`.

A backfill ends as `succeeded` when every selected slot succeeded or was
skipped by the recheck, `failed` when any did not succeed, or `cancelled`. A
finished backfill's record is kept for `slot_retention_days`.

## UI

- **Backfill** on a DAG opens a dialog with the range, `rerun`, concurrency,
  and failure behavior. It shows the same plan as the CLI, updated as the
  fields change.
- **History.** Selecting a range of runs offers **Backfill**, prefilled with
  that range.
- **Missed slots.** After a pause, a suspension, or an outage longer than
  `catchup_window`, the DAG page offers **N missed slots: Backfill**, prefilled
  with the gap.
- The DAG's history gains a **Backfills** tab: each backfill with its actor,
  range, progress, controls to cancel, resume, and change concurrency, and a
  grid of slots linking to their runs. A slot whose run was pruned shows its
  status without a link.
- Run lists show a `backfill` badge and link to the backfill.

## Security

- Starting or resuming a backfill needs the permission to run the DAG.
  Cancelling it and changing its concurrency need the same.
- The audit log records submit, resume, set, and cancel with the range and the
  slot count.
- The slot bound caps the size of one backfill record. Dispatch keeps at most
  `--max-concurrency` runs of a backfill in the queue, so one request cannot
  flood it.

## Compatibility and rollout

| Release | Ships |
| --- | --- |
| First | CLI, API, backfill record, slot index, scheduler dispatch, and the plan. Requires the first release of RFC 007. |
| Second | UI dialog, Backfills tab, history selection, the missed-slots prompt, and the MCP actions. |

- `backfill` is a new value of `context.trigger.type` in spec 017.
- The plan shows `dates` once the second release of RFC 007 ships. Until then
  it shows windows.
- Catchup changes in one way: it skips a slot whose latest backfill run is
  active or succeeded. As RFC 007 requires, a manual run does not suppress it.
  A slot that catchup already ran is skipped by a default backfill.
- Runs other than backfill runs move from low to high queue priority. Their
  order among themselves does not change.
- Runs still in history that have a scheduled time are added to the index on
  first use. Runs without one, including today's shell loops, are not visible
  to selection, so their slots plan as `run`.

## Verification and acceptance criteria

1. A daily DAG backfilled over 31 days gets 31 runs, each with its own
   `scheduled_at` and window, started in date order.
2. Submitting the same range again after it ends, with `--rerun none`, starts
   nothing and creates no record.
3. Submitting the same range while the first backfill is open returns its ID
   and starts nothing. An overlapping submit lists its pending slots as
   `skip: active`.
4. With `--rerun failed`, slots with no run and slots whose latest run did not
   succeed run. A slot waiting for a person does not.
5. `--dry-run` starts nothing and prints the same plan the real submit then
   executes.
6. For a DAG with `target_day: { shift: -1d }`, the plan shows each slot's
   resolved date and the first and last date in its header.
7. Killing the scheduler mid-backfill and restarting it finishes the
   backfill with no slot run twice.
8. A live slot that fires while a backfill run is running or queued gets its
   run, and starts before the queued backfill runs.
9. A slot that gains a successful run after planning is marked `skipped` when
   its turn comes.
10. `dagu backfill cancel` leaves no queued run of the backfill, and
    `dagu backfill resume` runs the slots that were queued at the cancel.
    `--stop-running` also stops active runs.
11. At concurrency 1, a failed slot stops the backfill by default. After the
    DAG is fixed, `resume` runs the remaining slots with the new definition.
12. An edit to the DAG during a backfill does not change later slots.
13. Runs queued when the scheduler is paused are neither started nor aborted,
    and start when it resumes.
14. A suspended DAG's backfill runs complete.
15. A slot whose successful run has passed `hist_retention_days` is listed as
    `skip: succeeded`.
16. After a schedule moves from 02:00 to 03:00, earlier days are listed as
    skipped, noting the earlier schedule.
17. With a weekday calendar, weekend slots do not appear in the plan.
18. A slot that catchup already ran is listed as `skip: succeeded`.
19. A backfill over `scheduler.backfill_max_slots` is rejected with the count
    and the bound. An hourly DAG over 90 days is accepted by default.
20. `--to` set to today's date is clamped to now.
21. Without a terminal or `--yes`, `backfill start` fails and names `--yes`.
22. Submitting with no scheduler running warns, and the backfill starts when a
    scheduler comes up.
23. A backfill with several failed slots sends one summary and no per-run mail.
24. A distributed DAG backfills through the coordinator like any queued run.

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
- **Select the range by data date.** The date a slot loads depends on the DAG's
  `dates`, so the scheduler cannot map a date to a slot without guessing. The
  plan shows both.
- **Select from the run store alone.** Retention removes the runs that prove a
  slot is done, so old slots would rerun.
- **`dagu backfill <DAG>` without a verb.** Subcommands would shadow DAGs named
  `ls`, `status`, or `cancel`, and each new subcommand could break another DAG.

## Consequences

- Loading history and repairing a bad range become one operation with a
  record.
- A slot can have several runs. Views that show a slot must show its latest
  run.
- The scheduler gains a second dispatch source beside schedules and catchup.
- The slot index is new durable state, written for every scheduled run.
- A live run and a backfill run of one DAG can be active together on a queue
  whose limit is above 1. A DAG that assumes one active run must keep the
  limit at 1.
