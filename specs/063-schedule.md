# Spec 063: Schedule Descriptors

## Status

Implemented.

## Scope

This specification covers calendar descriptors in workflow schedules.
It also defines how start schedules supply parameter overrides.

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

## Parameter overrides

A recurring or one-off start entry can supply a `params` mapping. Dagu treats
the mapping as literal run input and fills omitted keys from the DAG's defaults.
For example:

```yaml
params:
  mode: default
schedule:
  - expression: "0 5 * * *"
    params: {mode: y}
  - expression: "0 6 * * *"
    params: {mode: z}
steps:
  - run: ./process.sh "${params.mode}"
```

Live runs and catch-up use the selected entry's inputs. A backfill uses the
matching recurring entry from its pinned DAG definition unless the backfill
supplies explicit `--params`. Retries use the run's saved inputs. Manual starts
continue to use their own inputs and DAG defaults. A `restart_policy` restart
starts from the DAG defaults, not from the entry's `params`.

When multiple active entries fire at the same time, the live scheduler prefers
a due one-off and otherwise selects the first matching recurring entry in YAML
order. Catch-up and backfill select the first matching recurring entry.
Changing only `params` leaves the schedule's identity intact, including the
consumed state of one-off entries.

## Conformance

`conformance/spec063_schedule/` checks descriptor validation, validates a named
time zone prefix without a host time zone database, and compares the next run
reported by `dagu ls -n` for hourly and equivalent cron schedules.
