# Spec 063: Schedule Descriptors

## Status

Implemented.

## Scope

This specification covers calendar descriptors in workflow schedules.
Scheduler dispatch and timing guarantees are outside this conformance suite.

## Goal

Use readable calendar schedules with the same behavior as standard cron.

## Behavior

Dagu accepts these descriptors wherever a cron schedule expression is accepted:

| Descriptor | Canonical expression |
| --- | --- |
| `@hourly` | `0 * * * *` |
| `@daily` | `0 0 * * *` |
| `@midnight` | `0 0 * * *` |
| `@weekly` | `0 0 * * 0` |
| `@monthly` | `0 0 1 * *` |
| `@yearly` | `0 0 1 1 *` |
| `@annually` | `0 0 1 1 *` |

A descriptor has the same next run and canonical identity as its corresponding
cron expression. A `TZ=` or `CRON_TZ=` prefix is kept, so
`TZ=Asia/Tokyo @daily` is equivalent to `TZ=Asia/Tokyo 0 0 * * *`. The prefix
accepts IANA time zone names on every supported platform, including hosts
without a system time zone database.

`@every <duration>` (Go duration syntax, e.g. `@every 1h30m`) is also accepted
when the duration is a positive whole number of minutes. It keeps its
descriptor form as the canonical expression, with the shortest duration, so
`@every 60m` and `@every 1h` share an identity. It fires once per interval on a
fixed grid aligned to the Unix epoch, so fire times are deterministic across
scheduler restarts rather than anchored to a start time.

## Errors

Unknown descriptors fail validation; `@reboot` is rejected because it has no
scheduled fire time. `@every` fails validation when its interval is not a
positive whole number of minutes, since the scheduler evaluates schedules on
whole-minute ticks. It also fails with a `TZ=` or `CRON_TZ=` prefix, since its
epoch-aligned grid cannot follow a time zone. Runtime failure, timeout, abort,
and cleanup behavior belong to workflow execution and are outside this scope.

## Examples

```yaml
schedule: "@daily"
steps:
  - run: echo scheduled
```

## Conformance

`conformance/spec063_schedule/` checks descriptor validation, validates a named
time zone prefix without a host time zone database, and compares the next run
reported by `dagu ls -n` for hourly and equivalent cron schedules.
