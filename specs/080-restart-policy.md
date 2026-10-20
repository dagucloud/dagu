# Spec 080: DAG Restart Policy

## Status

Implemented.

This spec defines conformance behavior for the DAG-level `restart_policy`
field: its values, how it combines with other DAG fields, and the validation
errors it produces.

## Scope

This spec covers:

- the `restart_policy` DAG field and its accepted values
- how `restart_policy` combines with `restart_wait_sec`, `schedule`,
  `retry_policy`, history retention, and the base configuration
- the validation errors of a DAG that sets `restart_policy`
- the `loop_wait_sec` DAG field, which makes one run repeat its steps, and
  `loop_on_failure`, which says what the run does when a cycle fails
- the `restart` command for a DAG that sets `restart_policy`
- runs started by hand next to an active run of a DAG that sets `restart_policy`

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

### Loop

`loop_wait_sec` is a DAG-level field. It makes one run repeat its steps, as in
a `while true` loop, so that a run ends only when something interrupts it and
not after each pass.

Rules:

- `loop_wait_sec` is a whole number of seconds. Zero, or an absent field, means
  the run does not loop: it ends after one pass, and the scheduler starts a new
  run `restart_wait_sec` later.
- A positive `loop_wait_sec` requires `restart_policy` to be `always` or
  `unless-stopped`.
- A negative `loop_wait_sec` is an error.
- A base configuration cannot set `loop_wait_sec`: it applies to every DAG.

`loop_on_failure` is a DAG-level field that says what a run that loops does
when a cycle fails:

| Value | Meaning |
| --- | --- |
| `restart` | The run ends with the status of the failed cycle. `restart_policy` starts a new run after `restart_wait_sec`, and the new run counts the interruption. This is the default. |
| `continue` | The run goes on with the next cycle after `loop_wait_sec`, as a `while true` loop does. |

Rules:

- Values are case-insensitive and surrounding whitespace is ignored.
- An empty value, or an absent field, means `restart`.
- `loop_on_failure` requires a positive `loop_wait_sec`.
- A base configuration cannot set `loop_on_failure`.

### History Retention

A service DAG adds a run to its history each time it is restarted, so days of
runs do not bound its history:

- a `hist_retention_days` in its file applies
- a `hist_retention_days` in the base configuration does not apply
- a `hist_retention_runs` in its file or in the base configuration applies

A service DAG left without a retention keeps its latest 100 runs, where other
DAGs keep 30 days of runs.

### Restart Command

The `restart` command fails for a service DAG, without stopping or starting a
run: the runs of a service DAG are started for it. The command reads the
current definition of the DAG, so it also fails for a DAG that has no run yet,
or whose `restart_policy` was set after its latest run started.

### Runs Started by Hand

A service DAG has one run at a time. While its latest run is running, queued or
waiting, or has not started for up to 5 minutes, the `start`, `enqueue` and
`retry` commands, their API requests and MCP actions, and webhooks fail without
starting a run. With no active run they work, which is how a stopped service
comes back.

The current definition of the DAG decides whether it is a service, also for a run
started from a run snapshot or from a spec in a request. The run that the
scheduler dispatches from the queue is refused only when another run of the DAG
has a live process on the same host; it then ends as failed. A run started under
another name with `--name` is a run of another DAG.

### Run Loop

This section describes the implementation. The conformance suite checks only
the validation of `loop_wait_sec`; integration tests in `internal/intg` cover
the rest.

A pass of the steps is a cycle. A run that loops runs its first cycle as any
run does. When a cycle ends, the run waits `loop_wait_sec` seconds, counted from
the end of the cycle, and runs the steps again from a fresh plan, in the same
run.

- A cycle that fails ends the run with the status of that cycle, and
  `restart_policy` starts a new run `restart_wait_sec` later. The run counts the
  cycle in `failedCycles`. With `loop_on_failure: continue` the run goes on with
  the next cycle instead, as `while true; do ./job; sleep 5; done` does.
- Lifecycle handlers (the `handler_on` keys `init`, `success`, `failure`,
  `abort` and `exit`) and `timeout_sec` apply to each cycle.
- The loop ends when a stop is requested, even during the wait, when a cycle is
  aborted, when a cycle waits for approval, or when a cycle fails and
  `loop_on_failure` is `restart`. The run then ends with the status of the last
  cycle, and with `aborted` after a stop.
- A cycle that times out (`timeout_sec`) fails. It is a failed cycle like any
  other: with `loop_on_failure: continue` the run goes on with the next cycle.
- During the wait between two cycles the run reports `running` and has no end
  time. `startedAt` is the start of the first cycle. The status carries `cycle`,
  the number of the cycle the run is in, and `failedCycles`.
- When a cycle starts, the step and handler logs of the cycle before it are
  removed, except those of the latest failed cycle. A run that loops for months
  does not fill the disk, and the failure that matters stays readable.

A run that loops lives as long as nothing interrupts it, so what it stores must
stay bounded:

- The step and handler logs of the cycles before are removed, as said above.
- On the local node, Dagu rewrites the status file to the latest status at the
  end of each cycle, so the file holds a few lines and not one for each update.
- For a run on a worker, the coordinator appends each status to the file and
  nothing compacts it, so the file grows with each cycle. A DAG that sets both
  `loop_wait_sec` and `worker_selector` loads with a warning.
- The log of the run itself is not rotated. Each cycle adds the lines that the
  runner writes for its steps, and a cycle that succeeds adds no line of its own.
  A longer `loop_wait_sec` and fewer steps keep this log smaller.

When the run is lost, because the worker stops, Dagu is stopped, or the process
dies, the run ends and `restart_policy` starts a new run. Runs therefore count
interruptions, and a step that takes six seconds and a `loop_wait_sec` of five
make one run with a cycle every eleven seconds.

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
- A positive `loop_wait_sec` without `restart_policy` set to `always` or
  `unless-stopped` fails validation on the field `loop_wait_sec`.
- A negative `loop_wait_sec` fails validation on the field `loop_wait_sec`.
- A `loop_on_failure` that is not `restart` or `continue` fails validation on
  the field `loop_on_failure`.
- A `loop_on_failure` without a positive `loop_wait_sec` fails validation on the
  field `loop_on_failure`.
- A base configuration that sets `loop_on_failure` fails validation on the field
  `loop_on_failure`, and so does every DAG that uses it.
- A base configuration that sets `loop_wait_sec` fails validation on the field
  `loop_wait_sec`, and so does every DAG that uses it.
- The `restart` command for a service DAG exits with a non-zero status and an
  error that names `restart_policy`.
- A `start`, `enqueue` or `retry` command next to an active run of a service DAG
  exits with a non-zero status and an error that names `restart_policy`. The API
  responds with the status 409.

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

A poller whose steps run again every 5 seconds in one run. The run counts
only the times it is interrupted, such as a stop of Dagu or a change of worker:

```yaml
restart_policy: always
restart_wait_sec: 5
loop_wait_sec: 5
hist_retention_runs: 100
steps:
  - name: fetch
    run: ./fetch.sh
  - name: store
    run: ./store.sh
    depends: [fetch]
```

A consumer that keeps polling after a failed pass, as a `while true` loop does:

```yaml
restart_policy: always
restart_wait_sec: 5
loop_wait_sec: 10
loop_on_failure: continue
hist_retention_runs: 100
steps:
  - run: ./poll.sh
```

Rejected:

```yaml
restart_policy: always
restart_wait_sec: 5
loop_on_failure: continue
steps:
  - run: echo tick
```

```yaml
loop_wait_sec: 5
steps:
  - run: echo tick
```

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
fixtures, the `restart` command for a service DAG, and the commands that start a
run next to its active run.
