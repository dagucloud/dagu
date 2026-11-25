# RFC 027: Retry Child Steps While the Root Runs

- **Status:** Proposed
- **Date:** 2026-11-25
- **Issue:** https://github.com/dagucloud/dagu/issues/4111
- **Affected areas:** `runtime` (runner, `dag.run` executors, agent),
  `persis`, `service/coordinator`, `service/worker`, `proto`,
  `service/frontend` (API, UI), `specs`, `conformance`, and `docs`

## Summary

Let a user retry a failed step in a finished child DAG-run while its root
DAG-run is still running. The retry runs now, inside the running hierarchy,
and the root's final status reflects it.

The nearest running ancestor of the child does the work. It runs its own
`dag.run` step again for that one child and reuses the results of the other
children, which is what a retry after the root has finished does today. The
request reaches that ancestor the way a stop request reaches a run: through
the DAG-run store for local runs, and through the coordinator heartbeat for
runs on workers.

## Problem statement

A root DAG-run fans out to child runs with `dag.run`. When a step in one
child fails early on a transient error, it cannot be retried until every
other branch of the root has finished:

- The UI disables **Retry step** with "Retry unavailable while the root DAG
  run is running."
- `POST /dag-runs/{name}/{dagRunId}/retry` with `subDAGRunId` returns 409:
  "is active and cannot be retried".

The step needs minutes; the wait is often longer. In the issue, an
end-to-end test runs five modes in parallel, each fanning out to up to ten
facility runs. One facility failing in the first minutes leaves the whole
run failed until the longest branch ends, 15 to 25 minutes later.

The workarounds fall short:

| Option | Problem |
| --- | --- |
| `retry_policy` on the step | Covers known transient errors, not every failure |
| Wait for the root, then retry the step | Works, and delays the result by the longest branch |
| **Run step** from the DAG editor | Starts a separate run; the original run keeps its failure |
| Stop the root and retry it | Kills the branches that are still running |

GitLab CI retries a failed job while the rest of its pipeline runs. Airflow
clears a failed task in a running DAG run, and its scheduler runs the task
again.

## Goals

1. Retry a step in a finished child run while the root runs, from the UI and
   from the retry API with `subDAGRunId`.
2. Only that child runs again. The other children keep their results.
3. The root's final status, handlers, and notifications reflect the retry.
   Handlers run once, when the root ends.
4. Single-host, split-pod, and distributed deployments behave the same.
5. No step that already used a result runs on a stale one.
6. Retry after the root has finished is unchanged.

## Non-goals

- Retrying a step of the running root itself, or of any run that is not a
  child. See open question 1.
- Re-running steps that already used the old result. See open question 2.
- Retrying a step in a child run that is still running, waiting, or queued.
- Children of repeating steps. They cannot be retried individually today
  (`ErrRepeatingStepTarget`).
- Steps with `nodes` (RFC 013) and DAGs run by an agent step, in the first
  releases.

## Design

### Overview

```text
root (running) ── modes ──▶ M (running) ── facilities ──▶ F (failed) ──▶ P: step x failed
                                │
                                └─ owner: nearest running ancestor of F

API ──▶ request inbox ──▶ host that runs M ──▶ M's runner
        (DAG-run store)     local: root process, on its 5 s stop check
                            worker: coordinator heartbeat

M's runner decides:
  "facilities" still running              ──▶ add one attempt: retry F         [running step]
  "facilities" finished, nothing after
  it has started                          ──▶ run "facilities" again for F     [finished step]
  otherwise                               ──▶ reject with the reason
```

### Choosing the owner

The API resolves the path from the root to the target child as it does
today (`ResolveRetryPath`). Then:

1. The target child must be finished: `succeeded`, `partially_succeeded`,
   `failed`, or `aborted`. Otherwise the API returns 409, as today.
2. The owner is the nearest ancestor of the target child whose status is
   `running`. If no ancestor is running, the request is an ordinary retry of
   the root, as today.
3. The owner's step on the path is the step that leads to the target. The
   rest of the path, with the target step, is what the owner passes down.

Persisted statuses can lag. The owner checks its own state again before it
acts.

### What the owner does

The owner's runner takes the request in its graph loop, next to step
completions, so its state changes on one goroutine.

**The step is still running.** Only a `dag.run` step with `parallel` can be
running while one of its children has finished; a single `dag.run` step runs
while its one child runs. The executor adds one attempt for that child with
the rest of the path. It already does this for the `retry_policy` retries it
manages for its children (`PendingStepRetries`), locally and on workers,
through `SubDAGExecutor.Retry`. The step ends when all its attempts end, and
its status comes from each child's latest result.

- The executor keeps one error per child instead of a list it only appends
  to, so a child that succeeds on retry no longer fails the step.
- The executor rejects the attempt if the child is running or already has an
  attempt pending.

**The step has finished.** The owner runs the step again with the path, so
the step reuses every other child and retries the target, as a retry of a
finished root does. This is allowed only when no step after it has started:
every step downstream is still waiting, or was aborted because an upstream
step failed or was aborted. The runner:

1. resets the step and those aborted steps to not started;
2. gives the step its own retry path, separate from the run's;
3. recomputes the run's error from the steps that are still failed, so the
   step's earlier failure no longer decides the final status;
4. starts the step. The reset steps run or abort again when it ends, by the
   usual dependency rules.

The step's earlier execution stays in the attempt, as an execution that a
`retry_policy` retry replaces does.

**Otherwise the owner rejects the request** and names the reason:

- a step downstream has started, so it already used the result:
  `step "report" already used the result of "fan_out"; retry after the root
  DAG-run finishes`;
- the target child is running or has an attempt pending;
- the step is waiting on its own `retry_policy`;
- the run is stopping, has timed out, or is running its handlers.

### Delivering the request

The API, the scheduler, and the workers can run on different hosts. In the
Helm chart, the UI, scheduler, coordinator, and workers are separate pods.
Each run serves `/status` and `/stop` on an abstract Unix socket
(`@dagu_...`), which is reachable only from the same network namespace, so
the socket cannot carry the request. The request takes the path a stop
request takes.

**The inbox.** The API writes a request record to a small collection in the
DAG-run store, the store that holds the abort flag a stop writes.

| Field | Meaning |
| --- | --- |
| `id` | Request ID |
| `root`, `owner` | Root and owner DAG-run references |
| `owner_worker` | Worker ID from the owner's status; empty for a local owner |
| `step` | The owner's step on the path |
| `path` | The hops below the owner and the target step |
| `include_downstream` | As in today's retry |
| `actor` | Who asked |
| `created_at` | When the API wrote the record |
| `state`, `reason` | See below |

Each change of state is a compare-and-swap:

```text
pending ──▶ claimed ──▶ accepted
   │           └──────▶ rejected   (by the owner)
   └──────────────────▶ rejected   (API timeout, owner finished, older worker)
```

The host that runs the owner claims a record before the owner's runner
decides, and the owner acts only on a record it claimed. The API can reject
only a record that is still `pending`. So a retry never runs after the API
has answered no.

**Local owners.** A local root process runs its children in its own
process, except those dispatched to workers. On the 5-second tick that
already checks for a stop request, a root whose DAG has a `dag.run` step
lists the inbox for records with its root and no `owner_worker`. It claims
each one and hands it to the agent that runs the owner: its own agent or one
of its children in process. This works from any pod that shares the store,
as stop does.

**Owners on workers.** The coordinator lists the inbox at most once a
second, and all heartbeats share that listing. In the heartbeat response to
the worker named by `owner_worker`, it adds that worker's records next to
`cancelled_runs`. The worker claims each record through a new coordinator
RPC, hands it to the agent that runs the owner (the task's agent or one of
its children in process), and reports the decision through the same RPC. A
coordinator that sees from the heartbeat that the worker predates this
feature rejects the record at once.

**The answer.** The API reads the record every 250 ms for up to 10 seconds:

- `accepted`: 200.
- `rejected`: 409 with the reason.
- Still `pending` at 10 seconds: the API rejects the record and returns 409:
  `the DAG-run did not pick up the retry; it may run an older version`.
- `claimed` with no decision 5 seconds later: 409: `the DAG-run stopped
  before it answered`.
- The owner finished before it decided: the API chooses the owner again,
  once.

The API deletes the record once it has answered. Whoever lists the inbox
deletes records older than 10 minutes.

### After the owner accepts

- The target child gets a new attempt, recorded with the actor who asked.
  The owner and the root keep their attempts.
- The owner's step shows as running again, and the root stays `running`.
- When the root ends, its final status, its `success`, `failure`, and `exit`
  handlers, and its notifications come from the final step states, once. If
  the retry succeeds and nothing else failed, the root's `failure` handler
  does not run.
- The audit log records `dag_retry` with `live: true`, the owner, and the
  outcome.

## API and UI

- **API.** Same endpoint and body: `POST /dag-runs/{name}/{dagRunId}/retry`
  with `subDAGRunId` and `stepName`. Where it returned 409 because the root
  was active, it now returns 200 when the owner accepts, or 409 with the
  owner's reason. The schema does not change.
- **UI.** In a finished child run, **Retry step** is enabled while the root
  runs. Its tooltip says the retry runs inside the running root. A 409 shows
  its reason. In a child run that is running, waiting, or queued, the button
  stays disabled, as today.

## Performance

- **Local runs.** One listing of the inbox every 5 seconds per running root
  whose DAG has a `dag.run` step, on the existing stop-check tick. No new
  ticker. Other runs read nothing new. The check stops when the run ends.
- **Coordinator.** One inbox listing per second, shared by every worker's
  heartbeat. A heartbeat response grows only while a request for that worker
  is pending.
- **API.** Per click, the statuses along the path, as today, and up to 40
  reads of one record.
- **Growth.** None with the number of DAGs or stored runs. The inbox holds
  only requests from the last few seconds, each under 1 KB.
- **Upgrade.** No migration, index, or change to persisted run status.

## Security

The API checks the same permission as today's retry: execute on the root's
workspace. The record carries the actor. Owners trust the store as they
trust the abort flag. Nothing new is exposed.

## Compatibility and rollout

| Release | Ships |
| --- | --- |
| First | The inbox, local pickup, the coordinator relay, the running-step case, the API, and the UI. |
| Second | The finished-step case. Until it ships, such a request is rejected: `step "fan_out" has finished; retry after the root DAG-run finishes`. |

- An older root process never lists the inbox. Its requests are rejected
  after 10 seconds.
- An older worker is rejected at once. An older coordinator never relays
  requests, so they time out.
- Proto: one field in both heartbeat responses and one unary RPC. Older
  peers ignore the field.
- Downgrade is safe. Older binaries ignore the inbox, and its records
  expire.
- Retry after the root has finished is unchanged.

## Verification and acceptance criteria

1. Nested fan-out: a root `parallel` step over modes, each mode a `parallel`
   step over facilities. A facility child fails while its siblings run.
   Retrying its step is accepted. The facility runs again inside the mode's
   running step, the mode and the root get no new attempt, and the root ends
   `succeeded` when the retry succeeds.
2. The same with every run on workers (`default_execution_mode:
   distributed`), answered within 10 seconds.
3. The same with the root started by the scheduler in another pod than the
   API.
4. Retries for two different children of one running `parallel` step are
   both accepted. A second retry of the same child while it runs is
   rejected.
5. A `parallel` step whose retried child succeeds ends `succeeded`.
6. The issue's example: `fan_out` has finished and `long_branch` is running.
   In the first release the retry is rejected and names `fan_out`. In the
   second it is accepted, children `a` and `c` are reused, and the root's
   status follows the new result of `fan_out`.
7. With a step `report` that depends on `fan_out` and `long_branch`, a
   successful retry of a step in a child of `fan_out` lets `report` run
   after `long_branch` ends.
8. With a `continue_on: failure` dependent of `fan_out` that already ran,
   the retry is rejected and names that step.
9. A retry that makes the root succeed runs its `success` handler and not
   its `failure` handler.
10. A root from an older binary is rejected within 10 seconds. An owner on
    an older worker is rejected at once.
11. A retry in a child run that is still running is rejected, as today.
12. After the root finishes, retry behaves as today.
13. The audit log and the child's new attempt record the actor.

## Alternatives considered

- **The owner's Unix socket.** Each run already serves `/status` and `/stop`
  on one. Abstract sockets reach only the same network namespace: not
  workers, and not the scheduler pod from the API pod in the Helm chart.
- **Each running agent checks its own request file.** Runs with no `dag.run`
  step pay for a check they can never use, and agents on workers do not
  read the shared store.
- **Retry the child on its own now and update the root later.** The step
  runs twice, the root runs its `failure` handler before the fix lands, and
  the root needs a way to notice the new result.
- **Stop the root and retry it with the path.** Kills the branches that are
  still running.
- **Hold `dag.run` steps open until the root's other branches end.** Delays
  every dependent, including those under `continue_on`.
- **Reopen any finished step and re-run the steps after it,** as Airflow's
  clear with downstream does. Re-runs steps that already used the old
  result, and resets much more runner state. Requiring that nothing
  downstream has started keeps the finished-step case small.

## Consequences

- A running DAG-run takes a second kind of request from outside, after stop.
  The runner's graph loop gains an input.
- A `dag.run` step can execute twice in one attempt, in the finished-step
  case.
- A child's attempt count can grow while its root has one attempt.
- Pickup adds latency: up to 5 seconds for a local owner, and 1 to 2 seconds
  for an owner on a worker.

## Open questions

1. Should the finished-step case also cover a failed step of the running
   root itself, with no child? The mechanism is the same, but the UI and the
   API would change for every DAG.
2. Should a retry be allowed to re-run dependents that already used the
   result, after a confirmation?
3. Steps with `nodes` (RFC 013) track nodes as well as child runs. Should a
   later release cover them?
4. Should local pickup be faster than the 5-second stop check?
