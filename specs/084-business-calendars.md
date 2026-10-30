# Spec 084: Business Calendars

## Status

Implemented.

## Scope

This specification covers calendar files, the `calendar` and `business_days`
keys of start schedules, and the fire times that follow from them. It does not
define moving a fire to another business day, deadlines, or calendars on stop
and restart schedules. Scheduler dispatch and timing guarantees are outside
this conformance suite.

## Goal

Run a workflow on business days only, or on chosen business days of each month,
with one shared list of holidays per calendar.

## Calendar files

A calendar is a YAML file named `<name>.yaml` in `paths.calendars_dir`, which
defaults to `$DAGU_HOME/calendars` (environment variable `DAGU_CALENDARS_DIR`).
A name uses letters, digits, `-`, and `_`.

```yaml
description: Japanese bank business days
weekdays: [MON, TUE, WED, THU, FRI]
holidays:
  - 2027-01-01
  - date: 2027-01-11
    name: Coming of Age Day
  - from: 2027-12-31
    to: 2028-01-03
    name: Year-end closure
extra_business_days:
  - 2027-10-09
include: [jp-national]
through: 2027-12-31
```

| Key | Meaning |
| --- | --- |
| `description` | Text shown in listings. |
| `weekdays` | Days of the week that are business days unless they are holidays. One or more of `MON`, `TUE`, `WED`, `THU`, `FRI`, `SAT`, `SUN`, in any case. Defaults to `MON` to `FRI`. |
| `holidays` | Dates that are not business days: a `YYYY-MM-DD` date, `{date, name}`, or an inclusive range `{from, to, name}` of at most 366 days. `name` is optional. |
| `extra_business_days` | Dates that are business days whatever their weekday, even when they are also holidays. |
| `include` | Calendars whose holidays this calendar adds. Only holidays are added. |
| `through` | The last date the holidays are maintained for. Optional. |

A date is a business day when it is an extra business day, or when it is not a
holiday and its weekday is in `weekdays`.

The calendar `weekdays` is built in: Monday to Friday with no holidays. The
names `weekdays` and `none` are reserved; a file with either name is reported
as invalid.

`through` does not change evaluation. Dates after it count the weekdays alone
because no holidays are listed for them. From 30 days before `through`, and
after it, loading a DAG that uses the calendar reports a warning naming the
calendar and the date.

## Schedule keys

A start schedule entry in object form accepts two more keys.

| Key | Meaning |
| --- | --- |
| `calendar` | The calendar whose business days the entry fires on, or `none`. |
| `business_days` | A list of integers from 1 to 23, or -23 to -1. The entry keeps only fires on these business days of the month: 1 is the first business day, -1 the last. |

A DAG-level `calendar` key sets the calendar of every start schedule entry,
including plain strings, that names none. A base config may set it. An entry
with `calendar: none` has no calendar.

```yaml
calendar: jp-banking
schedule:
  - "CRON_TZ=Asia/Tokyo 0 18 * * *"
  - expression: "CRON_TZ=Asia/Tokyo 0 23 * * *"
    business_days: [3, -1]
  - expression: "0 3 * * *"
    calendar: none
steps:
  - run: echo scheduled
```

## Fire times

The cron expression produces candidate times. A schedule with a calendar fires
on the candidates whose date is a business day, and, when `business_days` is
set, whose date is one of the listed business days of its month. Other
candidates are dropped; no fire is moved to another time.

A candidate's date is read in the zone its cron expression names with a `TZ=`
or `CRON_TZ=` prefix, otherwise in the zone the schedule is evaluated in
(`core.tz`).

Every surface that reports fire times uses this rule: the scheduler, catch-up,
`dagu ls -n`, `dagu calendar fires`, and the upcoming runs in DAG details. A
schedule whose calendar cannot be loaded never fires.

`dagu calendar fires <DAG> [--from T] [--count N]` lists the next fires after
`T` (an RFC 3339 time, default now), ten by default. Each row has the fire time
with its offset, the schedule expression, and a note: the listed business day
the fire matched (`3rd business day`, `last business day`), `extra business
day`, and, for an entry without `business_days`, each holiday since the
previous fire on which the expression alone would have fired
(`skips Mon 2027-01-11 (Coming of Age Day)`).

`dagu calendar check [files...]` validates calendar files, by default every
file in `paths.calendars_dir`. A file given as a path resolves `include`
against the files beside it. The command exits non-zero when any calendar is
invalid.

## Errors

Loading a DAG fails when:

- a start schedule names a calendar that is neither built in nor a file; the
  message lists the available calendars;
- `business_days` is set and the entry has no calendar after the DAG default
  applies;
- `business_days` is set and the expression's day-of-month or day-of-week
  field is not `*`, including `@every` and descriptors such as `@monthly`;
- a `business_days` value is 0, outside -23 to 23, or listed twice;
- a one-off (`at`) entry sets `calendar` or `business_days`;
- a stop or restart entry sets `calendar` or `business_days`.

A calendar file fails to load on an unknown key, a malformed date, a range that
ends before it starts or exceeds 366 days, an empty `weekdays` list, an unknown
weekday, an include cycle, or an included calendar that does not exist.

## Conformance

`conformance/spec084_calendars/` checks schedule validation with calendars, the
rows `dagu calendar fires` prints for holidays, business days, extra business
days, and the built-in calendar, `dagu calendar check` on valid and invalid
files, and that `dagu ls -n` skips holidays.
