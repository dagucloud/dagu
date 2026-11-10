# RFC 009: Run Deadlines

- **Status:** Proposed
- **Date:** 2026-11-10
- **Affected areas:** `spec`, `ir`, `service/scheduler`, `eventstore`,
  notification routes, incident policies, `service/frontend` (API, UI),
  `specs`, and `docs`

## Summary

Let a DAG say when a run should have finished, and tell someone when it has
not:

```yaml
schedule: "CRON_TZ=Asia/Tokyo 0 1 * * *"
deadline:
  at: "05:00"
```

If the 01:00 run has not finished by 05:00 Tokyo time, Dagu records a
`dag.run.deadline_missed` event. Notification routes and incident policies
handle it like a failure. The run keeps going unless the deadline says to
fail it.

The scheduler checks deadlines, not the run. A run that is still queued, or
whose process hangs, misses its deadline like any other.

## Problem statement

A batch job has a time by which its output is needed: before the market
opens, before the morning report. Dagu can only stop a run that takes too
long:

- **`timeout_sec` kills the run.** A load that is late and still running is
  usually better finished than killed.
- **It counts from the start of the run.** Time spent waiting in a queue, or
  waiting for a worker, is not counted. The run can start four hours late
  and finish inside its timeout.
- **It is a duration.** "By 05:00" cannot be written.
- **A run waiting for approval** has no process and no timeout. It can wait
  past the time anyone needed the result.

So operators learn a job is late when a downstream consumer complains.
Schedulers aimed at batch operations have this: Digdag's `sla:` takes a time
of day or a duration, and can alert or fail.

## Goals

1. Declare a deadline as a time of day or as a duration.
2. Alert through the channels that already carry failures.
3. Catch runs that are queued, waiting, or hung, not only runs that are
   slow.
4. Fire once per run and deadline, across scheduler restarts.
5. Leave the run alone by default.

## Non-goals

- Step deadlines. A step has `timeout_sec`.
- Runs that were never created. See open question 1.
- Predicting lateness from past durations.
- Service-level reporting: percentages met per month.

## Declaring a deadline

`deadline` takes one entry or a list:

```yaml
deadline:
  - at: "05:00"
  - within: 90m
    fail: true
```

| Field | Meaning | Default |
| --- | --- | --- |
| `at` | A wall-clock time, `HH:MM`. | |
| `within` | A duration, in the syntax of `catchup_window`. | |
| `fail` | Stop the run and mark it `failed` when the deadline passes. | `false` |
| `notify` | Send the event to notification routes and incident policies. | `true` |
| `name` | Shown in the alert. Unique within the DAG. | `at 05:00`, `within 90m` |

Each entry has exactly one of `at` and `within`.

- **The anchor** is the run's `scheduled_at` when it has one, else the time
  the run was created. Queue time counts.
- **`within`** is the anchor plus the duration.
- **`at`** is the first occurrence of that wall-clock time at or after the
  anchor, in `context.run.timezone` (RFC 007), else the server zone. A 23:00
  run with `at: "05:00"` has its deadline the next morning.
- **Retries do not move the deadline.** It is anchored to the run, not the
  attempt.
- **A manual run with `--scheduled-at`** in the past, and every backfill
  run, has no deadline. Its anchor is already history.
- **Validation.** `dagu validate` rejects an entry with both or neither of
  `at` and `within`, a malformed time, and a non-positive duration.

## Evaluation

```text
scheduler tick ─▶ runs with an open deadline ─▶ deadline passed and run not finished?
                                                   │ yes
                                                   ▼
                              mark fired ─▶ dag.run.deadline_missed ─▶ routes, incidents
                                                   │ fail: true
                                                   ▼
                                         stop run, status failed
```

- **Where.** The scheduler that holds the scheduler lock evaluates deadlines
  on each tick, so resolution is one minute.
- **Finished** means `succeeded`, `partially_succeeded`, `failed`,
  `aborted`, or `rejected`. `queued`, `running`, and `waiting` are not
  finished.
- **Once.** The run record stores each fired deadline. A restart does not
  fire it again.
- **Late evaluation.** If the scheduler was down when the deadline passed,
  it fires on the first tick after the restart, with the deadline's own time
  in the event.
- **Distributed runs** are evaluated the same way. The scheduler reads run
  status; it does not need the worker.
- **A paused scheduler still evaluates deadlines.** Pausing stops new runs,
  not notification delivery.

### The event

`dag.run.deadline_missed` joins the event store's `dag.run.*` types. It
carries the DAG, the run ID, the deadline's name, the deadline time, the run
status when it fired, and whether the run was failed.

- **Notification routes** can select it like `dag.run.failed`. A route that
  selects failures does not receive it unless it also selects deadlines.
- **Incident policies** can open an incident from it.
- **DAG-level email** gains a deadline switch beside failure and success.
- **Reacting with a DAG.** The `dag` trigger source of RFC 004 matches
  `dag.run.*` events, so another DAG can start on a missed deadline.

### `fail: true`

The scheduler stops the run as an operator's stop does, and records the
status `failed` with the reason `deadline missed: <name>`. The `failure` and
`exit` handlers run. A queued run is removed from its queue and marked
`failed`. The DAG's retry policy does not retry it.

## UI

- A run with a deadline shows it in the run header: time left, or how late.
- Run lists show a **Late** badge on runs that missed a deadline, whatever
  their final status.
- The upcoming-runs preview shows each run's deadline.

## Compatibility and rollout

| Release | Ships |
| --- | --- |
| First | `deadline`, scheduler evaluation, the event, notification routes, and the run badge. |
| Second | `fail: true`, incident policies, and DAG-level email. |

- `deadline` is a new top-level key. Existing DAGs are unaffected.
- `at` uses `context.run.timezone` once the first release of RFC 007 ships.
  Before that it uses the schedule entry's zone, computed the same way.
- `timeout_sec` is unchanged. The two can be combined: a deadline to alert,
  a timeout to stop.

## Verification and acceptance criteria

1. A run scheduled at 01:00 and still running at 05:00 with `at: "05:00"`
   produces one event at 05:00, and keeps running.
2. A run that finishes at 04:59 produces none.
3. A run still queued at its deadline produces the event.
4. A run waiting for approval at its deadline produces the event.
5. Restarting the scheduler after the event does not produce it again.
6. With the scheduler down from 04:00 to 06:00, the event appears at 06:00
   and names 05:00 as the deadline.
7. With `fail: true`, the run ends `failed`, its `failure` handler runs, and
   it is not retried.
8. `at: "05:00"` on a `CRON_TZ=Asia/Tokyo` schedule fires at 05:00 Tokyo
   time on a server whose zone is UTC.
9. A backfill run of last week's slot produces no event.

## Alternatives considered

- **A `handler_on.deadline` handler.** Handlers run in the run's process. A
  queued run has none, and a hung one cannot be trusted to run it.
- **Check the deadline inside the run.** Misses queued, waiting, and hung
  runs, which are the cases that matter most.
- **A watchdog DAG** that polls run status on a schedule. Every team writes
  its own, and it has no view of the deadline in the UI.
- **Extend `timeout_sec` with an `on_timeout: alert` mode.** It would still
  count from the run's start and still be a duration.
- **Child steps under the deadline,** as Digdag's `sla:` allows. The event
  plus a `dag` trigger does the same without a second place for steps.

## Consequences

- Lateness becomes a first-class signal beside failure.
- The scheduler scans runs with open deadlines every tick. The cost grows
  with the number of unfinished runs that declare one.
- A run can be both `succeeded` and late. Views and metrics must not fold
  the two together.

## Open questions

1. Should a slot that produced no run at all miss its deadline? A scheduler
   outage past `catchup_window` is the worst case and is not covered here.
2. Should a deadline be allowed on a DAG's `base.yaml`, so one line covers
   every DAG?
3. Should `at` accept a day offset, such as `"05:00+1d"`, for jobs whose
   deadline is more than a day after the slot?
4. Should there be a warning before the deadline, such as `warn_before:
   30m`?
