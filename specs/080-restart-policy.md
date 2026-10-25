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
- the `loop_wait_sec` DAG field, which makes a run loop, and
  `loop_on_failure`, which says what the run does when a cycle fails
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

A workflow author can declare that a DAG is always on, kept
running by Dagu instead of a cron schedule, a supervisor DAG, or an external
watchdog.

## Related Specs

- Built-in run context: [Spec 017](017-built-in-run-context.md), which defines
  the `restart` trigger type
- Schedule descriptors: [Spec 063](063-schedule.md)
- Repeat policy: [Spec 075](075-repeat-policy.md), which repeats one step
  inside one run. A loop executes all the steps again; see [Loop](#loop).

## Terms

An always-on DAG is a DAG whose `restart_policy` is `always` or `unless-stopped`.

In the text of this spec, each of these words has one meaning. The names of
commands, fields, values and trigger types keep the spelling that they have in
the product, such as the `restart` command, which stops a run and starts a new
one for a DAG without a restart policy:

| Word | Meaning |
| --- | --- |
| run | One execution of a DAG, with one run ID. |
| restart | The scheduler starts a new run of an always-on DAG. |
| loop | A run executes all the steps of its DAG again, in the same run. |
| cycle | One execution of all the steps of a run that loops. |
| repeat | A step executes again in the same run, as Spec 075 defines. |
| retry | A failed attempt of a step executes again. |
| attempt | One execution of a step. |

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
  always-on DAGs.

### Restrictions

An always-on DAG:

- must have a `restart_wait_sec` of at least 1, set in its file or inherited
  from the base configuration
- must not define a schedule in its file: `schedule`, in any of its forms,
  including `start`, `stop` and `restart`. An empty `schedule` defines none.
- must not define a DAG-level `retry_policy` in its file

A schedule or a DAG-level `retry_policy` that an always-on DAG would inherit from
the base configuration is ignored.

An always-on DAG may set `queue`, `timeout_sec`, `max_clean_up_time_sec`, history
retention, `handler_on`, `preconditions`, and any other field.

### Loop

`loop_wait_sec` is a DAG-level field. It makes a run loop, as in a `while true`
loop: after the steps end, the run waits and executes the steps again. A run
then ends only when something interrupts it, and not after each cycle.

Rules:

- `loop_wait_sec` is a whole number of seconds. Zero, or an absent field, means
  the run does not loop: it ends after the steps, and the scheduler starts a
  new run `restart_wait_sec` later.
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

An always-on DAG adds a run to its history each time it is restarted. An
always-on DAG that sets neither `hist_retention_runs` nor `hist_retention_days`,
in its file or in the base configuration, keeps its latest 100 runs, where other
DAGs keep 30 days of runs.

### Restart Command

The `restart` command fails for an always-on DAG, without stopping or starting a
run: the runs of an always-on DAG are started for it. The command reads the
current definition of the DAG, so it also fails for a DAG that has no run yet,
or whose `restart_policy` was set after its latest run started.

### Run Loop

This section describes the implementation. The conformance suite checks only
the validation of `loop_wait_sec`; integration tests in `internal/intg` cover
the rest.

A cycle is one execution of the steps. A run that loops executes its first
cycle as any run does. When a cycle ends, the run waits `loop_wait_sec` seconds,
counted from the end of the cycle, and executes the steps again from a fresh
plan, in the same run.

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

### Scheduler Behavior

This section describes the implementation. The conformance suite does not
check it. Integration tests in `internal/intg/queue` and `internal/intg/distr`
cover it.

The scheduler that holds the scheduler lock keeps the always-on DAGs alive. It
checks them every second and starts a run through the queue, once
`restart_wait_sec` has passed since the previous run ended. A scheduler that
waits for the lock does nothing.

The scheduler decides from the latest run of the DAG, not from its own memory.
It starts no run while the DAG has a live process, a queued item, or an active
run. The run ID of a restart follows from the run that it replaces, so two
schedulers that decide the same restart create one run.

When the active scheduler stops or fails, the DAG keeps running. The
next scheduler continues from the latest run. It takes over when the scheduler
lock is released or stale (`scheduler.lock_stale_threshold`, 30 seconds by
default). It starts no second run while one is active.

When the process of a local run dies, the next read of the latest status marks
the run as failed. The DAG restarts after that. In distributed mode, the
coordinator marks the run as failed when the worker stops sending heartbeats
and the run lease is stale (90 seconds by default). The queue can need up to 30
more seconds to admit the restart. The restart runs on any worker that matches
the `worker_selector`.

A run that fails this way was not stopped by an operator. A DAG with
`unless-stopped` restarts after it.

## Errors

Rules:

- A `restart_policy` that is not `no`, `always` or `unless-stopped` fails
  validation on the field `restart_policy`.
- An always-on DAG with a missing or non-positive `restart_wait_sec` fails
  validation on the field `restart_wait_sec`.
- An always-on DAG that defines a schedule fails validation on the field
  `restart_policy`.
- An always-on DAG that defines a DAG-level `retry_policy` fails validation on
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
- The `restart` command for an always-on DAG exits with a non-zero status and an
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

A poller that loops: its steps execute again 5 seconds after each cycle, in one
run. The run counts only the times it is interrupted, such as a stop of Dagu or
a change of worker:

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

A consumer that keeps polling after a failed cycle, as a `while true` loop does:

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
fixtures and the `restart` command for an always-on DAG.
