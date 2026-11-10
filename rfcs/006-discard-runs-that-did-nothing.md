# RFC 006: Discard Runs That Did Nothing

- **Status:** Proposed
- **Date:** 2026-11-10
- **Affected areas:** `spec`, `ir`, `runtime`, `persis`, `service/coordinator`,
  `service/frontend` (API), `specs`, and `docs`

## Summary

Let a step say "there was nothing to do" with an exit code, and leave such a
run out of the history.

A step lists the exit codes that mean no work under `discard_run_on`. When it
exits with one of them, the run stops, runs no handlers, sends no
notifications, and is removed with its logs and work directory. Every other
run is recorded as today.

```yaml
schedule: "*/5 * * * *"
steps:
  - id: feed
    command: ~/feed/feed-run.sh   # exits 3 when the queue is empty
    discard_run_on:
      exit_code: [3]
```

## Problem statement

A common pattern is a DAG that polls: it runs every few minutes, checks for
work, and exits at once when there is none. Each check is a full run in the
history, with a status record, logs, and a work directory.

One user's setup shows the cost. A feed DAG runs every 5 minutes in five
copies. Over 14 days, 14,909 of its 15,166 runs found nothing to do and ended
in about 2 seconds. Those empty runs made most of the 835 MB of run history,
on a machine with 1 GB of RAM and a small disk. The 257 runs that did work are
hard to find among them.

The existing options do not fit:

| Option | Problem |
| --- | --- |
| `hist_retention_days` or `hist_retention_runs` | Bounds the disk, but drops runs that did work along with empty ones, and the list stays full of empty runs |
| An unmet DAG-level precondition | The run is recorded as `aborted` (Spec 023), runs the `abort` handler, and the check happens before work is claimed |
| Exiting 0 when there is nothing to do | Recorded as a success, indistinguishable from real work |
| A watched folder (`watch`, Spec 082) | Removes the poller when work arrives as files in a folder the scheduler can read. Not when the work is in a queue on another machine or in an API's state |
| Starting runs from outside (`dagu enqueue`, the REST API, RFC 004 triggers) | Right when an event exists. Polling an API without webhooks still needs a poller |

## Goals

1. A run that a step reports as having done nothing leaves no record, logs, or
   work directory.
2. Every other run is recorded exactly as today.
3. The DAG author decides what "nothing to do" means, inside the step that
   finds out, so that claiming work and reporting none is one atomic step.
4. No new run status in the API, the UI, or the Console.
5. The same outcome for local and distributed runs.

## Non-goals

- Saving the cost of starting the process. An empty run still starts and
  ends; only its record, logs, and noise go away.
- Discarding on an unmet DAG-level precondition. See
  [Open questions](#open-questions).
- Discarding child runs started by `dag.run` or `parallel`. See
  [Child runs](#child-runs).
- Keeping a count or sample of discarded runs. See
  [Observability](#observability).

## Design

### Declaring the signal

`discard_run_on` is a step field:

| Field | Type | Meaning |
| --- | --- | --- |
| `discard_run_on.exit_code` | list of integers, 1 to 255 | Exit codes of this step that mean the run did nothing |

Validation rejects:

- an exit code of 0, or outside 1 to 255;
- an exit code also listed in the same step's `continue_on.exit_code`,
  `retry_policy.exit_code`, or `repeat_policy.exit_code`, because the step
  could not both discard and continue, retry, or repeat.

A check that fits in a condition can be the first step:

```yaml
steps:
  - id: has_work
    command: test -n "$(ls -A ~/feed/queue)" || exit 3
    discard_run_on:
      exit_code: [3]
  - id: process
    depends: [has_work]
    command: ~/feed/process.sh
```

When several runs share one queue, the step that takes a job should signal,
not a separate check. Five copies can all see a non-empty queue, and four of
them then find nothing.

### Runtime behavior

When a step exits with a code in its `discard_run_on.exit_code` in a root run:

1. The step ends as `skipped`.
2. No step that has not started starts. Running steps are stopped the way
   `dagu stop` stops them, honoring `max_clean_up_time_sec`.
3. No lifecycle handler that has not run starts: not `success`, `failure`,
   `abort`, or `exit`. An `init` handler that already ran stays run.
4. No email is sent, whatever `mail_on` says.
5. The final status is written with `Ephemeral: true`. The repository already
   emits no status event for such a status (`eventingAttempt.Write` and
   `CompareAndSwapLatestAttemptStatus` in `internal/persis`). Consumers of the
   event store, including the `dag` trigger source of RFC 004 and Console
   reporting, therefore never see the run end.
6. The process that wrote the final status removes the run with
   `RemoveDAGRun`. That removes the record, the logs of the run and its child
   runs, and the work directory.

Until it ends, the run is visible as running, like any other run. It counts
toward `max_active_runs` while it runs.

A step that exits with any other code behaves as today.

### Where the removal happens

| Mode | Final status written by | Removal by |
| --- | --- | --- |
| Local | The agent, through its run-state store | The agent, after the write |
| Distributed | The coordinator, when it accepts the worker's final status | The coordinator, after the write |
| Queued run started locally | The agent | The agent |

A scheduled distributed run is persisted as `queued` before dispatch
(`internal/service/scheduler/dag_executor.go`). The discard path removes that
record with the rest.

### A crash between write and removal

If the process stops after writing the final status and before removing the
run, a record marked `Ephemeral` stays in the history store. Run lists and the
DAG's latest status skip history runs marked `Ephemeral`, and history
retention removes them like any ended run. This is the only listing change.

### Child runs

A child run started by `dag.run` or `parallel` does not honor
`discard_run_on`. The listed exit code is treated like any other exit code, so
the step fails unless `continue_on` says otherwise. The parent's record refers
to its child runs, and removing one would leave the parent pointing at nothing.

### Observability

A discarded run leaves one log line at info level in the process that removed
it: the DAG, run ID, step, and exit code. Nothing else records it. See open
question 3.

## Example: the feed DAG

The feed script already knows when it took no job, because the queue was empty
or a `STOP` file paused the feed. It exits 3 in that case instead of 0:

```yaml
schedule: "*/5 * * * *"
max_active_runs: 3
steps:
  - id: feed
    action: exec
    timeout_sec: 10800
    with:
      command: /bin/sh
      args: ["~/feed/feed-run.sh"]
    discard_run_on:
      exit_code: [3]
```

Over the same 14 days, the history would hold the 257 runs that did work and
the runs that failed, and none of the 14,909 empty ones.

If the job queue could move to the machine running the scheduler, as one file
per job in a folder, `watch` would remove the poller altogether: each file
starts one run, and `max_active_runs` caps how many run at once. The discard
signal is for the case where it cannot.

## Compatibility and rollout

The field is additive. A DAG without `discard_run_on` behaves as today.

1. **Spec, validation, and runtime for local runs**, with a new section in the
   step specification and conformance tests that run a DAG and inspect the
   history.
2. **Distributed runs**, removal in the coordinator.
3. **Listing rule** for history runs marked `Ephemeral`.
4. **Documentation**, including the polling pattern and when to prefer
   `dagu enqueue` or a trigger.

## Verification and acceptance criteria

1. A step exiting a listed code leaves no status record, no log files, no work
   directory, and no entry in the run list or the DAG's latest status.
2. The event store holds no terminal event for the run, and no email is sent.
3. Steps that had not started do not start. A running parallel step is stopped.
   The `success`, `failure`, `abort`, and `exit` handlers do not run.
4. A step exiting an unlisted code produces the same history as today.
5. Validation rejects exit code 0, codes outside 1 to 255, and codes shared
   with `continue_on`, `retry_policy`, or `repeat_policy` on the same step.
6. In a child run, the listed exit code fails the step as an ordinary exit code
   would, and both runs are recorded.
7. A distributed run that signals is removed by the coordinator, including its
   `queued` record from dispatch.
8. A record left by a crash after the final write is absent from run lists and
   removed by retention.
9. 100 scheduled runs that signal leave no history, and a run of the same DAG
   that does work is recorded normally.

## Alternatives considered

- **Start runs from outside instead of polling.** A watched folder (`watch`),
  `dagu enqueue`, the REST API, and RFC 004's webhook sources remove the empty
  runs entirely, and are the better answer when work arrives as a file or an
  event. They do not cover polling state that sends no events, such as a queue
  on another machine or an API without webhooks.
- **Retention by count or age.** Already available. It bounds the disk but
  removes runs that did work too, and the list stays full of empty runs.
- **Discard on an unmet DAG-level precondition.** Declarative, but the check
  runs before work is claimed, so concurrent consumers still produce empty
  runs, and it changes Spec 023's `aborted` outcome. It could reuse the same
  discard path later.
- **A new run status, `skipped`, kept with its own retention.** Keeps the noise
  in every list and adds a status to the API, filters, the UI, and the
  Console.
- **Start every run in the ephemeral store and promote runs that do work.** A
  run would be invisible while it works, a 32-minute encode included.
  Promotion would move records and replay events, and ephemeral cleanup runs
  only in the web server today, not in `dagu scheduler`.
- **A guard command in the scheduler.** Runs user commands inside the
  scheduler process, on the wrong host for distributed workers.

## Consequences

- The history shows runs that did something, and the DAG's latest status is
  the latest real run.
- Empty runs still cost a process start and a few seconds of CPU.
- A discard leaves no trace. A script that exits with the listed code on an
  error hides that error. The exit code should be one the script uses for
  nothing else.
- A run appears as running in the UI and then disappears.

## Open questions

1. Is `discard_run_on` the right name? It mirrors `continue_on`.
2. Should a run started by hand, from the UI or `dagu start`, be discarded too,
   or kept so that the person who started it sees an outcome?
3. Should Dagu count discarded runs per DAG, shown on the DAG page or as a
   metric, so that a misused exit code is noticed?
4. Should an unmet DAG-level precondition be able to discard a run, opt in, as
   a declarative form for checks that need no atomic claim?
5. Should child runs honor the signal, with the invoking step ending
   `skipped`, as Spec 023 does for an unmet DAG-level precondition?
