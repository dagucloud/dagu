# RFC 005: On-Demand Workers

- **Status:** Proposed
- **Date:** 2026-11-06
- **Affected areas:** `service/worker`, `service/launcher` (new), `cmd`,
  `cmn/config`, `specs`, and `docs`

## Summary

Let a deployment run each distributed task in a container that exists only for
that task, without giving Dagu access to the container daemon.

A new process, `dagu launcher`, holds the daemon socket and nothing else
privileged. It registers one ordinary worker for each entry in its
configuration. When one of those workers claims a task, the launcher starts a
container from that entry's fixed settings and hands it the task. The container
runs the task with the code `dagu worker` already uses, reports to the
coordinator, and exits.

DAG authors change nothing. `worker_selector` picks the worker as it does today.
The coordinator protocol does not change.

## Problem statement

A deployment that wants each kind of job isolated, with its own dependencies
and secrets, has these options today:

| Option | Cost |
| --- | --- |
| One always-on `dagu worker` container per kind of job | Holds RAM while idle, and grows with each application |
| `container:` or Docker steps | The Dagu process needs the daemon socket, which amounts to root on the host |
| A socket proxy in front of the daemon | Anything allowed to create containers can usually start a privileged one |
| Rootless Podman behind `DAGU_CONTAINER_RUNTIME=podman` | Limits the damage to one user, but Dagu still chooses every image and mount |
| `kubernetes` steps | Creates a Job per step, but needs a cluster that is itself always on |

A self-hosted user reported this choice directly. They run one worker container
per application because they will not give Dagu the socket, and they would
rather have only Dagu running while idle.

## Goals

1. Idle cost does not grow with the number of worker definitions. One launcher
   serves all of them.
2. The Dagu server, scheduler, coordinator, and task containers never hold the
   daemon socket.
3. Nothing in a task can choose an image, mount, network, or privilege.
4. DAGs do not change.
5. Status, logs, artifacts, retries, and cancellation behave as they do on
   `dagu worker`.
6. It works with any Docker-compatible daemon, including rootless Podman.

## Non-goals

- Starting containers on other hosts, or autoscaling machines.
- A Kubernetes backend. `kubernetes` steps already cover that case.
- Protecting a worker's secrets from a compromised coordinator. See
  [Security](#security).
- Keeping running tasks alive when the launcher crashes.
- Resuming agent sessions held on a worker. See
  [Agent sessions](#agent-sessions).
- Running more than one launcher against one daemon.
- Dagu Cloud. Managed pods have no container daemon.

## Design

### Overview

```text
  Dagu server, scheduler, coordinator          (no daemon access)
        ^                         ^
        | poll, claim,            | status, logs, artifacts,
        | heartbeats, cancel      | secret references
        |                         |
  +-----+---------+  stdin  +-----+------------+
  | dagu launcher +-------->| task container   |
  | one process   |         | dagu worker-task |
  +-----+---------+         +-----^------------+
        | daemon socket           |
        v                         | create, start, stop, remove
  Docker or Podman ---------------+
```

A task moves through these steps:

1. The launcher starts one `worker.Worker` per configured entry, using the
   entry's `id`, `labels`, and `max_active_runs`.
2. A poller claims a task and acknowledges the claim. The coordinator records a
   lease for the entry's worker ID, as it does for any worker.
3. The launcher's `TaskHandler` creates a container, attaches to its stdin,
   starts it, and writes the task.
4. The container runs `dagu worker-task`, which runs the task through the
   existing remote task handler. It reports under the same worker ID.
5. The handler waits for the container to exit, checks that the run reached an
   expected state, and removes the container.
6. The handler returns, which frees the poller's slot.

### Why the coordinator does not change

Each thing the coordinator needs already exists:

- **Identity.** `Poll` sets `Task.worker_id`. Status, log, secret, and profile
  calls are validated against the lease's attempt and worker ID
  (`validateStatusLease`). A container using the task's worker ID passes.
- **Liveness.** `trackingHandler` keeps the attempt registered while `Handle`
  blocks, so the launcher's `Worker` sends the worker and run heartbeats.
- **Cancellation.** Heartbeat responses cancel the handler's context.
- **Concurrency.** Pollers block in `Handle`, so `max_active_runs` caps the
  number of containers.
- **Task content.** A distributed task already carries `definition`,
  `base_config`, `previous_status`, and its owner coordinator. The container
  needs no DAG files.

### Configuration

The launcher reads a config file of its own. The server never reads this file.

```yaml
worker:
  coordinators: ["dagu:50055"]

launcher:
  health_port: 8093
  workers:
    - id: backup
      labels: { role: backup }
      image: registry.example/dagu-worker-backup:2.26.0
      env_files: [/etc/dagu-launcher/backup.env]
      volumes:
        - dagu-backup-cache:/var/lib/dagu
        - /mnt/nas/backups:/backups
      network: dagu
      memory: 512m
      cpus: 1
      max_active_runs: 2
```

```yaml
# A DAG, unchanged from an always-on worker.
worker_selector: { role: backup }
```

| Field | Required | Default | Meaning |
| --- | --- | --- | --- |
| `id` | Yes | | Worker ID. Must be unique in the deployment and stable across restarts |
| `labels` | No | None | Worker labels matched by `worker_selector` |
| `image` | Yes | | Image containing a `dagu` binary of a compatible version |
| `pull` | No | `missing` | `always`, `missing`, or `never` |
| `env` | No | None | Environment variables |
| `env_files` | No | None | Files of environment variables, read at each launch so a change needs no restart |
| `volumes` | No | None | `source:target[:ro]` |
| `network` | Yes | | Network from which the coordinator is reachable |
| `memory` | No | None | Memory limit |
| `cpus` | No | None | CPU limit |
| `max_active_runs` | No | `4` | Concurrent containers for this entry |
| `stop_timeout` | No | `75s` | Time between SIGTERM and SIGKILL. Keep it 15s above `worker.shutdown_timeout`, which defaults to 60s |

The launcher refuses to start when an entry:

- sets a field outside this table, such as `privileged`, `cap_add`, or
  `devices`;
- mounts `/`, `/run`, `/var/run`, or a path ending in `docker.sock` or
  `podman.sock`;
- uses `network: host` or `network: container:*`;
- repeats another entry's `id`.

The mount check catches common mistakes. It is not a complete defense, because
whoever writes this file already controls the host's containers.

The launcher picks its daemon the way Docker steps do, through
`docker.ResolveDaemonHost`, so `DOCKER_HOST`, `DAGU_CONTAINER_RUNTIME`, and
`DAGU_PODMAN_HOST` all apply. Pulls use the existing `RegistryAuthManager`, so
private registries work through `DOCKER_AUTH_CONFIG` in the launcher's
environment. At startup, the launcher pulls any image the `pull` policy requires
so that the first run does not pay for it.

An illustrative deployment, where only the launcher sees the socket. Both
services and the task containers must run on the daemon behind that socket, so
that they share the `dagu` network. The documentation will ship a tested Compose
file.

```yaml
services:
  dagu:
    command: ["dagu", "start-all"]
    networks: [dagu]
  launcher:
    command: ["dagu", "launcher", "--config", "/etc/dagu/launcher.yaml"]
    volumes:
      - /run/user/1000/podman/podman.sock:/run/podman/podman.sock
      - ./launcher.yaml:/etc/dagu/launcher.yaml:ro
      - ./secrets:/etc/dagu-launcher:ro
    networks: [dagu]
```

### Container specification

Every value comes from the entry or from a constant. The only task field used is
the attempt key, and only through a hash.

| Setting | Value |
| --- | --- |
| Image, volumes, network, limits | From the entry |
| Environment | `worker.coordinators` and `peer` settings from the launcher's config, then `env_files`, then `env`. The launcher's own environment is not inherited |
| Command | `dagu worker-task`. The image's `ENTRYPOINT` is kept, the same contract as `command: ["dagu", "worker"]` today |
| Stdin | `OpenStdin: true`, `StdinOnce: true` |
| Security | `no-new-privileges`, `Privileged: false` |
| Auto-remove | Off, so the launcher can read the exit state and OOM flag first |
| Name | `dagu-<id>-<hash of attempt key>` |
| Labels | `dagu.launcher=true`, `dagu.launcher.worker=<id>` |

The official image needs no change. Use `PUID` and `PGID` in `env` to choose the
user, and mount a volume at `DAGU_HOME` to keep tool and bundle caches between
runs.

### Task handoff

`dagu worker-task` is a hidden command. It reads one frame from stdin: a 4-byte
big-endian length followed by a marshaled `coordinatorv1.Task`.

Stdin is used for these reasons:

- A task can carry `definition`, `params`, and `passed_envs`. Stdin keeps them
  out of `docker inspect`, unlike an environment variable or a command argument.
- It has no environment or argument size limit.
- It needs no shared filesystem and no new coordinator call.

The launcher attaches before starting the container, so no bytes are lost.

Stdin stays open after the frame, because it is also the liveness link. With
`StdinOnce`, the daemon closes the container's stdin when the launcher's attach
stream ends. End of file before the run finishes means the launcher is gone, and
the container treats it as SIGTERM.

### Task container

`worker.RunTask` in `internal/service/worker` gives the task what a `Worker`
gives it, without the parts the launcher owns:

| Provided | Omitted |
| --- | --- |
| A coordinator client from `worker.coordinators` and `peer` | Polling and claim acknowledgment |
| `NewRemoteTaskHandler` with `WorkerID` set to `task.worker_id` | Worker and run heartbeats |
| OpenCode host and SQL pool manager in the context | Health server |
| A shutdown signal, so SIGTERM or stdin end of file drains the run, then cancels it after `worker.shutdown_timeout` | Machine sampling and the browser reaper |

The process exits 0 once the handler returns, even if the DAG failed. It exits
non-zero only when setup fails. The launcher does not read a run's result from
the exit code.

### Launcher

The launcher lives in `internal/service/launcher`. It runs one `worker.Worker`
per entry, each with its own coordinator client, because `Worker.Stop` closes
its client. Each `Worker` gets a config copy with `HealthPort: 0`, and the
launcher serves one health endpoint of its own.

It does not parse DAGs, resolve secrets, or run steps. Those remain in the task
container, which keeps the process with the socket small.

Container access goes behind a small interface, so that tests can use a fake:

```go
type containerRuntime interface {
    Ensure(ctx context.Context, image string, pull PullPolicy) error
    Start(ctx context.Context, spec containerSpec, frame []byte) (runningContainer, error)
    StopAndRemoveManaged(ctx context.Context, timeout time.Duration) error
}

type runningContainer interface {
    Wait(ctx context.Context) (exitState, error)
    Signal(ctx context.Context, sig os.Signal) error
    Stop(ctx context.Context, timeout time.Duration) error
    Remove(ctx context.Context) error
}
```

The handler is the whole of the launcher's task logic:

```go
func (h *taskHandler) Handle(ctx context.Context, task *coordinatorv1.Task) error {
    if task.TargetWorkerId != "" {
        return h.fail(ctx, task, errAgentSessionResume)
    }
    c, err := h.runtime.Start(ctx, h.spec(task.AttemptKey), encodeFrame(task))
    if err != nil {
        return h.fail(ctx, task, err)
    }
    defer h.remove(c)
    worker.OnShutdownSignal(ctx, forwardTo(c))
    state := h.waitOrStop(ctx, c) // ctx canceled: stop with stop_timeout
    return h.failIfUnfinished(ctx, task, state)
}
```

`h.spec` takes only the attempt key, which enforces goal 3 by construction.

`internal/service/worker` needs three small changes: export `onShutdownSignal`,
add `RunTask`, and add the agent session mode described below.

### Reporting a failure the container could not report

Only the launcher can report that a container never started or died early. It
reports from inside `Handle`, while the lease is still live.

After a container exits, the launcher calls `GetDAGRunStatus`:

- A different attempt: do nothing.
- `NotStarted`, `Queued`, or `Running`: push `Failed` with a reason such as
  `task container exited with code 137 (out of memory)`. Start from the returned
  status so that step results are kept, and mark unfinished steps failed.
- Anything else, including `Waiting`: do nothing.

The check must come first. `statusDecision` rejects a terminal status that
differs from the recorded one, but accepts one that matches, so a blind `Failed`
push could overwrite a real failure and its details.

If the check or the push fails, the existing lease expiry fails the run within
90 seconds (`DefaultStaleLeaseThreshold`).

### Agent sessions

Managed OpenCode harness steps and `browser` and `computer` steps can keep a
session on the worker, recorded as `OwnerWorkerID`. On an on-demand worker the
first attempt works, but the session ends with the container. This includes a
session waiting for a person's answer.

- **Resume.** A retry that must resume a session arrives with `target_worker_id`
  set. The launcher fails it at once: `this on-demand worker cannot resume an
  agent session; run this DAG on an always-on worker`.
- **Cleanup.** Cleanup jobs for these sessions are completed without calling the
  provider, because the session is already gone. `Worker` gets a mode for this.
  Otherwise the launcher would try to start OpenCode, fail, and release the job
  forever.

## Security

| Attacker holds | With the socket in Dagu today | With the launcher |
| --- | --- | --- |
| Dagu server or coordinator | Root on the host | Can send any DAG to any entry, so can read that entry's secrets and volumes. Cannot start another image, add a mount or privilege, or reach the daemon |
| Code in a job | Depends on the worker's mounts | That container's environment, volumes, and peer credentials. No socket |
| The launcher | Not applicable | Full daemon access. Rootless Podman limits this to one user |
| Write access to the launcher's config | Not applicable | Full daemon access. Keep the file away from the server |

What this protects is the host. It does not separate a worker's secrets from
Dagu, because anyone controlling the coordinator can dispatch a DAG that prints
them. Always-on workers have the same limit today.

Other properties:

- **Secrets.** `env_files` are readable only by the launcher and are passed to
  the daemon as container environment. They never go to the coordinator.
  Dagu-managed secrets still resolve through secret references, as on any worker.
- **Unlabeled DAGs.** An empty `worker_selector` matches every worker, including
  entries with secrets. This is also true today. Deployments should give every
  entry a label and every DAG a selector.
- **Peer transport.** `peer.insecure` defaults to `true`, which has no client
  authentication. The launcher logs a warning on plaintext transport. Use mTLS
  when job code is not fully trusted.
- **Peer credentials.** A job can use its container's client certificate to act
  as a worker. This is also true today.

## Failure behavior

| Event | Launcher action | Run result |
| --- | --- | --- |
| Image pull, create, or start fails | Pushes `Failed` at once with the reason | `Failed` |
| Run reaches a terminal state | Removes the container | As reported by the container |
| Run reaches `Waiting` | Removes the container | `Waiting`. Approval dispatches a new attempt, which starts a new container |
| Container dies early, including OOM | Pushes `Failed` with the exit code | `Failed` |
| Coordinator cancels the run | Stops the pull, or stops the container with `stop_timeout` | `Aborted` as reported, or `Failed` if the container is killed first |
| Launcher receives SIGTERM | `Worker.Stop` forwards SIGTERM to containers, then cancels after `worker.shutdown_timeout` | Same as stopping `dagu worker` |
| Launcher crashes | None. Stdin closes, and the container stops and reports | `Aborted` or `Failed` as reported, otherwise lease expiry |
| Launcher restarts | Stops and removes containers labeled `dagu.launcher=true` before its first poll | No effect on finished runs |
| Coordinator unreachable for 90s | Existing owner timeout cancels the handler, which stops the container | Lease expiry on the coordinator |
| Daemon stops during a run | Treated as an early exit | `Failed` |
| Parent dispatches a sub-DAG to its own entry with no free slot | None. The parent waits while holding a slot | Can wait indefinitely. Set `max_active_runs` above the nesting depth |

## Observability

- Each entry appears on the Workers page as an ordinary worker with its busy
  count.
- Heartbeats report the launcher's machine, not its task containers. Resource
  use shown for these workers, including in Dagu Cloud, does not cover the jobs.
- The launcher logs each launch with worker ID, run ID, attempt key, container
  ID, launch duration, exit code, and OOM flag.
- A failed container's last 4 KiB of output goes to the launcher's log, not to
  the run, because it can contain secrets.
- Step logs stream to the coordinator as on any worker.

## Compatibility and rollout

The change is additive: no DAG, proto, or API changes, and `dagu worker` keeps
working. Migrating a worker means adding an entry with the same labels and image,
removing the old container, and removing the socket from the Dagu service.

1. **`dagu worker-task` and `RunTask`.** Testable by piping a frame by hand.
2. **`dagu launcher`.** Released as experimental, with `specs/` and `conformance/`
   covering config acceptance and rejection without a daemon, as spec 037 does.
3. **Documentation.** A tested Compose file using rootless Podman, and a note on
   the agent session limit.

## Verification and acceptance criteria

1. A DAG with `worker_selector` runs in a new container, with normal status,
   logs, and artifacts. Afterwards the container is gone, and no worker container
   exists while idle.
2. A task with hostile `target`, `params`, `definition`, and `worker_selector`
   values produces the same container spec as a plain task.
3. Each config rule in [Configuration](#configuration) stops the launcher from
   starting.
4. With `max_active_runs: 2` and three tasks, no more than two containers run at
   once, and the third task runs afterwards.
5. Canceling a run sends SIGTERM, records `Aborted`, and removes the container.
6. A job exceeding `memory` fails within 5 seconds of exit, with an out-of-memory
   reason rather than lease expiry.
7. A missing image fails the run with the pull error, without waiting for lease
   expiry.
8. A real `Failed` run keeps its original error and step results after the
   launcher's status check.
9. After `kill -9` on the launcher, the container stops and the run ends. A
   restarted launcher leaves no labeled containers behind before polling.
10. A run reaching a human task leaves `Waiting`, and approval completes it in a
    new container.
11. A retry with `target_worker_id` fails with the stated message, and session
    cleanup jobs for the entry complete.
12. Task values do not appear in `docker inspect`.
13. Criteria 1, 5, and 9 pass on Docker and on rootless Podman, using the official
    image, so that the frame and end of file pass through
    `tini`, `entrypoint.sh`, and `sudo`.
14. An idle launcher with five entries uses less RAM than two idle `dagu worker`
    processes. This is a target to measure, since the proposal's value rests on it.

## Alternatives considered

- **Document rootless Podman only.** Works today with no code, and is the
  recommended socket for the launcher. Alone, it leaves Dagu holding the socket
  and choosing every image and mount.
- **A socket proxy.** Filtering requests safely means understanding container
  create requests, and most setups end up allowing privileged containers.
- **A broker for every Docker step.** Keeps per-step containers, but must expose
  and police most of Docker's configuration. Rejected as too large.
- **Start normal workers on demand, and exit when idle.** Needs a new RPC to
  expose pending tasks, and the launcher races other workers for them. Idle waits
  also use RAM. Its one benefit, reusing a warm worker, can come later.
- **Let containers hold their own lease and survive a launcher restart.** Better
  for upgrades, but needs two heartbeat sources and re-adopting containers to keep
  the concurrency limit. Deferred until a launcher restart proves to be a real
  cost.
- **Build the launcher into `start-all`.** Puts the socket back in the Dagu
  process, which defeats goal 2.
- **Run each job on Dagu Cloud.** Managed pods have no container daemon, and these
  jobs often need the local network. It would also require new billing and abuse
  controls.

## Consequences

- Idle memory stops growing with the number of worker definitions, and the host
  is protected from a compromised Dagu process.
- The socket moves rather than disappears. The launcher is a new privileged
  process to operate and upgrade.
- A launcher crash or restart stops running tasks for every entry, where
  separate workers would fail independently.
- Each run pays for container startup and cold caches, unless a volume is mounted
  at `DAGU_HOME`. A per-minute schedule creates a container per minute.
- Agent sessions that wait or resume need an always-on worker, so some
  deployments will run both kinds.

## Open questions

1. Is the demand real? So far there is one report. Measure the idle RAM of a few
   workers against the launcher before building past step 1.
2. Should this be free? Worker features are not license-gated today, and the
   proposal is to keep it free.
3. Should the launcher refuse plaintext peer transport instead of warning?
4. Do frames and end of file pass reliably through `sudo` in the official image's
   entrypoint? If not, add an optional per-entry `entrypoint`.
5. Is tool installation safe when concurrent containers share one cache volume at
   `DAGU_HOME`?
