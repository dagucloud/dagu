# RFC 022: Job Run Reports to Dagu Console

- **Status:** Implemented
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
| `cloud.report` | `DAGU_CLOUD_REPORT` | `runs` with a run key | `errors` also sends error messages; `off` and `health` send nothing. |

- A key that does not start with `drk_` is a configuration error: the run
  starts, the sender stays off, and one warning says so. A run key is never
  a license key; `DAGU_LICENSE_KEY` keeps its meaning.
- With a run key and no `cloud.report`, the sender reports at `runs`, the
  level Dagu's monitoring dialog recommends for servers, and keeps error
  output on the machine. `cloud.report: errors` also sends error messages,
  which a machine that goes away leaves nowhere else; without them a
  failure alert says only which step failed. They are masked as the server
  reporter masks them: known secret values and common token formats are
  replaced. Masking cannot catch everything a program prints, such as
  hostnames or row contents, and a workspace can keep none (`dagu_errors`
  in Console).

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
- The command is not a workflow test (`internal/cmd/case_run.go`) or a dry
  run.
- No server reports this data directory's DAG runs: no live process holds
  the server reporter's lease, `<data>/cloud/report-state.json.lock`.

The machine's license plays no part: a run key is how a machine without a
server reports, and an activation left in an image or a volume can outlive
the server it belonged to.

A Dagu scheduler starts each run as a `dagu start` child with its own
environment, so a scheduler with a run key reports every run it starts as
a job run. RFC 020 allows this: such a machine is measured as job runs and
gets nothing a server gets. A server that reports runs sends every run of
its data directory, whichever process started it, and holds its lease while
it does. A run that finds the lease held sends no job report, so Console
counts the run once.

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
- Both also carry where the run ran, below.

### Where the run ran

A job's machine and its logs are gone once the run ends, so the report says
where the run happened, for a person to find its logs and what started it:

```json
"platform": "github_actions",
"platform_url": "https://github.com/acme/etl/actions/runs/9876543210/attempts/2",
"host": "etl-28911-x7k2p"
```

- **`platform`** is the system the machine says it runs in, from its
  environment, the first that matches:

  | Platform | Detected by | `platform_url` |
  | --- | --- | --- |
  | `github_actions` | `GITHUB_ACTIONS=true` | `$GITHUB_SERVER_URL/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID/attempts/$GITHUB_RUN_ATTEMPT` |
  | `gitlab_ci` | `GITLAB_CI=true` | `$CI_JOB_URL` |
  | `buildkite` | `BUILDKITE=true` | `$BUILDKITE_BUILD_URL` |
  | `circleci` | `CIRCLECI=true` | `$CIRCLE_BUILD_URL` |
  | `azure_pipelines` | `TF_BUILD=True` | `$SYSTEM_COLLECTIONURI$SYSTEM_TEAMPROJECT/_build/results?buildId=$BUILD_BUILDID` |
  | `jenkins` | `JENKINS_URL` set | `$BUILD_URL` |
  | `cloud_run` | `CLOUD_RUN_JOB` set | none |
  | `aws_batch` | `AWS_BATCH_JOB_ID` set | none |
  | `kubernetes` | `KUBERNETES_SERVICE_HOST` set | none |

  CI systems come before Kubernetes, since many CI runners run on it. A
  machine none of them matches sends none.
- **`platform_url`** links to the CI job or run, where its full log is. It is
  sent only as an `https` URL of at most 2048 bytes.
- **`host`** is the machine's hostname, which on Kubernetes is the pod's
  name, cut to 253 bytes.
- **What started the run.** `dagu start` records a run started from the
  command line as `manual`, which on a job's machine is rarely a person. A
  job report sends `trigger_type` empty unless `--trigger-type` was passed,
  and Console shows the platform's name, such as "GitHub Actions" or
  "Kubernetes", in its place. The run's own history on the machine keeps
  what `dagu start` recorded.
- These go only to the workspace's own Console, never into the run's log.

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
  sends the finish within the default 30-second grace period. Without a
  finish, as when the run stops to wait for an approval, `Close` waits only
  for a status change not yet sent: one request of at most 5 seconds.
- The request carries `User-Agent: dagu-oss/<version>` and
  `Authorization: Bearer <run key>`. Console shows the version beside the
  key, so an owner sees which Dagu their machines run.
- **Proxies and certificates.** The sender uses Go's default transport, as
  the license client and the server reporter do: it honors `HTTPS_PROXY`,
  `HTTP_PROXY`, and `NO_PROXY`, and trusts the system's certificates and
  those `SSL_CERT_FILE` or `SSL_CERT_DIR` names. A cluster that reaches the
  internet through a proxy needs nothing more. The only host it calls is
  Console's, `console.dagu.sh:443` by default.

## Failure behavior

| Case | What happens |
| --- | --- |
| No route to Console | Each report fails; the finish is retried for 10 seconds, then dropped. The run ends as it would. |
| Console answers `503` because the workspace is busy | The finish waits `Retry-After` and tries again within its budget. |
| The key is revoked mid-run | The next report gets `401`; the sender stops. |
| The process is killed with SIGKILL | Nothing more is sent. Console sees the run as it last reported. |
| A run key where a server reports this data directory's runs | The run sends no job report; the server reports it. |
| A malformed run key | The sender stays off and warns once; the run starts. |

### What the run prints

What a job's process prints is what people read in CI and in
`kubectl logs`, and those logs are often public, as in an open-source
repository's CI. So the sender names neither the key nor the workspace:

- When the sender turns on for a run, one line at info level:
  `Reporting this run to Dagu Console`.
- One warning when reporting stops short: the key was refused (`401`), or the
  finish could not be sent within its budget. It says which, and what to
  check: the key, or the route to Console.
- One warning for a malformed key, as above.
- One line at info level when Console counted the finish but does not list
  it because the workspace used this month's job runs: `Dagu Console counted
  this run but does not list it: this month's job runs are used.` Console's
  answer says so (`"listed": false, "why": "allowance"`); a finish sent again
  says nothing.
- Everything else, such as a progress report that failed, at debug level.

Console's answer carries no workspace name, so the sender has none to print.

## Performance

- A 5-minute run sends about seven requests: start, about five progress
  reports, and the finish. Each is one small JSON body.
- No disk is written. Beyond what the run already does, the only read is
  one stat of the server reporter's lease.
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

- Console needs a few additions before it turns job runs on (RFC 020): to
  take and show `platform`, `platform_url`, and `host`, to answer `listed`
  and `why`, to show the Dagu version beside each key, and to tell an owner
  whose key was never used which Dagu version it needs.

Order:

1. This change, released.
2. Console's additions, its run key panel and never-used keys naming the
   first Dagu version with the sender, and `monitor.job_runs` turned on in
   production.
3. The docs page: monitoring Kubernetes Jobs and CI runs, with the Secret,
   `envFrom`, and CI snippets Console's panel shows; the host to allow
   through a firewall; and, for a Kubernetes Job or a CI run that retries,
   passing `--run-id` its Job or run name, so its retries show in Console as
   attempts of one run instead of unrelated runs.

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
  `cloud.report: off`, for a sub-DAG run, on a distributed worker, for a
  workflow test, or for a dry run.
- A run key reports with an online license in the data directory.
- At `cloud.report: runs` the finish carries no errors.
- The key and the workspace appear in no log line.
- In GitHub Actions the reports carry `platform: github_actions` and a link to
  the run's attempt; in a Kubernetes pod, `platform: kubernetes` and the
  pod's name as `host`; in GitLab CI on Kubernetes, `gitlab_ci`; on a laptop,
  no platform. A `platform_url` that is not `https` is left out.
- Without `--trigger-type` the reports carry no trigger; with
  `--trigger-type scheduler`, they carry `scheduler`.
- Behind an `HTTPS_PROXY`, the reports go through it.
- A finish Console counted but does not list prints its one line; the same
  finish sent again prints nothing.
- A run with a key prints `Reporting this run to Dagu Console` once; a revoked
  key, and a finish that cannot be sent, each print one warning; a failed
  progress report prints nothing above debug level.
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

## Decisions

- **Job runs report at `runs` by default**, as servers are recommended
  to, and keep error output on the machine. `cloud.report: errors` sends
  error messages too, so failure alerts say why a run failed after its
  machine is gone.
- **The run prints one neutral line and warnings,** naming neither the key
  nor the workspace, since job logs are often public.
- **A job run names its platform, not a trigger.** Without `--trigger-type`,
  Console shows the platform it ran in, such as "GitHub Actions" or
  "Kubernetes", where a server's run shows "Manual" or "Scheduler".
