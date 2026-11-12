# RFC 008: Backfill

- **Status:** Proposed
- **Date:** 2026-11-10
- **Affected areas:** `service/scheduler`, `intake`, `queue`, `persis`
  (backfill store, slot index), `notification`, `service/frontend` (API, UI),
  `service/mcp`, `runtime`, `cmd`, `audit`, `specs`, and `docs`

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
day covers October with the fires of October 2 through November 1.
`--by target_day --from 2026-10-01 --to 2026-10-31` selects the same slots by
the dates they load. The plan shows those dates before anything starts.

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
6. A live slot is never skipped because of a backfill and never waits behind a
   queued backfill run. When the queue is full, it waits only for backfill runs
   already executing.
7. The plan states what each slot will load.
8. A backfill does not change how a DAG's live state reads.
9. The UI shows which slots are done, failed, and missing, and starts a
   backfill from that view.

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
- **The range selects fire times by default.** What a slot loads depends on the
  DAG's `dates`. With `target_day: { shift: -1d }` on a 02:00 schedule, the
  fires of October 1 through 31 load September 30 through October 30. The plan
  shows this, so an off-by-one is visible before the submit.
- **`--by` selects by data date.** With `--by target_day`, `--from 2026-10-01`
  and `--to 2026-10-31` take every slot whose `target_day` falls in October, in
  the entry's zone. Every `dates` operation moves forward with time, so these
  slots form one unbroken run of fire times and the selection is exact. On an
  hourly DAG with the same entry, it takes the 744 fires that load October.
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
  dequeued, cancelled while queued, or dropped by a pause or suspension. A slot
  the scheduler skipped under `skip_if_successful` counts as succeeded. One
  skipped because the DAG was busy counts as missing.

  `--slot` names slots directly, for repairs that are not contiguous. Named
  slots run whatever their history, unless they are active.
- **Each selected slot** gets one new queued run with that scheduled time and
  window. Earlier runs of the slot stay in history.
- **Pinned at submit.** The record holds the DAG definition and each slot's
  window, so an edit or a git sync during a backfill does not change later
  slots. The pin covers the YAML only. Scripts and files the steps call, and
  sub-DAG definitions, are read when each run starts, and the plan says so.
  Each run records the hash of the definition it ran, and the UI marks slots
  that ran an older one. `resume` switches a backfill to the current
  definition.
- **IDs.** A backfill ID is 8 characters, short enough to type and to keep run
  IDs within the 64-character limit. Run IDs are
  `backfill-{backfill id}-{generation}-{timestamp}`. The generation counts
  resumes, so a resumed slot gets a new run. After a crash, the scheduler finds
  the runs it already created.
- **Identity.** Each run records its backfill ID, and its
  `context.trigger.type` is `backfill`, a new value. The actor is the user who
  submitted the backfill.
- **Retries keep the identity.** A retry, by hand or by the DAG's retry policy,
  keeps the backfill ID and the trigger type `backfill`, where other retries
  become `retry`. Every rule in this RFC that treats backfill runs apart keys
  on the backfill ID. A retried backfill run therefore keeps its low priority,
  its exemption from the live checks, its place out of latest status and
  history, its quiet notifications, and its hold during a pause. It is never
  treated as a scheduler retry, which a pause or a suspension aborts.

### Slot index

The run store lists runs by DAG and start time, not by scheduled time, and
retention removes runs that selection still needs. A per-DAG slot index records
the runs of each slot, whatever started them: the scheduler, catchup, a manual
run on a chosen or default slot (RFC 007), or a backfill.

- **Written once** when a top-level run with a scheduled time is created. An
  entry holds the run ID, the trigger type, and the schedule entry that fired
  it. Status is read from the run itself, so the index never mirrors status
  changes. Before retention removes a run, its final status is written to the
  entry. A run an operator deletes drops its entry instead. A sub-DAG run takes
  its parent's slot and is not indexed. The scheduler also records a slot it
  skipped, with the reason.
- **Outlives run history.** Run retention does not touch the index. Entries
  last `slot_retention_days`, 730 by default, which sits beside
  `hist_retention_days`. A slot whose run was pruned keeps its status.
- **Earlier schedules.** A run fired live, by catchup, or by a backfill under an
  earlier recurring schedule covers a slot only when all of these hold:
  - The old and new entries fire equally often and differ only in time of day.
  - The run's fire time is nearer to that slot than to any other. Ties go to
    the later slot.
  - When the DAG has `dates`, the run's recorded first date equals the slot's.

  After a move from 02:00 to 01:00, the 02:00 run on October 5 covers the 01:00
  slot on October 5, which loads the same day. Runs of a different cadence
  cover nothing, and the plan says how many slots run because of that. Manual
  runs and one-off `at` fires count only by exact match.
- **No record.** When the range starts before the earliest indexed slot of a
  DAG that has one, the plan warns that older slots have no record and will
  run.
- **Catchup and backfill dispatch check it** under the scheduler lock, so
  neither starts a slot the other holds.

## Commands

```sh
dagu backfill start <DAG> (--from <time> [--to <time>] [--by <dates entry>] | --slot <time>...) [flags]
dagu backfill ls [<DAG>]
dagu backfill status <backfill id> [--watch]
dagu backfill resume <backfill id> [--max-concurrency <n>] [--on-failure <policy>] [--yes]
dagu backfill cancel <backfill id> [--stop-running]
```

| Flag | Meaning | Default |
| --- | --- | --- |
| `--from` | First slot, inclusive. A date, a wall-clock time, or an RFC 3339 time. | required without `--slot` |
| `--to` | Last slot, inclusive. | now |
| `--by` | A `dates` entry. `--from` and `--to` then name dates of that entry, not fire times. | none |
| `--slot` | One slot to run. Repeatable. Excludes the range flags and `--rerun`. | none |
| `--rerun` | `none`, `failed`, or `all`. | `none` |
| `--max-concurrency` | Runs of this backfill allowed at once. | `1` |
| `--on-failure` | `stop` or `continue`. | `stop` |
| `--params` | Params for every run. | the DAG's defaults |
| `--profile` | Runtime profile for every run. | the DAG's default |
| `--dry-run` | Print the plan and start nothing. | off |
| `--format` | Plan output: `text` or `json`. | `text` |
| `--yes` | Skip the confirmation. | off |

- A date without a time means the start of that day for `--from` and the end
  of that day for `--to`. Forms without an offset follow the zone rules of
  `--scheduled-at` (RFC 007).
- With `--by`, `--from` and `--to` are dates. A slot is selected when the
  calendar date of the entry's instant, in the entry's zone, falls between
  them, whatever the entry's format. A DAG without that entry is rejected.
- A `--to` in the future, including today's date, is clamped to now, and the
  plan prints the clamped end. Fires in the current minute or later are left to
  the live schedule. A `--from` in the future is rejected.
- A backfill over `scheduler.backfill_max_slots` slots, 10,000 by default, is
  rejected with the count and the bound. Slot progress is appended to the
  record, so a large backfill does not rewrite it on each run.
- A `--slot` that is not a fire time, or is later than now, is rejected with
  the nearest fire times, as RFC 007 rejects a future slot.
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
Live runs may wait up to 5m behind an executing backfill run

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
  `skip: active`. A skip that relies on a run from an earlier schedule names
  that run's date.
- **Concurrency** is the lower of `--max-concurrency` and the queue's limit.
- **The estimate** uses the median duration of the DAG's recent successful
  runs. It is omitted when there is no history.
- **Warnings** appear above the prompt:
  - No scheduler is running.
  - The queue caps the concurrency, or the backfill would fill the queue.
  - How long live runs may wait for an executing backfill run, and any start
    deadline (RFC 009) that wait would miss. When a backfill run outlasts the
    cadence, how many live slots `overlap_policy: skip` drops.
  - The DAG is suspended, and its backfill runs will still run.
  - The `handler_on` steps that will run once per slot, by name.
  - Slots with no record, and slots that run because the cadence changed.

### API and MCP

| Endpoint | Purpose |
| --- | --- |
| `POST /dags/{fileName}/backfills` | Submit, with the CLI's options including `by`. `dry_run` returns the plan. |
| `GET /backfills` | List, with an optional DAG filter. |
| `GET /backfills/{id}` | Status. |
| `POST /backfills/{id}/resume` | Resume, with new options. `dry_run` returns the plan. |
| `POST /backfills/{id}/cancel` | Cancel, with an optional `stop_running`. |
| `GET /dags/{fileName}/slots` | Slots in a range with their latest status, window, and dates. |
| `GET /backfills/{id}/slots` | A backfill's slots, paged and filtered by status. |

A plan response carries the slot count for each `--rerun` value and a
`revision`. A submit may send the revision. If the plan has changed since, the
submit is refused and returns the new plan.

The MCP execute tool gains `backfill`, `backfill_resume`, and
`backfill_cancel`, with the CLI's options, where `dry_run` returns the plan.
The read tool gains `backfills` and `slots` targets, so an agent can find
gaps, watch a backfill, and stop what it starts.

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
- **Live runs come first.** The checks that skip a live slot because the DAG is
  running or queued ignore backfill runs, and so does `skip_if_successful`. The
  live run is enqueued instead. Backfill runs are low priority in the queue and
  all other runs are high priority, so they keep their order among themselves
  and start before queued backfill runs. Priority applies only while a run is
  queued: an executing backfill run is not preempted, and the queue limit still
  holds. On the default limit of 1, a live run waits for at most the one
  backfill run executing.
- **Waiting reason.** `status`, the backfill page, and the queue page say why a
  backfill run waits, such as `queue at limit, 2 live runs ahead`.
- **A DAG's latest run** excludes backfill runs wherever the product answers
  that question: the DAG list, DAG details, `dagu status <DAG>`, MCP, and the
  scheduler's checks. A failed run of an old slot does not turn a healthy DAG
  red. A DAG whose only runs are backfill runs shows the latest of them.
  `dagu status <DAG>` and the DAG API also return the open or last unresolved
  backfill as `latestBackfill`, so the activity stays visible.
- **`skip_if_successful` and `overlap_policy` do not apply** to backfill
  runs. The backfill's own selection and `--max-concurrency` replace them.
- **Failure.** `--on-failure stop` enqueues nothing more after a slot fails.
  Active runs finish and the remaining slots are marked `not run`. `continue`
  goes on to the next slot and sends one notice at the first failure. The
  default is `stop` at any concurrency: Dagu cannot tell an incremental load,
  where a gap corrupts later slots, from independent slots. The DAG's own retry
  policy applies to each run first.
- **Waiting.** A run waiting for a person counts against concurrency and holds
  the slot order. `status` and the UI show `blocked: waiting approval`.
- **Cancel** stops enqueueing and removes the backfill's queued runs. Their
  slots count as missing again. Runs already executing finish, unless
  `--stop-running` stops them too.
- **Resume** re-plans the slots of a backfill that have not succeeded, with the
  current definition and any new `--max-concurrency` or `--on-failure`. On an
  open backfill it first withdraws the queued runs, as cancel does, and lets
  active runs finish. It keeps the record, shows the plan, and runs the slots
  in order. A fix or a new concurrency therefore needs one command.
- **Pause.** A paused scheduler enqueues no backfill runs and holds those
  already queued. They are neither started nor aborted, do not block other runs
  in the queue, and start when the scheduler resumes. Nothing is discarded.
- **A suspended DAG** can be backfilled. Suspension stops its schedule, not an
  operator's request. The dispatcher must tell a pause from a suspension, which
  the existing check reports alike. Retries of backfill runs follow the same
  rules.
- **A DAG renamed or deleted** during a backfill ends the backfill with the
  remaining slots marked `cancelled`.
- **Notifications.** `mail_on` and notification routes do not fire per backfill
  run, except for `wait`, where a person must act. The backfill sends one
  summary through the same channels when it ends, with the totals and the
  failed slots grouped by error. `handler_on` steps still run, because Dagu
  cannot tell a cleanup handler from an alert, and a skipped cleanup corrupts
  state. The plan names the handlers that will run once per slot. The docs show
  the opt-out for an alert handler: a precondition on
  `${context.trigger.type}` with `expected: backfill` and `negate: true`.

A backfill ends as `succeeded` when every selected slot succeeded or was
skipped by the recheck, `failed` when any did not succeed, or `cancelled`. A
finished backfill's record is kept for `slot_retention_days`.

## UI

A backfill is a decision about slots, so the UI shows slots. A form with two
dates hides the holes a backfill exists to fill.

### Slots view

Each DAG with a recurring schedule gains a **Slots** view: history by slot
rather than by run.

- **Cells.** One cell per slot, showing its latest status: succeeded, failed,
  active, pending in a backfill, skipped, or missing. Status is never shown by
  color alone.
- **Layout follows the cadence.** Daily or slower slots form a calendar. Hourly
  slots form one row per day, and finer cadences one row per hour.
- **Labels.** When the DAG has `dates`, a cell is labeled with its first entry,
  so choosing October means choosing the cells labeled October. Hover shows the
  fire time, window, every date, the trigger, and a link to the run.
- **Selection.** Drag for a range, or click cells one at a time. On a phone,
  tap the first and the last. **Backfill** appears with the selection.

### Starting a backfill

The Slots view, the **Backfill** item in the Run button's menu, and the
missed-slots notice open one panel:

1. **Slots.** The selection, with presets for the last 7 and 30 days, since
   the last success, and the missed slots. Date fields for exact ranges name
   what they filter: a `dates` entry such as `target_day`, or fire time.
2. **What to run,** in plain words with live counts, each beside its `--rerun`
   value: **Fill gaps** (`none`, 3), **Fill gaps and redo failed** (`failed`,
   5), **Redo everything** (`all`, 31). Cells picked one at a time run as named
   slots, and this section is hidden.
3. **Options,** collapsed: concurrency with the queue's limit beside it, params
   through the DAG's param form, profile, and failure behavior in plain words:
   stop for loads that build on the previous slot, continue for independent
   slots.
4. **Plan.** `Loads 2026-10-01 to 2026-10-31`, the totals, the estimate, and
   the warnings.
5. **A button that names the effect:** `Start 28 runs`.

Redoing succeeded slots needs a checkbox that names how many will run again.
The panel sends the plan's revision, so a stale plan shows the new one instead
of starting.

### Missed slots

When a pause, a suspension, or an outage longer than `catchup_window` ends, the
DAG shows **12 slots passed while this DAG was suspended: Backfill**, which
opens the panel with that gap selected. Slots skipped because the DAG was busy
raise no notice. Dismissing the notice hides that gap until another is missed.

### Watching a backfill

Each backfill has its own page and URL. A DAG lists its backfills, and the runs
and queue pages filter by `backfill`.

- **One sentence gives the state and the next action:**
  - `Running: 12 of 28 done, 1 failed, about 1h 40m left`
  - `Blocked: Oct 5 is waiting for approval`, linking to the approval
  - `Stopped at Oct 9: 14 slots not run`, with **Resume**
  - `Waiting for a scheduler`
  - `Waiting: queue at limit, 2 live runs ahead`
- **Slot grid.** The same cells as the Slots view, updated live and paged, so
  10,000 slots load one screen at a time. A slot whose run was pruned shows its
  status without a link.
- **Failures grouped by error,** with the slot count, the failing step, and a
  link to a log. Twenty-seven slots with one cause read as one problem.
- **Controls.** **Cancel** asks whether to stop active runs. **Resume** shows
  the plan, takes new concurrency and failure behavior, and says when the
  definition has changed since the backfill started. Slots that ran an older
  definition are marked.

### Keeping the live view clean

- **DAG list.** The status follows the latest-run rule in Execution. A chip,
  `Backfill 12/28, 1 failed`, links to the open backfill. After a backfill ends
  `failed`, the chip stays red until its failed slots succeed or someone
  dismisses it.
- **Run timeline and DAG history** hide backfill runs by default, on the
  server, with a toggle to show them. The timeline holds 30 runs at most, so a
  long backfill would otherwise push out every live run. A DAG whose only runs
  are backfill runs shows them, as the latest-run rule does.
- **Run lists and the queue page** show a `backfill` badge and a filter.
- **Run details** show `Slot Oct 2 02:00, loads Oct 1`, a link to the backfill,
  and `slot 1 of 28`.

## Security

- Starting or resuming a backfill needs the permission to run the DAG.
  Cancelling it needs the same.
- The audit log records submit, resume, and cancel with the range and the slot
  count.
- The slot bound caps the size of one backfill record. Dispatch keeps at most
  `--max-concurrency` runs of a backfill in the queue, so one request cannot
  flood it.
- The Slots view and the backfill page need read access only. Their actions
  appear only for users who hold the permissions above.

## Compatibility and rollout

| Release | Ships |
| --- | --- |
| First | CLI, API, backfill record, slot index, scheduler dispatch, and the plan. Requires the first release of RFC 007. |
| Second | The UI and the MCP actions. |

- `backfill` is a new value of `context.trigger.type` in spec 017. Unlike
  other trigger types, it survives a retry.
- `--by` and the plan's dates need `dates`, from the second release of RFC 007.
  Until then the plan shows windows.
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
11. A failed slot stops the backfill by default at any concurrency. After the
    DAG is fixed, `resume` runs the remaining slots with the new definition.
12. An edit to the DAG during a backfill does not change later slots.
13. Runs queued when the scheduler is paused are neither started nor aborted,
    and start when it resumes.
14. A suspended DAG's backfill runs complete.
15. A slot whose successful run has passed `hist_retention_days` is listed as
    `skip: succeeded`.
16. After a schedule moves from 02:00 to 03:00, or from 02:00 to 01:00, each
    earlier day is covered by its own run, and `--rerun failed` reruns the day
    whose run failed.
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
25. A failed backfill run of an old slot does not change the DAG's status in
    the DAG list.
26. After a 40-slot backfill, the run timeline and DAG history still show the
    live runs.
27. The Slots view shows the succeeded, failed, and missing slots of a range,
    and labels cells with the first `dates` entry.
28. Selecting three non-adjacent cells starts exactly those three slots.
29. A `--slot` that is not a fire time is rejected with the nearest fire times.
30. A submit with a stale plan revision starts nothing and returns the new
    plan.
31. Lifting a suspension shows the slots that passed, and the panel opens with
    them selected.
32. A slot skipped under `skip_if_successful` is listed as `skip: succeeded`.
33. The page of a 10,000-slot backfill renders without loading every slot.
34. `--by target_day --from 2026-10-01 --to 2026-10-31` on an hourly DAG with
    `target_day: { shift: -1d }` selects the 744 fires that load October.
35. With a queue limit of 1, a live slot that fires while a backfill run
    executes runs next, and the plan states the wait.
36. `resume --max-concurrency 4` on an open backfill withdraws its queued runs
    and continues with the new limit and the current definition.
37. After a backfill ends `failed`, the DAG list shows a red chip until its
    failed slots succeed, and `dagu status` returns it as `latestBackfill`.
38. The plan names the `handler_on` steps that will run once per slot.
39. A backfill run waiting in a full queue shows the reason in `status`.
40. A backfill run retried by the DAG's retry policy keeps its backfill ID and
    trigger type, stays behind live runs in the queue, and sends no per-run
    mail.
41. A retry of a backfill run queued during a scheduler pause is held, not
    aborted, and on a suspended DAG it runs.
42. `--by` on an entry formatted as `unix` selects the same slots as on a
    `date` entry with the same shift.
43. A DAG whose only runs are backfill runs shows them in its run timeline.

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
- **Make `--by` the default when a DAG has `dates`.** A range would change
  meaning when a DAG gains `dates`, as ranges did when Airflow 3 changed its
  default timetable.
- **Let live runs bypass the queue limit,** as Airflow does. Overlap safety
  would move to DAG authors, while Dagu's defaults assume one active run per
  DAG.
- **Continue on failure by default.** Schedulers that do so let authors declare
  dependencies between periods. Dagu has none, so a gap in an incremental load
  would go unnoticed.
- **Edit a backfill in flight.** `resume` re-plans with new options, so a
  separate `set` verb would be a second way to do one thing.
- **Mirror run status in the slot index.** Two copies of status drift, as
  Dagster's partitions stuck "in progress" show.
- **Select from the run store alone.** Retention removes the runs that prove a
  slot is done, so old slots would rerun.
- **`dagu backfill <DAG>` without a verb.** Subcommands would shadow DAGs named
  `ls`, `status`, or `cancel`, and each new subcommand could break another DAG.
- **CLI and API only.** Operators with a UI login and no shell could not start
  or stop a backfill, and gaps would stay invisible until someone looked.
- **Two date fields and a table of slots.** The form hides the holes it is
  meant to fill and invites the off-by-one that labeled cells prevent.
- **A client-side filter for backfill runs.** The timeline holds 30 runs at
  most, so a long backfill would leave the filter nothing to show.

## Consequences

- Loading history and repairing a bad range become one operation with a
  record.
- A slot can have several runs. Views that show a slot must show its latest
  run.
- The scheduler gains a second dispatch source beside schedules and catchup.
- The slot index is new durable state, written once per scheduled run and once
  more when retention prunes the run.
- A DAG's latest run no longer means its newest run. It means its newest run
  that is not a backfill run.
- A live run and a backfill run of one DAG can be active together on a queue
  whose limit is above 1. A DAG that assumes one active run must keep the
  limit at 1.
