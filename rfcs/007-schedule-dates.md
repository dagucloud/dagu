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
- **Manual runs.** Rerunning one past day means editing params by hand. The
  `--schedule-time` flag exists for the scheduler's own use and is
  unavailable in remote contexts. The API, UI, and MCP have no equivalent.
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
5. Upcoming runs and any chosen slot show their window and dates before the
   run starts.
6. A scheduled DAG's `dates` always come from a schedule slot, whichever
   trigger starts the run.

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
- **Bounds.** The window is half-open: `[interval_start, interval_end)`. An
  event at exactly `interval_end` belongs to the next slot.
- **Derived from the schedule.** `interval_start` is the previous fire time,
  whether or not a run happened then. A retry, a catchup run, and a backfill
  of one slot all see the same window.
- **Active entries** are the recurring start entries with no `profile` or
  with the profile of the run. One-off `at` entries are not part of the
  cadence and never bound another run's window. A run fired by an `at` entry
  gets the window of an off-schedule time.
- **Several schedule entries.** Previous and next are taken across all active
  start entries, so a cadence split over entries yields contiguous windows.
- **Business calendars.** A fire dropped by a calendar is not a fire. The
  window before a Monday run starts at Friday's fire.
- **Timezone.** The entry's `CRON_TZ` or `TZ` prefix, else the server `tz`,
  else the host's local zone. `@every` uses UTC. A zone with no IANA name,
  such as the host's zone when `tz` is unset, is named by its UTC offset,
  such as `UTC+09:00`. A slot at a time no entry
  fires belongs to the entry of its previous fire time. A DAG with no
  recurring start entry uses the server `tz`, else the host's local zone.
- **Recorded on the run.** The scheduler computes the values at dispatch and
  stores them with the run. Retries keep them after the schedule is edited.
- **Reruns.** `dagu restart` and the reschedule action start a new run for
  the same slot, unless `--scheduled-at` names another. The new run copies
  `scheduled_at`, the window, and `timezone` from the original and resolves
  `dates` from the definition it runs. A reschedule from the current DAG file
  keeps the slot and computes its window from the current schedule, the
  schedule of the definition it runs.
- **Sub-DAGs** take `scheduled_at`, the window, and `timezone` from a parent
  run that has a slot, so a backfill reaches shared child DAGs. A child's
  `dates` resolve against the inherited slot.

### Choosing the slot of a manual run

`dagu start`, `dagu enqueue`, `dagu restart`, `dagu dry`, the run dialog, the
`start` and `enqueue` endpoints (`scheduleTime`), and the MCP execute tool
(`scheduleTime`) accept a scheduled time:

```sh
dagu start etl --scheduled-at latest
dagu start etl --scheduled-at 2026-11-09
dagu start etl --scheduled-at 2026-11-09T02:00
dagu start etl --scheduled-at 2026-11-09T02:00:00+09:00
```

| Form | Meaning |
| --- | --- |
| `latest` | The latest fire time at or before now. |
| A date | The fire time on that date. With none or several, the command fails and lists the nearest fire times. |
| A time without an offset | That wall-clock time in the schedule's zone. |
| RFC 3339 with an offset | That instant. |

- Times are at minute precision, like an `at` schedule entry.
- Forms without an offset use the zone of the recurring start entries. When
  entries use different zones, those forms are rejected and the message
  lists the zones. A DAG with no recurring start entry uses the server zone
  and rejects `latest` and a date.
- `--scheduled-at` works in local and remote contexts. It replaces
  `--schedule-time` for operators. The scheduler keeps launching runs with
  `--schedule-time`, now hidden, which takes the RFC 3339 instant the
  scheduler fired and neither rejects nor warns: a restart schedule's fire
  is not a start fire, and a warning on every such run would be noise.
- The run records `scheduled_at` and the window as if the scheduler had fired
  that slot. The run's trigger type stays `manual`.
- A slot later than now is rejected, as Airflow and Control-M reject one. A
  run for a day that has not happened loads nothing, or loads part of a day
  and records it as done. The error names the latest fire time. `dagu dry`
  and the previews accept a future slot, because they start nothing.
- A time that is not a fire time of the schedule is accepted, because a slot
  from before a schedule edit is no longer a fire time. Its window runs from
  the previous fire time to the given time. Every surface warns and names
  the nearest fire times. The run dialog lists fire times and needs an
  explicit choice for any other time.
- For a DAG with no recurring start entry, `interval_start` and
  `next_scheduled_at` stay missing.
- The scheduler's already-finished and `skip_if_successful` checks read a
  manual run by when it ran, as they do today. A chosen slot cannot suppress
  a scheduled fire.
- The run dialog preselects the latest past slot when the DAG has `dates`.

### A run with no chosen slot

A manual, webhook, or file-triggered run that names no slot, and a sub-DAG of
a parent with none, gets:

| DAG | The run's slot |
| --- | --- |
| Recurring start entries and `dates` | The default slot: the latest fire time at or before the run's creation, recorded as if chosen. |
| Recurring start entries, no `dates` | None. `scheduled_at` and the window stay missing, as today. |
| No recurring start entry | None. |

The default slot keeps a hand-started run on the dates the schedule gave. A
monthly DAG fired on the 1st and started by hand on the 15th processes what
the run on the 1st processed. The run details say when a slot was the
default.

## Derived dates

```yaml
schedule: "CRON_TZ=Asia/Tokyo 0 2 * * *"
calendar: jp-holidays

dates:
  target_day:     -1d
  target_compact: { from: target_day, format: date_compact }
  month_start:    [-1d, month_start]
  prev_month_end: [month_start, -1d]
  last_open_day:  -1bd
  window_start:   { from: interval_start, format: rfc3339 }
  window_days:    { each: 1d }
  loaded_at:      { format: "%Y/%m/%d %H:%M", timezone: UTC }

steps:
  - id: load
    run: ./load.sh --date ${dates.target_day} --since ${dates.window_start}
```

For the run fired at 2026-11-10 02:00 in Tokyo, `target_day` is `2026-11-09`,
`month_start` is `2026-11-01`, `prev_month_end` is `2026-10-31`, and
`window_days` is `["2026-11-09"]`.

Monday's run owns the window from Friday's fire. On 2026-11-09, `window_days`
is `["2026-11-06", "2026-11-07", "2026-11-08"]`, while `target_day` names only
Sunday. A job that loads one day at a time uses
`parallel: ${dates.window_days}`.

| Field | Meaning | Default |
| --- | --- | --- |
| `from` | `scheduled_at`, `interval_start`, `interval_end`, `next_scheduled_at`, or the name of another entry. | `scheduled_at` |
| `each` | A step through the window. The entry becomes a list. | none |
| `shift` | One operation, or a list applied left to right. | none |
| `format` | A named format or a strftime pattern. | `date` |
| `timezone` | The zone used for shifting and formatting. | `context.run.timezone`, else the server `tz`, else the host's local zone |

An entry written as a string or a list is its `shift`: `target_day: -1d` means
`{ shift: -1d }`.

Shift operations:

| Operation | Meaning |
| --- | --- |
| `+N` or `-N` with `min`, `h` | Elapsed minutes or hours. |
| `+N` or `-N` with `d`, `w`, `mo`, `y` | Calendar days, weeks, months, or years in the zone. |
| `+N` or `-N` with `bd` | Business days of the calendar in effect for the slot. |
| `hour_start`, `day_start`, `week_start`, `month_start`, `year_start` | The start of that unit in the zone. |
| `bd_or_next`, `bd_or_prev` | The same day when it is a business day, else the next or the previous business day. |

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
- **Params.** Dates resolve before params, so the slot is known when
  `params[].eval` runs and `eval` can read `${dates.NAME}`.
  `params[].default` stays literal.
- **When `params[].eval` runs.** Today `eval` runs when the DAG is built,
  before the run exists, so it can read OS environment variables, `consts`,
  and earlier params, but not `${context.*}`. This release moves it to run
  creation, after the slot, its window, and `dates` resolve, and before root
  `env`, which may reference params. `eval` then also reads `${dates.*}` and
  the `${context.run.*}` fields a run has at creation: `id`,
  `scheduled_at`, the window, and `timezone`. A metadata-only load still
  skips `eval`. The order of a run's values becomes: consts, slot and
  window, dates, params, env, steps.
- **Building on another entry.** `from: target_day` starts from that entry's
  instant after its `shift`. The new entry takes that entry's `timezone`
  unless it sets its own, and does not take its `format`. An entry cannot be
  named `scheduled_at`, `interval_start`, `interval_end`, or
  `next_scheduled_at`. A cycle, or a `from` that names a list, fails
  `dagu validate`.
- **Lists.** `each` steps from `interval_start` up to, but not including,
  `interval_end`. `shift` and `format` apply to each value, and
  `${dates.NAME}` gives a JSON array. The step has no sign: `1d`, `2h`,
  `1bd`. A `bd` list starts at the first business day at or after
  `interval_start`. `each` cannot be combined with `from`. A step under one
  day needs an explicit `format`, because the default `date` would repeat
  values. A list over 1,000 values, the `parallel` limit, is refused before a
  run exists.
- **Validation at load.** An unknown `from`, operation, format directive, or
  zone fails `dagu validate`. The unit `m` is unknown, and its error names
  `min` and `mo`, so a month cannot silently become a minute.
- **Business days** use the calendar of the slot's schedule entry, its own or
  the DAG's. A run with no entry, such as a sub-DAG or a DAG with no
  schedule, uses the DAG's `calendar`. `dagu validate` fails on `bd`,
  `bd_or_next`, and `bd_or_prev` when a recurring start entry has no
  calendar, or when the DAG has neither a recurring start entry nor a
  `calendar`.
- **Resolved once.** Dates are computed when the run is created and recorded
  on the run. Every attempt reads the recorded values.
- **Calendar arithmetic.** A day shift keeps the wall-clock time across a
  daylight saving change. A month shift clamps to the last day of the month:
  March 31 minus one month is the last day of February.
- **Week start** is Monday.
- **A run without a slot** resolves `from: scheduled_at` and
  `from: interval_end` against the time the run was created, cut to the
  minute. The run details mark dates that came from the clock.
- **A date that needs a window.** `from: interval_start`,
  `from: next_scheduled_at`, `each`, and any entry built on one of them need
  recurring start entries or a parent with a slot. On a DAG with no
  recurring start entry, `dagu validate` warns, and a
  start outside such a parent is refused before a run exists. The error
  names the date and says the DAG has no schedule. The run dialog shows the
  same text and disables Run. A sub-DAG under a parent with no slot fails at
  start and names the date.
- **Environment.** Dates are not projected into environment variables. A DAG
  passes the ones a script needs through `env`.

### Overriding a date for one run

A date is derived from the slot and has no override of its own. A DAG that
allows one declares a param:

```yaml
params:
  - name: date
    eval: ${dates.target_day}

steps:
  - run: ./load.sh --date ${params.date}
```

```sh
dagu start etl -- date=2026-11-01
```

- `eval` runs only when the caller gives no value, so the param follows the
  slot until an operator overrides it.
- The override changes `${params.date}`, not `${dates.target_day}`.
- The run dialog prefills the param from the selected slot, because
  metadata-only loads skip `eval`.

### Preview

`dagu calendar fires <DAG>`, the Cron launcher, `upcomingRuns` in the API, and
the MCP read result list the resolved dates beside each upcoming run.
`dagu calendar fires --from` lists past fires the same way. The editor shows
them as the YAML changes.

A slot's dates usually describe the period before it fired: the slot fired on
November 10 loads November 9. A wrong pick is costliest on a past slot, so
every way of starting one shows the slot, window, and dates:

- `dagu dry [--scheduled-at <slot>]` prints what the run would use.
- The run dialog shows them and updates them as the slot changes.
- `dagu start` and `dagu enqueue` print them when the run is created.
- The API and MCP return them without starting a run.

## Compatibility and rollout

| Release | Ships |
| --- | --- |
| First | `interval_start`, `interval_end`, `next_scheduled_at`, `timezone`, `--scheduled-at`, slot inheritance by sub-DAGs, and reruns that keep the slot. Closes dagucloud/dagu#3279. |
| Second | `dates`, the default slot, and the previews. |

- `dates` is a new top-level key. DAGs that do not use it keep their
  behavior, except for sub-DAGs.
- A sub-DAG of a run with a slot now has `context.run.scheduled_at`, which
  was missing. A clock time in its `wait` steps resolves on the slot's day
  instead of the day the attempt started.
- A DAG that uses `${dates.x}` as plain text today changes meaning. Spec 003
  preserves unknown reference-looking text, so such text is possible. The
  release notes must say so.
- Spec 017 gains the new `context.run` fields and their availability in
  sub-DAGs. A new spec owns `dates`. Specs 003 and 005 add its namespace and
  its use in `params[].eval`, and Spec 003 moves `params[].eval` from DAG
  build to run creation. An `eval` that runs a command now runs once per run
  rather than at every DAG build, which also stops it running when a DAG is
  only listed or validated.

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
8. `dagu validate` rejects `shift: -1bd` when a recurring start entry has
   `calendar: none`, and accepts it when every entry names its own calendar.
9. `dagu validate` rejects `shift: -1m` and names `min` and `mo`.
10. A monthly DAG with `dates`, fired on the 1st and started by hand on the
    15th with no slot, resolves `{ shift: -1d }` to the last day of the
    previous month.
11. `dagu restart` and reschedule of a past slot's run keep its
    `scheduled_at` and window.
12. A sub-DAG with its own `dates`, run by a parent for a past slot, resolves
    them from the parent's slot.
13. `dagu dry --scheduled-at` prints the window and dates that the real run
    of that slot records.
14. `--scheduled-at 2026-11-09` selects the only fire of that day, and fails
    with the list of fires on a DAG with several.
15. An off-schedule `--scheduled-at` warns and names the nearest fire times.
16. A DAG with no schedule and `from: interval_start` is refused at start and
    leaves no run. The same DAG succeeds as a sub-DAG of a run with a slot.
17. A one-off `at` fire between two cron fires leaves the window of the next
    cron fire unchanged.
18. `dagu start --scheduled-at` with a slot later than now is refused, names
    the latest fire time, and leaves no run. `dagu dry --scheduled-at` with
    the same slot prints its window and dates.
19. With a weekday calendar, `{ shift: [month_start, +1mo, -1d, bd_or_prev] }`
    on 2026-05-15 resolves to `2026-05-29`, the Friday before a month end
    that falls on a Sunday.
20. `target_day: -1d` resolves the same as `{ shift: -1d }`.
21. `{ from: target_day, format: date_compact }` gives `20261109` for the run
    of 2026-11-10. A cycle of entries fails `dagu validate`.
22. On a daily DAG with a weekday calendar, Monday's `{ each: 1d }` lists
    Friday, Saturday, and Sunday, and `parallel` runs one child for each.
23. `{ each: 1h }` with no `format` fails `dagu validate`.
24. A param with `eval: ${dates.target_day}` follows the slot.
    `-- date=2026-11-01` overrides it and leaves `${dates.target_day}`
    unchanged.

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
- **Only `+Nbd` and `-Nbd` for business days.** They always move. "The last
  business day of the month" needs a step that stays put on a business day,
  which Control-M's working-day arithmetic also provides beside its offsets.
- **The clock for a manual run with no slot.** A monthly DAG started on the
  15th would load the 14th, and the same DAG would get different dates from
  the CLI and the run dialog.
- **Allow a manual run for a future slot,** to test a month end before it
  comes. `dagu dry` shows the same window and dates without loading a day
  that has no data yet, and a mistyped month is the likelier cause.
- **Fail a manual run with no slot.** Every plain `dagu start` and webhook
  would need a slot the operator has to look up, when the latest slot is
  almost always the one meant.

## Consequences

- One DAG handles scheduled runs, reruns of a past slot, and backfills
  without date params.
- The schedule becomes part of what a run computes, not only when it starts.
  Editing a schedule changes the windows of later runs.
- A manual run of a DAG with `dates` repeats its latest slot rather than
  processing up to now.
- `dates` is a second small language beside cron. It must stay closed: new
  operations need a spec change.

## Open questions

1. Should `week_start` be configurable, or follow the calendar?
