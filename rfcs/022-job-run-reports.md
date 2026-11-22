# RFC 022: Job Run Reports to Dagu Console

- **Status:** Draft
- **Date:** 2026-11-23
- **Affected areas:** `cmd` (`start`, `exec`, `retry`, `restart`),
  `runtime/runstate`, `cloudreport`, `license`, `cmn/config`, and `docs`

## Summary

Let a DAG run on a machine that exists only for that run, such as a
Kubernetes Job, a CI step, or a Cloud Run job, report itself to Dagu Console
with a run key. When `DAGU_CLOUD_RUN_KEY` is set, `dagu start` and the
commands like it send Console the run when it starts, every minute while it
runs, when its status changes, and when it finishes. The machine never
becomes a server: it takes no license, no server slot, and no paid feature.

This is the sender that Console's RFC 020 ("Runs from Short-Lived Machines",
`dagucloud/cloud`) waits for. Console already takes these reports at
`POST /api/v1/runs/report`, stores the finished runs under the key's name,
counts them against the workspace's monthly allowance, and alerts on
failures.

## Problem statement

- **A one-off run reports nothing.** Only the server process starts the
  reporter (`internal/cmd/server.go`). `dagu start job.yaml` in a container
  sends Console nothing, so its failures and history are lost when the
  container ends.
- **Running the server in a throwaway container breaks the server count.**
  Each container gets a new data directory and activates as a new server,
  which holds its license slot until released. Machines meant to disappear
  fill the plan and raise "stopped reporting" alerts.
- **Console is the only record that survives such a run.** The machine's own
  run history goes with it.

## Goals

1. A top-level run on a machine with a run key reaches Console when it
   finishes, failed or not, unless the machine has no route to Console.
2. Console sees the run while it runs: when it starts, each minute, and at
   once when its status changes, such as to waiting for an approval.
3. Reporting never changes the run: not its status, its exit code, or its
   start time, and it adds at most a few seconds to the process's exit.
4. A machine that is a server, with an online license, reports through the
   server reporter only, never twice.

## Non-goals

- Health, queues, workers, or schedules of the machine. A job runner reports
  its run only.
- Sub-DAG runs. A step that runs a sub-DAG reports as one step of its
  parent, as the server reporter does.
- Keeping unsent reports on disk to send later. The machine is going away;
  a finish that cannot be sent within its budget is lost, and Console's
  deadlines catch a daily job that never reported.
- Logs, outputs, and the DAG file. The run's steps are sent with their
  status and times, as the server reporter sends them, not their output.
- Workflow tests (RFC 020, `dagu test`) and dry runs. They run nothing that
  happened in production.

## Guarantees

Promised:

- **A finished run is sent once it ends, retried for up to 10 seconds.**
  Console counts it once however many times it arrives.
- **Reporting never fails a run.** Every error stays in the sender. The run's
  status and exit code are what they would be without a run key.
- **The run starts at once.** The start report goes out in the background.
- **A run key alone writes runs.** The sender sends the key to Console and to
  no one else, and logs neither it nor any part of it.

Not promised:

- **Runs whose reports never arrive.** A machine with no route to Console, or
  one killed before its finish left, reaches Console as far as it got.
  Console marks a run whose reports stop as lost (RFC 020, phase 2).
- **Progress at an exact pace.** Progress reports go out about each minute,
  later while one is being sent.

## Design

```text
dagu start job.yaml                                   Dagu Console
  |                                                   ------------
  | agent ── RecordStatus ──> job reporter ── one at a time ──> POST /api/v1/runs/report
  |                            |  start: in the background       Authorization: Bearer drk_…
  |                            |  status change: at once
  |                            |  each next_report_seconds: progress
  |                            v  terminal status: the finish, retried ≤ 10 s
  | attempt.Close ─── waits for the finish, at most its budget
  v
exits with the run's own status
```

### Setting

| Key | Environment | Default | Meaning |
| --- | --- | --- | --- |
| `cloud.run_key` | `DAGU_CLOUD_RUN_KEY` | empty | The run key the workspace's owner made in Console. Empty turns job reports off. |
| `license.cloud_url` | `DAGU_LICENSE_CLOUD_URL` | `https://console.dagu.sh` | Console's address, the one the license client uses (`internal/license/client.go:19`). |
| `cloud.report` | `DAGU_CLOUD_REPORT` | `errors` with a run key | `runs` sends runs without error messages; `off` and `health` send nothing. |

- A key that does not start with `drk_` is a configuration error: the run
  starts, the sender stays off, and one warning says so. A run key is never
  a license key; `DAGU_LICENSE_KEY` keeps its meaning.
- With a run key and no `cloud.report`, the sender reports at `errors`: a
  machine that goes away leaves its error messages nowhere else. They are
  masked as the server reporter masks them, and a workspace can still keep
  none (`dagu_errors` in Console).

### Where it runs

The sender wraps the run-state store the agent writes its status through
(`runstate.Store`, `internal/runtime/runstate/runstate.go:14`), in the one
place each command builds its agent:

- `withDAGRunAgent` (`internal/cmd/start.go:706`), which `start` and `exec`
  use, and the agents `retry` (`internal/cmd/retry.go:751`) and `restart`
  (`internal/cmd/restart.go:233`) build. One helper wraps the store for all
  of them.

It is on only when all of these hold:

- A run key is set, and `cloud.report` sends runs.
- The run is top-level: no parent run.
- The run executes here, not on a distributed worker (`workerID` is
  `local`). A worker's runs belong to the server that dispatched them.
- The data directory has no online license: no stored activation that
  verifies (`license.StoredCloudCredentials`,
  `internal/license/manager.go:216`). A server reports its own runs; a run
  key on a licensed server does nothing, and says so once at debug level.
- The command is not a workflow test (`internal/cmd/case_run.go`) or a dry
  run.

A Dagu scheduler starts each run as a `dagu start` child with its own
environment, so a scheduler without an online license and with a run key
reports every run it starts as a job run. RFC 020 allows this: such a
machine is measured as job runs and gets nothing a server gets.

### What it sends

One run per request, at most 64 KiB, as RFC 020 sets:

```json
{"protocol": 1, "active": { … }}
{"protocol": 1, "finished": { … }}
```

- `active` is the run in progress, as the server reporter's `activeRun`
  (`internal/cloudreport/active.go:58`), with its steps.
- `finished` is the run that ended, as the server reporter's `finishedRun`
  (`internal/cloudreport/events.go:131`): its status, trigger, times, failed
  steps, errors at the `errors` level, and steps.
- Both are built by the functions the server reporter uses: `newActiveRun`,
  `failedSteps`, `runErrors`, and `runSteps`. They move into a package both
  use, so a job run and a server run read alike in Console.
- The workflow is the DAG name, as for a server. The same DAG under two run
  keys is two workflows in Console.

### When it sends

The wrapped attempt forwards every status the agent records
(`Attempt.RecordStatus`, `internal/runtime/runstate/runstate.go:33`) to the
reporter, which never blocks the agent.

| Moment | Report |
| --- | --- |
| The first status of the attempt | `active`, in the background |
| The run's status changes, such as to waiting or running again | `active`, at once |
| `next_report_seconds` since the last report, with up to 10% jitter | `active`, with the latest status |
| A terminal status: succeeded, partially succeeded, failed, aborted, or rejected | `finished` |

- A local run writes its terminal status once, at the very end, after its
  handlers (`shouldDelayTerminalStatus`, `internal/runtime/agent/agent.go:1541`),
  so the finish carries the run's final word.
- A run short enough to end before its first report sends only the finish.
- The interval starts at 60 seconds. Each answer's `next_report_seconds`
  replaces it, kept between 30 seconds and 5 minutes.

### How it sends

- **One report at a time.** A report waiting while another is in flight is
  replaced by a newer one, so progress never piles up and never trails the
  finish. The finish waits for nothing but the report in flight.
- **The finish is retried for up to 10 seconds in all,** with backoff from
  500 ms. On `429` and `503` it waits for `Retry-After` within that budget.
  It is not retried on `400`, `401`, or `413`.
- **Progress is not retried.** The next report replaces it.
- **`401`** means the key is unknown or revoked: the sender stops for this
  run and logs one warning.
- **Each request times out after 5 seconds,** and the whole report path
  after 10.
- **The process waits for the finish.** The wrapped attempt's `Close` waits
  for the finish to be sent or its budget to end, then returns. A Kubernetes
  pod stopped with SIGTERM aborts the run, writes its terminal status, and
  sends the finish within the default 30-second grace period.
- The request carries `User-Agent: dagu-oss/<version>` and
  `Authorization: Bearer <run key>`, and nothing else that names the machine.

## Failure behavior

| Case | What happens |
| --- | --- |
| No route to Console | Each report fails; the finish is retried for 10 seconds, then dropped. The run ends as it would. |
| Console answers `503` because the workspace is busy | The finish waits `Retry-After` and tries again within its budget. |
| The key is revoked mid-run | The next report gets `401`; the sender stops. |
| The process is killed with SIGKILL | Nothing more is sent. Console sees the run as it last reported. |
| A run key on a licensed server | The sender stays off; the server reporter reports the run. |
| A malformed run key | The sender stays off and warns once; the run starts. |

The sender logs to the run's log only once per run, and never the key. Its
other messages go to the process's own log at debug level.

## Performance

- A 5-minute run sends about seven requests: start, about five progress
  reports, and the finish. Each is one small JSON body.
- No disk is written and no file is read beyond what the run already does.
- The start report is in the background, so the run starts at once. Only the
  finish adds to the process's exit, at most 10 seconds and usually one round
  trip.

## Compatibility and rollout

- Without a run key nothing changes.
- Console takes these reports behind `monitor.job_runs` (RFC 020, phase 1).
  Console phase 1 keeps finishes; it takes progress reports and keeps them
  from phase 2. The sender sends both from the start, so it needs no change
  when Console adds phase 2.
- Steps ride on the reports already: Console stores them as it does a
  server's (RFC 021 in `dagucloud/cloud`).

Order:

1. This change, released.
2. Console's run key panel names the first Dagu version with the sender, and
   `monitor.job_runs` is turned on in production.
3. The docs page: monitoring Kubernetes Jobs and CI runs, with the Secret,
   `envFrom`, and CI snippets Console's panel shows.

## Verification and acceptance criteria

- With a run key, `dagu start` sends `active` when the run starts and
  `finished` when it ends, with the run's status, times, failed steps,
  errors, and steps; a fake Console receives both, in that order.
- A run that waits for an approval sends `active` with `waiting` at once.
- A run longer than the interval sends progress about each interval; the
  answer's `next_report_seconds` changes the next interval, kept between 30
  seconds and 5 minutes.
- Progress never arrives after the finish, even when the finish is sent
  while a progress report is in flight.
- The finish is retried on `503` and `429` after `Retry-After`, and given up
  after 10 seconds; `401` stops the sender for the run.
- With Console unreachable, the run's status and exit code are what they are
  without a key, and the process exits within 10 seconds of the run's end.
- SIGTERM aborts the run and its finish still reaches the fake Console.
- No report is sent: without a key, with a key not starting with `drk_`, at
  `cloud.report: off`, for a sub-DAG run, on a distributed worker, with an
  online license in the data directory, for a workflow test, or for a dry
  run.
- At `cloud.report: runs` the finish carries no errors.
- The key appears in no log line.
- `retry` and `restart` report the new attempt as `start` does.
- End to end against a local Console with `monitor.job_runs` on: a run key
  made on the Servers page, a failed `dagu start` in a container, and the
  run, its alert, and the month's count shown in Console.

## Alternatives considered

- **Run the server in each container.** That is the problem this replaces:
  every container activates as a server.
- **Read the event store, as the server reporter does.** The server reporter
  polls a shared event store because it is a separate process. A one-off
  command already holds every status its agent writes, so wrapping the
  run-state store sees each change at once, with no polling and no files.
- **Keep unsent reports on disk.** The disk goes with the machine. A
  bounded retry of the finish is the most that helps.
- **Batch several runs per request.** A job's process has one top-level run.

## Open questions

- Whether `cloud.report` should default to `runs` instead of `errors` for job
  runs, so error messages leave the machine only when someone chooses that.
- Whether the run's log should name the run key's workspace once, so a
  person reading the log knows where its report went.
