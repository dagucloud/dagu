# Spec 080: DAG Restart Policy

## Status

Implemented.

This spec defines conformance behavior for the DAG-level `restart_policy`
field: its values, its validation rules, and the restarts the scheduler
performs for a DAG that sets it.

## Scope

This spec covers:

- the `restart_policy` DAG field and its accepted values
- how `restart_policy` combines with `restart_wait_sec`, `schedule` and
  `retry_policy`
- when the scheduler starts a run of a DAG that sets `restart_policy`
- the trigger type of the runs the scheduler starts

This spec does not define:

- the `restart_policy` field of the `container` block, which configures the
  Docker restart policy of a container
- `schedule.restart`, `restart_schedule`, and the `restart` command
- step-level retry or repeat behavior
- how a run is executed after it is dequeued, including worker placement
- the web UI

## Goal

A workflow author can declare that a DAG is a long-running service: Dagu keeps
one run of it alive and starts the next run when the current one ends, without
a cron schedule, a supervisor DAG, or an external watchdog.

## Related Specs

- Schedule descriptors: [Spec 063: Schedule Descriptors](063-schedule.md)
- Repeat policy: [Spec 075: Repeat Policy](075-repeat-policy.md), which repeats
  a step inside one run

## Terms

A service DAG is a DAG whose `restart_policy` is `always` or `unless-stopped`.

An active run is a run of the DAG that is queued, running, or waiting.

A terminal run is a run that has finished with the status `succeeded`,
`partially_succeeded`, `failed`, `aborted`, or `rejected`.

The restart wait is the value of `restart_wait_sec`.

## Behavior

### Field

`restart_policy` is a DAG-level field. It accepts:

| Value | Meaning |
| --- | --- |
| `no` | The scheduler does not start runs of this DAG because of this field. This is the default. |
| `always` | The scheduler starts a new run whenever the DAG has no active run. |
| `unless-stopped` | Same as `always`, except that no run is started after a run that ended with the status `aborted`. |

Rules:

- Values are case-insensitive and surrounding whitespace is ignored.
- An empty value, or an absent field, means `no`.
- A base configuration cannot set `restart_policy` to `always` or
  `unless-stopped`: it applies to every DAG, so it would turn them all into
  services. Setting it in the base configuration fails validation.

### Restrictions

A service DAG:

- must have a `restart_wait_sec` of at least 1, set in its file or inherited
  from the base configuration
- must not define a schedule in its file: `schedule`, in any of its forms,
  including `start`, `stop` and `restart`
- must not define a DAG-level `retry_policy` in its file

A schedule or a DAG-level `retry_policy` that a service DAG would inherit from
the base configuration is ignored.

A service DAG may set `queue`, `timeout_sec`, `max_clean_up_time_sec`, history
retention, `handler_on`, `preconditions`, and any other field.

### Desired State

While the scheduler is running, a service DAG that is not suspended has at most
one active run, and the scheduler starts a run when it has none.

Rules:

- A run the scheduler starts is queued through the DAG's queue, like a
  scheduled run. It is subject to the queue's concurrency limit.
- When the scheduler first sees a service DAG that has no run, it starts the
  first run at once.
- When the latest run of the DAG is terminal, the scheduler starts the next
  run once the restart wait has elapsed since that run finished. A run that
  finished before the scheduler started, for example during an outage, has
  already waited as long as it has been finished.
- The scheduler checks service DAGs at least once every second. A run starts
  at most one second after its wait has elapsed, plus the time the queue takes
  to dispatch it.
- The scheduler starts no run while the DAG has an active run. This holds after
  a scheduler restart or failover, and for runs started by another trigger,
  such as a manual start.
- Each run the scheduler starts is a new DAG run with its own ID and history
  entry. A restart is never a retry of the previous run.
- When the scheduler cannot enqueue a run, it logs the error and tries again on
  its next check.

### Stopping a Service

Rules:

- A suspended DAG gets no new run. Suspending the DAG does not stop an active
  run. The cluster-wide scheduler pause has the same effect as suspending every
  DAG.
- Resuming a suspended DAG makes the scheduler start its next run once the
  restart wait has elapsed since the latest run finished.
- With `always`, stopping a run does not stop the service, because the
  scheduler starts the next run after the restart wait. The DAG is stopped by
  suspending it and stopping its active run.
- With `unless-stopped`, a run that ended with the status `aborted`, for
  example because it was stopped, starts no new run. A new run starts when
  another trigger, such as a manual start, starts one and that run ends in a
  status other than `aborted`.

### Run Identity

Rules:

- The ID of a run the scheduler starts is derived from the DAG name and the
  run it follows, so a restart that is evaluated twice, for example around a
  failover, starts one run.
- The trigger type of the run is `restart`.
- A queued run with the trigger type `restart` is not started while its DAG is
  suspended, like a run with the trigger type `scheduler`.

### History

Rules:

- Each run the scheduler starts is a run of its own, so a service DAG that
  keeps failing fills its history with one run per restart wait. The usual
  history retention applies.
- Loading a service DAG that sets neither `hist_retention_runs` nor
  `hist_retention_days`, in its file or in the base configuration, reports a
  warning. The DAG loads and runs.

### Queues

Restarts are queued runs, so they need queues. When queues are disabled, the
scheduler starts no run for a service DAG and logs a warning.

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
- A DAG that fails validation is not loaded by the scheduler, so it is never
  restarted.

## Examples

A service that processes a queue forever and is started again 5 seconds after
it ends:

```yaml
restart_policy: always
restart_wait_sec: 5
hist_retention_runs: 100
steps:
  - run: python3 worker.py
```

A service that stays stopped once an operator stops it:

```yaml
restart_policy: unless-stopped
restart_wait_sec: 10
hist_retention_runs: 100
steps:
  - run: ./serve.sh
```

A health probe every 10 seconds, with one run for the whole period:

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
fixtures and checks the restarts of a service DAG against a running scheduler:
the first run, the restart after a failed and after a succeeded run, the single
active run, the restart wait, `unless-stopped` after a stopped run, and the
suspension of the DAG.
