# Spec: Scheduler High Availability

## Status

Partially implemented. Conformance covers roles, takeover, and role
reporting. Further sections are added as the behavior they describe lands.

## Scope

This spec defines how several `dagu scheduler` processes sharing one data
directory choose an active instance, how a standby takes over, and how an
operator tells them apart. It does not define the availability of the shared
storage, the execution of steps, or distributed worker behavior.

## Goal

An operator can run a standby scheduler on a second host and know, without
reading logs, which instance is active and how long a failover takes.

## Behavior

### Roles

- Schedulers that use the same data directory compete for one lock. The
  instance holding it is `active`. Every other instance is `standby`.
- Only the active instance schedules runs and consumes queues.
- A standby keeps waiting for as long as the active instance renews the lock.
- A standby keeps waiting through storage errors.
- The active instance renews the lock every `I`, the smaller of 7 seconds and
  a quarter of the stale threshold. It keeps running through renewal errors,
  and stops being active once it has gone `T - I` without a renewal or finds
  the lock is no longer its own. A renewal that blocks on storage counts as
  no renewal.
- An instance past that limit dispatches nothing, even before it stops.

### Takeover

- The active instance releases the lock when it stops cleanly. A standby then
  becomes active within the lock retry interval plus startup.
- When the active instance dies without releasing the lock, a standby becomes
  active after the lock goes stale. With a stale threshold `T` and a retry
  interval `R`, this happens within `T + 2R` plus startup of the death.

### Observability

- Every scheduler serves `GET /health` on `scheduler.port`, including a
  standby. The response is `200` with `status` and `role`, where `role` is
  `active` or `standby`.
- `GET /health?role=active` answers `503` on an instance that is not active,
  so a load balancer or probe can select the active instance.

## Configuration

| Key | Default | Meaning |
| --- | --- | --- |
| `scheduler.lock_stale_threshold` | `30s` | Age at which a lock can be taken over |
| `scheduler.lock_retry_interval` | `5s` | How often a standby tries the lock |
| `scheduler.port` | `8090` | Port of the health endpoint; `0` disables it |

## Storage

All schedulers must use the same `DAGU_HOME`, or the same data and DAG
directories, on storage that every host can reach.

## Not provided

- Availability of the shared storage itself.
- Exactly-once execution of step side effects, or resuming a step after a
  crash. Runs on a failed host are marked failed.

## Conformance

`conformance/spec085_scheduler_ha` runs the real binary. It asserts that one
of two schedulers is active and stays so past the stale threshold, that a
standby takes over within the bound after the active process is killed, and
that a clean stop hands over without waiting for staleness.
