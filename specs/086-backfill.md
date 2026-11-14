# Spec: Backfill

## Status

Partially implemented. Slot history, `slot_retention_days`, planning,
submitting with `dagu backfill start`, the scheduler running a backfill, and
`dagu backfill ls`, `status`, `cancel`, and `resume` are implemented, and
conformance covers them. The summary a backfill sends when it ends is not
implemented. Further sections are added as the behavior they describe lands.

## Scope

This spec defines how Dagu knows what happened to each schedule slot of a
DAG, and how a backfill chooses and records the slots that still need a run.
It does not define what a slot or its window is (spec 017), how history
retention picks the runs it removes, or how a run executes.

## Goal

An operator can backfill a range of past slots and have only the slots that
need a run start, even after history retention has removed the runs that show
a slot is done.

## Behavior

### Slot history

- Each DAG keeps a slot history. For each slot it holds the runs that slot had
  and, when the scheduler did not run the slot, why.
- A run belongs to a slot when it is a top-level run with a scheduled time,
  whatever started it: the scheduler, catchup, or a manual run on a chosen
  slot. Its slot is its `context.run.scheduled_at`.
- These runs do not belong to a slot of their own:
  - A sub-DAG run, including one that `dag.enqueue` starts. It takes its
    parent's slot.
  - An ephemeral run.
  - A retry. It is the same run of the same slot.
- For each run, the history holds its run ID, its trigger type, and the
  recurring schedule that fires at the slot. That schedule is read from the
  definition the run was created with, so it stays known after the schedule
  changes. A run on a time that no recurring schedule fires at has none.
- While a run exists, its status is read from the run. When history retention
  removes a finished run, the slot keeps that run and its final status.
- When an operator removes a run, the run no longer counts for its slot. This
  covers `dagu rm --history`, `dagu cleanup`, and deleting a run from the API
  or the UI. A removal refused because the run is active changes nothing.
- A run whose removal the slot history cannot record is not removed. The
  removal fails with an error, and other runs in the same retention pass are
  still removed.
- The scheduler records a slot that comes due and that it does not run, with
  one of these reasons:

  | Reason | When |
  | --- | --- |
  | `succeeded` | `skip_if_successful` held the slot back. |
  | `running` | A run of the DAG was running. |
  | `queued` | A run of the DAG was queued. |
  | `overlap` | The catchup overlap policy `skip` dropped the slot. |

  A slot whose checks failed is not recorded as skipped. A slot that already
  has a run is recorded through that run.
- Each read of a DAG's slot history first adds the runs in its run history
  that it lacks, except runs whose slots are older than `slot_retention_days`:
  the whole history on the first read, and later the runs created since the
  previous read. A run that could not be recorded when it was created is
  added this way. A run removed while its history is being added stays
  removed.
- A slot stays in the history for at least `slot_retention_days` days after
  its scheduled time, whatever `hist_retention_days` or `hist_retention_runs`
  remove.

### Slot state

A backfill reads each slot's state from its history:

- A run counts for its slot while it exists, with its own status. A removed
  run counts with the final status the slot kept, unless it was dropped.
- A run that never started counts for nothing: one cancelled or dequeued
  while it was queued. A run whose retry was cancelled that way did start.
- A slot is active while any run that counts for it is queued, running, or
  waiting. Otherwise it takes the status of its latest run that counts.
  Partially succeeded counts as succeeded.
- A slot with no run that counts is succeeded when the scheduler skipped it
  as `succeeded`, and missing otherwise.
- A run that the scheduler, catchup, or a backfill started under an earlier
  schedule covers the current slot nearest to it, ties going to the later
  slot, when the two schedules fire on the same days, once a day or once an
  hour, and differ only in when. After a move from 02:00 to 01:00, the 02:00
  run of October 5 covers the 01:00 slot of October 5. A run of another
  cadence covers nothing, even at a current fire time: an hourly run at
  02:00 does not cover a daily 02:00 slot. Days are compared by the days a
  schedule selects, so `MON-FRI` and `1-5` fire on the same days. A manual
  run counts only for its own slot.

### Starting a backfill

```sh
dagu backfill start <DAG> (--from <time> [--to <time>] [--by <dates entry>] | --slot <time>...) [flags]
```

| Flag | Meaning | Default |
| --- | --- | --- |
| `--from` | First slot, inclusive. A date, a time without an offset, or an RFC 3339 time. | required without `--slot` |
| `--to` | Last slot, inclusive. | now |
| `--by` | An entry of the DAG's `dates` ([Spec 097](097-derived-dates.md)). `--from` and `--to` are then dates of that entry, not fire times. | none |
| `--slot` | A slot to run. Repeatable. Excludes `--from`, `--to`, `--by`, and `--rerun`. | none |
| `--rerun` | `none`, `failed`, or `all`. | `none` |
| `--max-concurrency` | Runs of the backfill allowed at once. | `1` |
| `--on-failure` | `stop` or `continue`. | `stop` |
| `--params` | Params for every run. | the slot's schedule overrides and the DAG's defaults |
| `--profile` | Runtime profile for every run. | the DAG's default |
| `--dry-run` | Print the plan and start nothing. | off |
| `--format` | Plan output: `text` or `json`. | `text` |
| `--yes` | Start without asking. | off |

- The slots are the fire times of the DAG's recurring start schedules for the
  profile, with business calendars applied, in `[--from, --to]`.
- A date means the start of that day for `--from` and its end for `--to`. A
  date or a time without an offset is read in the zone of the schedules, as
  `--scheduled-at` is.
- A `--to` at or after the current minute is clamped to just before it, and
  the plan says so. A `--from` later than now is rejected.
- A `--slot` that is not a fire time, or that is later than now, is rejected.
- With `--by`, `--from` and `--to` are dates (`YYYY-MM-DD`). A slot is in the
  range when the calendar date of the entry's instant, in the entry's zone,
  falls between them, whatever the entry's format. For a list entry, when any
  of its values does. The entry is resolved as the slot's run resolves it. A
  `--to` of today or later runs to now and is clamped, and a `--from` later
  than today is rejected.
- Each slot's action follows `--rerun`:

  | Slot state | `none` | `failed` | `all` | `--slot` |
  | --- | --- | --- | --- | --- |
  | missing | `run` | `run` | `run` | `run` |
  | succeeded | `skip: succeeded` | `skip: succeeded` | `run` | `run` |
  | failed | `skip: failed` | `run` | `run` | `run` |
  | active | `skip: active` | `skip: active` | `skip: active` | `skip: active` |

  A slot that an open backfill of the DAG has yet to run is active.
- The plan lists every slot with its window start and action, and the number
  of slots each `--rerun` value would run. The text plan shows the first and
  last three slots; `--format json` shows every slot.
- For a DAG with `dates`, the plan gives each slot the dates its run gets.
  The text plan adds a column and a `Loads <first> to <last> (<entry>)` line
  for the `--by` entry, else the DAG's first, over the slots it runs.
- Without `--yes`, the command prints the plan and asks before starting. With
  no terminal to ask on, or with `--format json`, it fails and names `--yes`.
- A submit records the backfill, with the DAG's definition and each slot's
  window as they are at submit, and prints its 8-character ID.
- A submit with the same range and options as an open backfill of the DAG
  starts nothing and names that backfill. A submit that selects no slot
  creates no backfill.
- A dry run starts nothing and prints the plan a submit would then record.
- Planning and recording hold a lock per DAG, so two submits never select the
  same slot.
- The plan states the concurrency, which is the lower of `--max-concurrency`
  and the limit of the DAG's queue. It also states the queue's limit. When
  the DAG has recent successful runs, the plan gives an estimate: the median
  duration of up to 20 of those runs, times the number of rounds the runs
  take at that concurrency.
- The plan warns when no scheduler is running, when the DAG is suspended,
  when the queue's limit lowers the concurrency, when the backfill can fill
  the queue so live runs may wait behind an executing backfill run (with the
  median duration when known), which `handler_on` steps will run once per
  slot, how many slots run because the schedule's cadence changed since
  their runs, and which `--params` name a param whose `eval` reads the run's
  dates, since the value given holds for every slot.

### Running a backfill

- The scheduler that holds the scheduler lock runs open backfills. It queues
  the next slots of a backfill, oldest first, while fewer than
  `--max-concurrency` of its runs are queued, running, or waiting. With a
  concurrency of 1, a slot is queued only after the slot before it finished.
- A backfill waits while no scheduler runs.
- A paused scheduler queues no new run of a backfill and holds its queued
  runs: they are neither started nor aborted, and start when the scheduler
  resumes. A suspended DAG's backfill runs still run.
- These rules follow a backfill run's retries, by hand or by the DAG's retry
  policy. A retry of a run of a backfill that ended `succeeded` or `failed`
  opens the backfill again until the retry ends; the slots a stopped
  backfill did not reach stay `not run`. A cancelled backfill stays
  cancelled.
- A backfill whose DAG is renamed or deleted ends `cancelled`, with the slots
  it had not queued cancelled. A rename of the file, or of the DAG inside
  it, counts as a rename.
- A backfill run that history retention removed counts for the backfill with
  the final status its slot kept.

### Live runs come first

- A backfill run is queued behind every other run of its queue. Other runs
  keep their order among themselves. An executing backfill run is not
  stopped for a live run, and the queue's limit still holds.
- A queued or running backfill run does not make the scheduler skip a live
  slot because the DAG is queued or running, and does not count for
  `skip_if_successful`. The live slot's run is queued instead.
- A DAG's run history, in the API and its live feed, leaves out backfill
  runs unless asked for them with `includeBackfill`, so a long backfill does
  not push the live runs out of a page. A DAG whose only runs are backfill
  runs lists them.
- `mail_on` and notification routes do not fire for a backfill run, except
  for a run waiting for a person. A backfill run neither opens nor resolves
  an incident. `handler_on` steps still run.
- When a backfill ends succeeded or failed, the scheduler sends one summary
  to the notification targets a run of its DAG with that outcome notifies:
  its DAG's notification settings, else its workspace's routes. The summary
  gives the slot counts, the time from submission to the end, up to 10
  groups of failed slots by step and error with a link to each group's
  first run, and a link to the backfill. Slack, Teams, Telegram, email, and
  webhook targets receive it; a webhook gets `{"type": "backfill.ended",
  "backfill": …, "message": …}`. Command targets and a cancelled backfill
  send none. The DAG's own `mail_on` mail sends no summary.
- Catchup skips a slot whose latest backfill run is active or succeeded.
  A manual run does not keep catchup off a slot.
- The scheduler keeps, per DAG, the latest gap whose start-schedule slots
  passed without a run, with its span, its reason, and its slot count up to
  10,000:
  - `suspended` or `paused`: from the first tick that found the DAG
    suspended or the scheduler paused to the tick that found it running
    again, whose own slots run;
  - `down`: the slots no scheduler ran, for a DAG without catch-up, or
    before its catch-up window starts.
  A newer gap replaces it. A gap without a slot is not kept.
- A DAG's latest run is its newest run that is not a backfill run, wherever
  the latest run is shown or checked: the DAG list, the DAG's details,
  `dagu status`, and the scheduler. A DAG whose only runs are backfill runs
  shows the newest of them.
- A DAG's latest backfill is its newest open backfill. Without one, it is
  the DAG's newest backfill that ended `failed`, while a slot that backfill
  failed has not succeeded since. `GET /api/v1/dags/{fileName}` returns it
  as `latestBackfill`. `dagu status <DAG>` without `--run-id` prints it
  after the latest run, except in a remote context.
- Each run is queued through the DAG's queue and counts against its limit.
  It runs the definition pinned at submit, with the backfill's profile and
  its explicit params or the first matching recurring schedule's overrides.
  It has its slot's scheduled time and window. Its
  `context.trigger.type` is `backfill`.
- Every run records a digest of the YAML it runs as `definitionDigest`.
- Each run evaluates its params' `eval` when it starts, from its own slot's
  dates, so a param with `eval: ${dates.target_day}` takes each slot's date.
  A value given with `--params` replaces that `eval`, so the param holds that
  value in every slot.
- A backfill run's ID is `backfill-<backfill id>-<generation>-<slot>`, with
  the slot in UTC as `20060102T150405Z`. A scheduler that restarts finds the
  runs it queued before and queues none of them again.
- When a slot's turn comes, it is skipped instead of run when another run of
  it is active, or when it gained a run since the backfill was submitted that
  `--rerun` would skip.
- A slot whose run did not succeed stops a backfill with `--on-failure stop`:
  no further slot is queued, active runs finish, and the slots not reached
  are not run. With `--on-failure continue`, the backfill goes on.
- The DAG's retry policy applies to a backfill run first: a failed run with
  retries left, within `scheduler.retry_failure_window`, holds its slot as
  active until the retries succeed or run out.
- A backfill ends `succeeded` when every slot's run succeeded or the slot was
  skipped, and `failed` otherwise.
- A backfill that ended more than `slot_retention_days` ago is removed when
  its DAG next gets a backfill.

### Listing and following backfills

```sh
dagu backfill ls [<DAG>]
dagu backfill status <backfill id> [--watch] [--format text|json]
```

- `ls` lists the backfills of every DAG, or of one, oldest first, with their
  status and number of slots.
- `status` shows a backfill's status and each slot's state: `pending`,
  `queued`, `running`, `waiting`, `succeeded`, `failed`, `skipped`,
  `not run`, or `cancelled`, with the slot's run ID once queued.
- An open backfill's status is `running`, or `waiting for scheduler` when no
  scheduler runs. An ended backfill's status is `succeeded`, `failed`, or
  `cancelled`.
- `status` also says why an open backfill makes no progress. A run waiting
  for a person gives `Blocked: the run of slot <slot> is waiting for a
  person`. Otherwise, the backfill's oldest queued run gives `Waiting: queue
  at limit, <n> runs ahead` when the queue runs its limit, `Waiting: <n> runs
  ahead in the queue`, or `Waiting: next in the queue`.
- `status` gives about how long an open backfill's slots left take, from the
  pace of the runs that ended since its slots were last planned:
  `About 1h 40m left, at the pace of its runs so far`. The API gives it as
  `secondsLeft`, and the run that blocks a backfill as `waitingRunId`.
- `status` groups the failed slots by the step their runs failed at and the
  first line of its error, the largest group first, each with its slot
  count, its earliest slot, and that slot's run:
  `Failed: 2 slots at step load: connection refused (first <slot>, run <id>)`.
  The dispatcher records the step and error when it sees a run fail, so
  reading them needs no run's status. The API gives them as `failures`.
- `status` marks a slot whose run ran another definition than the one the
  backfill runs now, as a run a resume of an edited DAG carries:
  `running (older definition)`. The API gives it as `otherDefinition`.
- `status --watch` prints a line each time the backfill's progress changes,
  then the status when the backfill ends. It exits 0 only when the backfill
  succeeded. With `--format json`, the progress lines go to stderr and stdout
  holds only the final status.

### Cancelling and resuming

```sh
dagu backfill cancel <backfill id> [--stop-running]
dagu backfill resume <backfill id> [--max-concurrency <n>] [--on-failure <policy>] [--dry-run] [--format text|json] [--yes]
```

- `cancel` ends an open backfill `cancelled`. Its queued runs are withdrawn,
  so their slots count as missing again, and the slots it had not queued are
  `cancelled`. Running and waiting runs finish, unless `--stop-running`
  stops them too. Cancelling an ended backfill fails.
- `resume` plans again the slots of a backfill that have not succeeded or
  been skipped, with the DAG's current definition, and runs them in order
  with new runs. A slot that gained a successful run since is skipped. On an
  open backfill it first withdraws the queued runs. Its running and waiting
  runs finish as part of the resumed backfill: they count against its
  concurrency, and a failure among them fails it. `--max-concurrency` and
  `--on-failure` replace the backfill's.
- `resume` shows its plan and asks before resuming, as `start` does. A dry
  run prints the plan the resume then runs. The backfill keeps its ID. The
  plan warns when the DAG's definition changed since the backfill started:
  new runs run the current one, and active runs keep theirs.

### API

| Endpoint | Purpose |
| --- | --- |
| `POST /api/v1/dags/{fileName}/backfills` | Plan and submit, with the options of `dagu backfill start`. `dryRun` returns the plan only. |
| `GET /api/v1/dags/{fileName}/slots` | The slots of a range, each with its window start and state: `missing`, `succeeded`, `failed`, or `active`; the trigger of the run its state comes from; and, for a DAG with `dates`, the dates its run gets. |
| `GET /api/v1/backfills` | Backfills, oldest first, of every DAG or of `dag`. |
| `GET /api/v1/backfills/{id}` | A backfill's status, the number of slots in each state, and `waiting`, the reason `status` gives. |
| `GET /api/v1/backfills/{id}/slots` | A backfill's slots, `limit` at a time from `offset`, filtered by `state`. |
| `POST /api/v1/backfills/{id}/cancel` | Cancel, with `stopRunning`. |
| `POST /api/v1/backfills/{id}/resume` | Resume, with `maxConcurrency` and `onFailure`. `dryRun` returns the plan only. |
| `GET /api/v1/dag-runs?backfillId={id}` | The runs of a backfill, from its submission on unless `fromDate` or `toDate` is set. Every run carries `backfillId`, which a retry keeps. |
| `GET /api/v1/dag-runs?backfill=exclude` or `only` | The run list without backfill runs, or with only them. |

- A plan carries the number of slots each `rerun` value would run and a
  `revision`. A submit or resume that sends a `revision` the plan no longer
  has starts nothing and answers 409 with the new plan.
- Submitting, cancelling, and resuming need permission to run DAGs and are
  recorded in the audit log.
- A request with invalid times or options answers 400, an unknown DAG or
  backfill 404, and cancelling an ended backfill 409.
- The event stream's `backfill` topic, identified by the backfill ID, sends
  what `GET /api/v1/backfills/{id}` returns. A change to one of the
  backfill's runs updates it within seconds, at most once every 2s; other
  changes reach it within 30s.
- A backfill run's details give `backfillSlot`: the place of its slot among
  the backfill's slots, from 1, and how many slots the backfill has.

## Configuration

| Key | Default | Meaning |
| --- | --- | --- |
| `slot_retention_days` | `730` | Days a slot stays in the slot history, and an ended backfill stays listed. A positive integer. |
| `scheduler.backfill_max_slots` | `10000` | Most slots one backfill may cover. |

## Errors

- `slot_retention_days` that is zero or negative: validation fails with an
  error naming `slot_retention_days`.
- `dagu backfill start` exits with status 1 and starts nothing when:
  - neither `--from` nor `--slot` is given, or `--slot` is combined with
    `--from`, `--to`, `--by`, or `--rerun`;
  - `--by` names no entry of the DAG's `dates`, or `--from` or `--to` is not
    a date with `--by`, or `--to` is before `--from`;
  - `--from` or a `--slot` is later than now, or a `--slot` is not a fire time;
  - the range has more slots than `scheduler.backfill_max_slots`;
  - the DAG has no schedule;
  - queues are disabled, except for a dry run; the error names
    `queues.enabled`;
  - it would start runs without `--yes` and has no terminal to ask on.

## Examples

```yaml
schedule: "0 2 * * *"
hist_retention_days: 30
slot_retention_days: 730
steps:
  - run: ./load.sh
```

The run for 2026-10-01 02:00 succeeds. History retention removes it in
November. Its slot still counts as succeeded until at least 2028-09-30.

## Conformance

`conformance/spec086_backfill` asserts that `dagu validate` accepts a positive
`slot_retention_days` and rejects zero. Through `dagu backfill start`, it
asserts that the plan skips slots whose latest run succeeded or failed and
counts the slots of each `--rerun` value; that a slot keeps the status of a
run history retention removed; that a run removed with `dagu rm --history` no
longer counts; that a scheduler run under an earlier schedule covers the
nearest slot after the schedule moves; that `--to` is clamped to now; that
`--by` selects the slots whose entry falls in the range, whatever its format,
including a list entry, and that the plan shows each slot's dates; that the
rejections under Errors fail with status 1; and that submitting the same
backfill while it is open starts nothing while an overlapping one leaves the
open backfill's slots active. With a scheduler running, it asserts that a
backfill's slots each end with a succeeded run of the backfill, that the
backfill ends `succeeded`, and that `ls` lists it; that a param whose
`eval` reads a date takes each slot's own date, and that the plan warns when
`--params` gives it one value. It asserts that `cancel`
cancels an open backfill's slots and fails on an ended one, and that
`resume` makes the slots pending again.
