# RFC 007: Schedule Window and Derived Dates

- **Status:** Proposed
- **Date:** 2026-11-10
- **Affected areas:** `spec`, `ir`, `runtime` (run context), `intake`,
  `service/scheduler`, `service/frontend` (API, UI), `service/mcp`, `cmd`,
  `specs` (017), and `docs`

## Summary

Give every run of a scheduled DAG the window of time it owns, and let the DAG
declare the dates it derives from that window.

A run gets `interval_start`, `interval_end`, `next_scheduled_at`, and the
timezone of its schedule, next to the existing `scheduled_at`. A manual run can
be given a scheduled time, so one DAG serves scheduled runs, reruns of a past
day, and backfills (RFC 008).

A `dates` block turns those instants into the strings a job needs: yesterday in
Tokyo, the first day of the month, the last business day. Dates are validated
when the DAG loads, are the same on every retry, and can be previewed before
the run exists.

## Problem statement

`${context.run.scheduled_at}` is the only schedule value a run receives. It is
a UTC RFC 3339 timestamp, and it is missing on manual runs. Batch jobs need
more than that:

- **The window.** A job that runs every 30 minutes loads the 30 minutes before
  its fire time. The previous fire time is not available
  (dagucloud/dagu#3279).
- **Local dates.** A job that fires at 02:00 in Tokyo processes the previous
  Tokyo calendar day. From a UTC timestamp, the step must convert the zone
  and subtract a day itself.
- **Portability.** Steps do this with `date`, whose flags differ between GNU
  and BSD. The scheduling guide already shows
  `date -d tomorrow || date -v+1d`. Windows has neither.
- **Manual runs.** Rerunning one past day means editing params by hand,
  because a manual run has no scheduled time.
- **Preview.** Nothing shows which dates a run will use until it runs.

Other schedulers answer this with a logical time: Airflow's data interval,
Dagster's partitions, Digdag's `session_time` and `last_session_time`.
Digdag evaluates JavaScript with Moment.js inside `${...}` for the arithmetic.

## Goals

1. A run knows the window its schedule slot owns, derived from the schedule
   and not from run history.
2. A run can be started for a chosen slot from the CLI, API, UI, and MCP.
3. Common date arithmetic needs no shell and gives the same result on Linux,
   macOS, and Windows.
4. Derived dates are validated at load and never change across retries.
5. The upcoming-runs preview shows the dates each run will use.

## Non-goals

- A general expression language in `${...}`. The `js.run` action covers
  arbitrary computation.
- "Since the last successful run" windows. That is a watermark, covered by
  `state`.
- Non-time partitions (dagucloud/dagu#2645).
- Changing `scheduled_at`. It stays UTC RFC 3339.

## Schedule window

New fields under `context.run`, following the proposal in dagucloud/dagu#3279:

| Field | Meaning |
| --- | --- |
| `context.run.scheduled_at` | Existing. The fire time of the slot. |
| `context.run.interval_start` | The latest fire time of the DAG's active start schedules strictly before `scheduled_at`. |
| `context.run.interval_end` | The same instant as `scheduled_at`. |
| `context.run.next_scheduled_at` | The earliest fire time strictly after `scheduled_at`. |
| `context.run.timezone` | The IANA zone the slot's schedule entry was evaluated in. |

- **Format.** The three new instants are UTC RFC 3339, like `scheduled_at`.
- **Derived from the schedule.** `interval_start` is the previous fire time,
  whether or not a run happened then. A retry, a catchup run, and a backfill
  of one slot all see the same window.
- **Several schedule entries.** Previous and next are taken across all active
  start entries, so a cadence split over entries yields contiguous windows.
- **Business calendars.** A fire dropped by a calendar is not a fire. The
  window before a Monday run starts at Friday's fire.
- **Timezone.** The entry's `CRON_TZ` or `TZ` prefix, else the server `tz`,
  else the host's local zone. `@every` uses UTC.
- **Recorded on the run.** The scheduler computes the values at dispatch and
  stores them with the run. Retries keep them after the schedule is edited.
- **Sub-DAGs** do not inherit the window. The parent passes dates as params,
  as it passes any other value.

### Choosing the slot of a manual run

`dagu start`, `dagu enqueue`, the run dialog, the `start` and `enqueue`
endpoints, and the MCP execute tool accept a scheduled time:

```sh
dagu start etl --scheduled-at 2026-11-09T02:00:00+09:00
```

- The timestamp is RFC 3339 with an offset, at minute precision, like an `at`
  schedule entry.
- The run records `scheduled_at` and the window as if the scheduler had fired
  that slot. The run's trigger type stays `manual`.
- A time that is not a fire time of the schedule is accepted. Its window runs
  from the previous fire time to the given time.
- For a DAG with no schedule, `interval_start` and `next_scheduled_at` stay
  missing.
- The run dialog offers the latest past slot when the DAG has `dates`.

A manual run without a scheduled time has no slot. Its `scheduled_at` and
window stay missing, as today.

## Derived dates

```yaml
schedule: "CRON_TZ=Asia/Tokyo 0 2 * * *"
calendar: jp-holidays

dates:
  target_day:     { shift: -1d }
  target_compact: { shift: -1d, format: date_compact }
  month_start:    { shift: [-1d, month_start] }
  prev_month_end: { shift: [month_start, -1d] }
  last_open_day:  { shift: -1bd }
  window_start:   { from: interval_start, format: rfc3339 }
  loaded_at:      { format: "%Y/%m/%d %H:%M", timezone: UTC }

steps:
  - id: load
    run: ./load.sh --date ${dates.target_day} --since ${dates.window_start}
```

For the run fired at 2026-11-10 02:00 in Tokyo, `target_day` is `2026-11-09`,
`month_start` is `2026-11-01`, and `prev_month_end` is `2026-10-31`.

| Field | Meaning | Default |
| --- | --- | --- |
| `from` | `scheduled_at`, `interval_start`, or `next_scheduled_at`. | `scheduled_at` |
| `shift` | One operation, or a list applied left to right. | none |
| `format` | A named format or a strftime pattern. | `date` |
| `timezone` | The zone used for shifting and formatting. | `context.run.timezone` |

Shift operations:

| Operation | Meaning |
| --- | --- |
| `+N` or `-N` with `m`, `h` | Elapsed minutes or hours. |
| `+N` or `-N` with `d`, `w`, `mo`, `y` | Calendar days, weeks, months, or years in the zone. |
| `+N` or `-N` with `bd` | Business days of the DAG's `calendar`. |
| `hour_start`, `day_start`, `week_start`, `month_start`, `year_start` | The start of that unit in the zone. |

Named formats:

| Name | Example |
| --- | --- |
| `date` | `2026-11-09` |
| `date_compact` | `20261109` |
| `datetime` | `2026-11-09 02:00:00` |
| `rfc3339` | `2026-11-09T02:00:00+09:00` |
| `unix` | `1794157200` |

Rules:

- **Reference.** `${dates.NAME}` resolves wherever `${params.NAME}` does.
  `dates` is a reserved root namespace; a param named `dates` stays
  addressable as `${params.dates}`.
- **Validation at load.** An unknown `from`, operation, format directive, or
  zone fails `dagu validate`. So does `bd` without a `calendar`.
- **Resolved once.** Dates are computed when the run is created and recorded
  on the run. Every attempt reads the recorded values.
- **Calendar arithmetic.** A day shift keeps the wall-clock time across a
  daylight saving change. A month shift clamps to the last day of the month:
  March 31 minus one month is the last day of February.
- **Week start** is Monday.
- **A run without a slot.** A manual run with no scheduled time resolves
  `from: scheduled_at` against the time the run was created, cut to the
  minute. A date whose `from` is `interval_start` or `next_scheduled_at`
  fails the run at start with a message naming `--scheduled-at`. The run
  details mark dates that came from the clock.
- **Environment.** Dates are not projected into environment variables. A DAG
  passes the ones a script needs through `env`.

### Preview

`dagu calendar fires <DAG>`, the Cron launcher, `upcomingRuns` in the API, and
the MCP read result list the resolved dates beside each upcoming run. The
editor shows them as the YAML changes.

## Compatibility and rollout

| Release | Ships |
| --- | --- |
| First | `interval_start`, `interval_end`, `next_scheduled_at`, `timezone`, and `--scheduled-at`. Closes dagucloud/dagu#3279. |
| Second | `dates` and its preview. |

- Existing DAGs are unaffected. `dates` is a new top-level key.
- A DAG that uses `${dates.x}` as plain text today changes meaning. Spec 003
  preserves unknown reference-looking text, so such text is possible. The
  release notes must say so.
- Spec 017 gains the new `context.run` fields. A new spec owns `dates`.

## Verification and acceptance criteria

1. A DAG on `*/30 * * * *` fired at 12:00 sees `interval_start` 11:30 and
   `next_scheduled_at` 12:30.
2. A DAG on `CRON_TZ=Asia/Tokyo 0 2 * * *` fired on 2026-11-10 resolves
   `{ shift: -1d }` to `2026-11-09` on a server whose zone is UTC.
3. A retry started after the schedule is edited sees the original window and
   dates.
4. `dagu start --scheduled-at` for a slot three days ago gives the same
   window and dates as the scheduled run of that slot had.
5. With a weekday calendar, `-1bd` on a Monday resolves to Friday.
6. `{ shift: [month_start, -1d] }` on 2026-03-15 resolves to `2026-02-28`.
7. The same DAG gives the same dates on Linux, macOS, and Windows.
8. `dagu validate` rejects `shift: -1bd` when the DAG names no calendar.

## Alternatives considered

- **JavaScript in `${...}`,** as Digdag does. Any field could then run code.
  Validation would have to execute it, and the preview could not be trusted.
- **Filters on references,** such as `${context.run.scheduled_at | shift
  "-1d"}`. The same arithmetic would be repeated in every step that needs
  the date, and the preview would have to scan every field.
- **Zone-local variants of every context field,** such as Digdag's
  `session_date_compact` and `last_session_local_time`. Digdag ships 20 of
  them and still needs Moment.js for yesterday.
- **Leave it to `date`.** Not portable, not validated, and invisible until
  the step runs.
- **A `context.logical` namespace** separate from `scheduled_at`. Two names
  for one instant.

## Consequences

- One DAG handles scheduled runs, reruns of a past slot, and backfills
  without date params.
- The schedule becomes part of what a run computes, not only when it starts.
  Editing a schedule changes the windows of later runs.
- `dates` is a second small language beside cron. It must stay closed: new
  operations need a spec change.

## Open questions

1. Should a manual run with no scheduled time fail when the DAG has `dates`,
   instead of using the clock?
2. Should `week_start` be configurable, or follow the calendar?
3. Should `dates` allow one entry to build on another (`from: target_day`)?
4. Should catchup and backfill runs of a sub-DAG inherit the window after
   all, so shared child DAGs need no date params?
