# Spec 097: Derived Dates

## Status

Partially implemented. Loading, validation, resolution for runs,
`${dates.NAME}`, and the default slot are implemented; conformance covers
them through `dagu validate`, `dagu start`, sub-DAG runs, `parallel`, and
`dagu retry`, and the previews through `dagu dry`, `dagu calendar fires`, and
`dagu start`; the API previews are covered by API tests. Not yet implemented:
reading dates in `params[].eval`, and the UI previews.

## Scope

This spec defines the top-level `dates` key: dates a run derives from its
schedule slot, read as `${dates.NAME}`. It covers the entry forms, how an entry
resolves, the formats, and validation.

It does not define how a run gets its slot or window, which
[Spec 017: Built-In Run Context](017-built-in-run-context.md) defines, or how
`dagu backfill` selects slots by a date.

## Goal

A scheduled workflow names the dates it loads, such as the day before its
slot, once. Every run of a slot, whether the scheduler started it, a person
started it for that slot, or a retry, gets the same dates.

## Behavior

### Entries

```yaml
schedule: "CRON_TZ=Asia/Tokyo 0 2 * * *"
calendar: jp-banking

dates:
  target_day:     -1d
  target_compact: { from: target_day, format: date_compact }
  month_start:    [-1d, month_start]
  last_open_day:  -1bd
  window_start:   { from: interval_start, format: rfc3339 }
  window_days:    { each: 1d }
  loaded_at:      { format: "%Y/%m/%d %H:%M", timezone: UTC }
```

`dates` is a mapping of names to entries, kept in document order. A name
matches `^[A-Za-z][A-Za-z0-9_]*$` and is not `scheduled_at`,
`interval_start`, `interval_end`, or `next_scheduled_at`.

An entry is a shift written as a string or a list, or an object:

| Field | Meaning | Default |
| --- | --- | --- |
| `from` | The instant the entry starts from: `scheduled_at`, `interval_start`, `interval_end`, `next_scheduled_at`, or the name of another entry. | `scheduled_at` |
| `each` | A step through the window. The entry becomes a list. | none |
| `shift` | One operation, or a list applied left to right. | none |
| `format` | A named format or a strftime pattern. | `date` |
| `timezone` | The zone used for shifting and formatting. | See "Time zones". |

`target_day: -1d` means `{ shift: -1d }`, and `month_start: [-1d, month_start]`
means `{ shift: [-1d, month_start] }`. An object takes no other key.

The instants an entry starts from are the run's `context.run` values of
Spec 017: `scheduled_at` and `interval_end` are the slot, `interval_start` is
the previous fire, and `next_scheduled_at` is the next fire.

### Shift operations

| Operation | Meaning |
| --- | --- |
| `+N` or `-N` with `min` or `h` | Elapsed minutes or hours. |
| `+N` or `-N` with `d`, `w`, `mo`, or `y` | Calendar days, weeks, months, or years in the zone. |
| `+N` or `-N` with `bd` | Business days of the calendar in effect for the slot. |
| `hour_start`, `day_start`, `week_start`, `month_start`, `year_start` | The start of that unit in the zone. A week starts on Monday. |
| `bd_or_next`, `bd_or_prev` | The same instant when its date is a business day, else the next or the previous business day. |

- `N` is a whole number from 0 to 10000 and needs its sign.
- A day, week, or business-day shift keeps the wall-clock time, also across
  a daylight saving change.
- A month or year shift keeps the day of the month, clamped to the target
  month's last day: March 31 minus one month is the last day of February.
- A business day is a date the calendar counts as one in the zone.

### Formats

| Name | Example |
| --- | --- |
| `date` | `2026-11-09` |
| `date_compact` | `20261109` |
| `datetime` | `2026-11-09 02:00:00` |
| `rfc3339` | `2026-11-09T02:00:00+09:00` |
| `unix` | `1794157200` |

A format that is not a name is a strftime pattern and must contain `%`. It
takes these directives:

| Directive | Value |
| --- | --- |
| `%Y`, `%y` | Year, and its last two digits |
| `%m`, `%d`, `%e` | Month and day, zero-padded; day padded with a space |
| `%H`, `%I`, `%M`, `%S`, `%p` | Hour (24 and 12), minute, second, `AM` or `PM` |
| `%a`, `%A`, `%b`, `%h`, `%B` | Weekday and month names, short and long, in English |
| `%j` | Day of the year, `001` to `366` |
| `%u`, `%w` | Day of the week, `1` (Monday) to `7`, and `0` (Sunday) to `6` |
| `%V`, `%G` | ISO 8601 week number and its year |
| `%Z`, `%z` | Zone abbreviation, and offset such as `+0900` |
| `%s` | Unix seconds |
| `%F`, `%T`, `%R` | `%Y-%m-%d`, `%H:%M:%S`, `%H:%M` |
| `%%` | `%` |

### Building on another entry

`from: target_day` starts from `target_day`'s instant after its shift, not
from its formatted text. The entry takes that entry's zone unless it sets its
own, and does not take its format. Entries may build on entries declared
after them. A chain of entries that leads back to itself is a cycle.

### Lists

An entry with `each` lists the instants from `interval_start` up to, but not
including, `interval_end`, a step apart. Its shift and format apply to each
value.

- The step has no sign: `1d`, `2h`, `1bd`. It is at least 1.
- Day and longer steps keep the wall-clock time of `interval_start`. Each
  value is stepped from `interval_start`, so a month step clamped to a short
  month does not move later values.
- A `bd` list starts at the first business day at or after `interval_start`.
- `each` cannot be combined with `from`.
- A step under one day needs a `format`, since the default `date` would
  repeat values.
- A list holds at most 1,000 values, the `parallel` limit.

For a daily 02:00 schedule on a weekday calendar, Monday's run owns the window
from Friday 02:00. Its `{ each: 1d }` lists Friday, Saturday, and Sunday, and
its `{ each: 1bd }` lists Friday.

### Time zones

An entry is shifted and formatted in, by priority:

1. its `timezone`, an IANA zone name;
2. the zone of the entry it builds on;
3. `context.run.timezone`, the zone of the slot's schedule;
4. the server's `tz`, else the host's local zone.

A slot's zone given as a UTC offset, such as `UTC+09:00`, is that fixed
offset.

### Business days

Business days use the calendar of the schedule entry that owns the slot, its
own or the DAG's. A run whose slot no entry of its own DAG owns, such as a
sub-DAG run, uses the DAG's `calendar`.

### A run without a slot

A run that names no slot of a DAG with a recurring start schedule gets the
default slot of [Spec 017](017-built-in-run-context.md): the latest fire at or
before its creation. A monthly DAG fired on the 1st and started by hand on the
15th resolves its dates as the run of the 1st did.

A run that still has no slot, because its DAG has no recurring start
schedule and no parent run gives it one, resolves `scheduled_at` and
`interval_end` against the time the run was created, cut to the minute. An
entry that needs a window, one with `each` or one that starts from
`interval_start` or `next_scheduled_at`, directly or through the entries it
builds on, cannot resolve without a slot.

### Resolution

A run's dates resolve once, when the run is created, and are recorded on the
run. Every attempt reads the recorded values.

`dagu start`, `dagu enqueue`, `dagu restart`, and the API's start and enqueue
resolve the dates before the run exists, and refuse a run whose dates cannot
resolve. A sub-DAG run resolves its own DAG's dates when it starts, and fails
at start, naming the date, when one cannot resolve.

### Previews

A slot's dates can be read before a run exists, and after it records them:

- `dagu calendar fires`, with or without `--from`, adds a `DATES` column
  for a DAG with `dates`: each entry as `name=date`, and a list as
  `name=first..last`.
- `dagu dry [--scheduled-at]` prints the slot, its window, and the dates the
  real run of that slot records, under `Dates:`. With no `--scheduled-at` it
  takes the default slot, and says so.
- `dagu start` and `dagu enqueue` report on stderr the slot and dates of the
  run they create, as `Dates for slot <time>: name=date ...`.

- The upcoming runs of a DAG's details, and of the MCP `dag` read as
  `upcomingRunDates`, list the dates each fire's run gets.
- `GET /api/v1/dags/{fileName}/slot-preview?scheduledAt=` returns the slot a
  run would be started for, as the start API reads `scheduleTime`, with its
  window and dates, and starts nothing. With no `scheduledAt` it previews the
  default slot. A date that cannot resolve returns 400 with the reason.
- A run's details, and the MCP `run` read, include the dates it recorded and
  whether its slot was the default.

### References

`${dates.NAME}` is a Dagu-owned reference in the `dates` namespace of
[Spec 003](003-value-resolution.md). It resolves wherever `${params.NAME}`
does, except in the fields that resolve while the DAG loads, before a run's
dates exist: `consts`, `params[].eval`, and the map form of
`worker_selector`. There the reference is preserved.

- A single entry inserts its formatted text.
- A list inserts a JSON array of its values, such as
  `["2026-11-06","2026-11-07","2026-11-08"]`, so `parallel: ${dates.days}`
  runs one item per date.
- A `dates` param stays addressable as `${params.dates}`.
- Outside a run, as in `dagu validate`, a declared name is unavailable and is
  preserved with a runtime-only notice. A name the DAG does not declare is a
  defect.
- Dates are not environment variables. A DAG passes the ones a script needs
  through `env`.

## Errors

`dagu validate` and every load of the DAG fail, naming the entry and the
field, when:

- a name is invalid, reserved, or used twice;
- an object has a key other than `from`, `each`, `shift`, `format`, and
  `timezone`, or a field is not a non-empty string;
- `from` names neither an instant nor another entry, names a list, or is
  combined with `each`;
- entries build on each other in a cycle;
- a shift operation or step is unknown, lacks or has a sign, or exceeds
  10000. The unit `m` is unknown, and its error names `min` and `mo`, so a
  month cannot silently become a minute;
- a step under one day has no `format`;
- a format is neither a name nor a strftime pattern, or uses an unknown
  directive;
- a zone is unknown;
- an entry uses business days through `bd`, `bd_or_next`, or `bd_or_prev`
  while a recurring start schedule entry has no calendar, or while the DAG has
  neither a recurring start schedule entry nor a `calendar`.

On a DAG with no recurring start schedule entry, an entry that needs a window
is valid, since a sub-DAG run takes its parent's slot, and loading warns.

Resolving fails, naming the entry, when an entry needs a window the run does
not have, the schedule fires no time before or after the slot an entry starts
from, the run has no calendar for a business-day entry, no business day falls
within five years of a date, or a list exceeds 1,000 values.

## Examples

For the run fired at 2026-11-10 02:00 in Tokyo, the entries above resolve to:

| Entry | Value |
| --- | --- |
| `target_day` | `2026-11-09` |
| `target_compact` | `20261109` |
| `month_start` | `2026-11-01` |
| `last_open_day` | `2026-11-09` |
| `window_start` | `2026-11-09T02:00:00+09:00` |
| `window_days` | `["2026-11-09"]` |
| `loaded_at` | `2026/11/09 17:00` |

With the weekday calendar, `{ shift: [month_start, +1mo, -1d, bd_or_prev] }`
on 2026-05-15 resolves to `2026-05-29`, the Friday before a month end that
falls on a Sunday.
