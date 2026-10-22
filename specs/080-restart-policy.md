# Spec 080: DAG Restart Policy

## Status

Implemented.

This spec defines conformance behavior for the DAG-level `restart_policy`
field: its values, how it combines with other DAG fields, and the validation
errors and warnings it produces.

## Scope

This spec covers:

- the `restart_policy` DAG field and its accepted values
- how `restart_policy` combines with `restart_wait_sec`, `schedule`,
  `retry_policy`, and the base configuration
- the validation errors and warnings of a DAG that sets `restart_policy`
- the `restart` command for a DAG that sets `restart_policy`

This spec does not define:

- when the scheduler starts the runs of a DAG that sets `restart_policy`,
  how they are dispatched, or timing guarantees; scheduler dispatch is
  outside this conformance suite
- the `restart_policy` field of the `container` block, which configures the
  Docker restart policy of a container
- `schedule.restart` and `restart_schedule`
- step-level retry or repeat behavior
- the web UI

## Goal

A workflow author can declare that a DAG is a long-running service, kept
running by Dagu instead of a cron schedule, a supervisor DAG, or an external
watchdog.

## Related Specs

- Built-in run context: [Spec 017](017-built-in-run-context.md), which defines
  the `restart` trigger type
- Schedule descriptors: [Spec 063](063-schedule.md)
- Repeat policy: [Spec 075](075-repeat-policy.md), which repeats a step inside
  one run

## Terms

A service DAG is a DAG whose `restart_policy` is `always` or `unless-stopped`.

## Behavior

### Field

`restart_policy` is a DAG-level field. It accepts:

| Value | Meaning |
| --- | --- |
| `no` | The DAG is not kept running. This is the default. |
| `always` | The DAG is kept running: a new run follows each run, `restart_wait_sec` after it ends, whatever its status. |
| `unless-stopped` | Same as `always`, except that no run follows a run that an operator stopped. |

Rules:

- Values are case-insensitive and surrounding whitespace is ignored.
- An empty value, or an absent field, means `no`.
- A base configuration cannot set `restart_policy` to `always` or
  `unless-stopped`: it applies to every DAG, so it would turn them all into
  services.

### Restrictions

A service DAG:

- must have a `restart_wait_sec` of at least 1, set in its file or inherited
  from the base configuration
- must not define a schedule in its file: `schedule`, in any of its forms,
  including `start`, `stop` and `restart`. An empty `schedule` defines none.
- must not define a DAG-level `retry_policy` in its file

A schedule or a DAG-level `retry_policy` that a service DAG would inherit from
the base configuration is ignored.

A service DAG may set `queue`, `timeout_sec`, `max_clean_up_time_sec`, history
retention, `handler_on`, `preconditions`, and any other field.

### History Retention

A service DAG adds a run to its history each time it is restarted. A service
DAG that sets neither `hist_retention_runs` nor `hist_retention_days`, in its
file or in the base configuration, keeps its latest 100 runs, where other DAGs
keep 30 days of runs.

### Restart Command

The `restart` command fails for a service DAG, without stopping or starting a
run: the runs of a service DAG are started for it. The command reads the
current definition of the DAG, so it also fails for a DAG that has no run yet,
or whose `restart_policy` was set after its latest run started.

### Scheduler Behavior

This section describes the implementation. The conformance suite does not
check it. Integration tests in `internal/intg/queue` and `internal/intg/distr`
cover it.

The scheduler that holds the scheduler lock keeps the service DAGs alive. It
checks them every second and starts a run through the queue, once
`restart_wait_sec` has passed since the previous run ended. A scheduler that
waits for the lock does nothing.

The scheduler decides from the latest run of the DAG, not from its own memory.
It starts no run while the DAG has a live process, a queued item, or an active
run, or while its latest run cannot be read. The run ID of a restart follows from the run that it replaces, so two
schedulers that decide the same restart create one run.

When the active scheduler stops or fails, the DAG keeps running. The
next scheduler continues from the latest run. It takes over when the scheduler
lock is released or stale (`scheduler.lock_stale_threshold`, 30 seconds by
default). It starts no second run while one is active.

When the process of a local run dies, the next read of the latest status marks
the run as failed. The DAG restarts after that. A process that dies while it
writes the status of its run leaves the run lock of the DAG behind, and the
restart then waits until the lock is stale, 30 seconds after it was taken. In distributed mode, the
coordinator marks the run as failed when the worker stops sending heartbeats
and the run lease is stale (90 seconds by default). The queue can need up to 30
more seconds to admit the restart. The restart runs on any worker that matches
the `worker_selector`.

A run that fails this way was not stopped by an operator. A DAG with
`unless-stopped` restarts after it.

Removing a queued restart from its queue, with `dequeue`, the API or the UI,
stops it as an operator would: the run stays in the history as aborted. A DAG
with `unless-stopped` starts no run after it, and a DAG with `always` restarts
after `restart_wait_sec`.

## Errors

Rules:

- A `restart_policy` that is not `no`, `always` or `unless-stopped` fails
  validation on the field `restart_policy`.
- A service DAG with a missing or non-positive `restart_wait_sec` fails
  validation on the field `restart_wait_sec`.
- A service DAG that defines a schedule fails validation on the field
  `restart_policy`.
- A service DAG that defines a DAG-level `retry_policy` fails validation on
  the field `restart_policy`.
- A base configuration that sets `restart_policy` to `always` or
  `unless-stopped` fails validation on the field `restart_policy`, and so does
  every DAG that uses it.
- The `restart` command for a service DAG exits with a non-zero status and an
  error that names `restart_policy`.

## Examples

A worker that is started again 5 seconds after each run ends:

```yaml
restart_policy: always
restart_wait_sec: 5
hist_retention_runs: 100
steps:
  - run: python3 worker.py
```

A server that stays down once an operator stops it:

```yaml
restart_policy: unless-stopped
restart_wait_sec: 10
hist_retention_runs: 100
steps:
  - run: ./serve.sh
```

A health probe that checks every 10 seconds within one run:

```yaml
restart_policy: always
restart_wait_sec: 15
hist_retention_runs: 100
steps:
  - run: ./check.sh
    repeat_policy:
      repeat: while
      exit_code: [0]
      interval_sec: 10
handler_on:
  failure:
    run: ./alert.sh
```

Rejected:

```yaml
restart_policy: always
restart_wait_sec: 5
schedule: "* * * * *"
steps:
  - run: echo tick
```

```yaml
restart_policy: always
steps:
  - run: echo tick
```

```yaml
restart_policy: sometimes
restart_wait_sec: 5
steps:
  - run: echo tick
```

## Conformance

`conformance/spec080_restart_policy/` checks the validation rules with static
fixtures and the `restart` command for a service DAG.
