# Spec: Redis Actions

## Status

Implemented.

This spec defines conformance behavior for the built-in `redis.*` actions.

## Scope

Unlike other built-in actions, which each map to one fixed operation,
`redis.<command>` accepts (almost) any Redis command name as the
action's suffix: `redis.get`, `redis.set`, `redis.hset`, `redis.lrange`,
and so on all dispatch to the same `redis` executor, with `with.command`
implicitly set to the uppercased suffix.

This spec covers:

- the `redis.<command>` action-name convention, and that an explicit
  `with.command` must agree with it
- connection configuration: `with.url` or `with.host`/`with.port`,
  and that a DAG-level `redis:` block supplies defaults a step's own
  `with:` fields override
- single-command execution for a representative set of commands
  (strings, hashes, lists), driven by `with.key`, `with.value`,
  `with.keys`, `with.values`, `with.field`, `with.start`/`with.stop`,
  and so on
- `with.output_format` (`json`, the default; `jsonl`; `raw`; `csv`) and
  `with.null_value`
- that a nil result from a single command produces no output at all,
  while a nil found inside a non-nil array or map result is rendered
  using `with.null_value`
- `with.pipeline`: a batch of commands sharing one connection, and that
  it takes priority over the action-derived (or explicit) single
  command when both are present
- that a pipeline fails entirely if any queued command errors, and that
  a `nil` result inside an otherwise-successful pipeline renders as an
  empty string, not `null`
- `with.script`/`with.script_keys`/`with.script_args`: executing a Lua
  script, which also takes priority over a single command (though not
  over `with.pipeline`)
- `with.lock`: a distributed lock held for the duration of the step,
  released automatically when it ends
- that every configuration error this spec documents is rejected at
  DAG-build-time validation, not only at runtime -- except where noted,
  since several cross-field requirements cannot be expressed by this
  executor's registered JSON Schema
- validation and runtime errors

This spec does not define:

- the effect of any specific Redis command beyond the representative
  set this spec exercises (`GET`, `SET`, `HSET`, `HMGET`, `RPUSH`,
  `LRANGE`, `INCR`) -- streams, pub/sub, sorted sets, and most other
  command families are configuration surface this spec does not test
  live
- Sentinel or Cluster mode against a real topology (this spec verifies
  only that their required fields are enforced), or TLS connections
  against a real certificate
- connecting with no reachable Redis at all: the executor retries
  connecting up to 30 times with exponential backoff before failing,
  which can take upwards of 30 seconds -- this spec does not exercise
  that path live
- `with.multi`/`with.watch` (transactions and optimistic locking) or
  `with.script_sha` (pre-loaded scripts) beyond noting that they exist
- worker-mode connection pooling
- direct `type: redis` authoring

## Goal

Workflow authors read, write, and coordinate through Redis as DAG steps
-- caching a value, publishing a result, batching several operations, or
serializing concurrent steps with a distributed lock -- without shelling
out to `redis-cli`.

## Behavior

### Action name and command

`redis.<command>` sets `with.command` to `<command>`, uppercased. An
explicit `with.command` may also be given, but it must agree
(case-insensitively) with the action-derived command, or the DAG fails
to build.

### Connection

`with.url` (`redis://[user:pass@]host:port[/db]`) or `with.host` (with
`with.port`, defaulting to `6379`) selects the server. A DAG-level
`redis:` block may set any connection field (`url`, `host`, `port`,
`password`, `username`, `db`, `tls`, `tls_skip_verify`, `mode`,
`sentinel_master`, `sentinel_addrs`, `cluster_addrs`, `max_retries`)
as a default for every step; a step's own `with:` field overrides the
corresponding DAG-level value.

### Result output

The default `with.output_format: json` pretty-prints the result. `raw`
writes it as plain text (one line per array element); `csv` writes it
as CSV; `jsonl` writes one JSON value per line for an array result
(and one line per key for a map result). Whatever the format, a nil
result from a single command (for example, `GET` on a missing key)
produces no output at all -- the result writer is never invoked for a
top-level nil. `with.null_value` (default `"null"`) only comes into
play for a nil found as one element inside an otherwise non-nil array
or map result (for example, one missing field among several in an
`HMGET`).

### Pipeline

`with.pipeline` is a list of `{command, key, ...}` entries sharing one
connection; when set, it runs instead of the single command the action
name (or an explicit `with.command`) would otherwise select. Its result
is a JSON array with one entry per queued command, in order. If any
queued command fails, the entire step fails and no partial result is
written; a `nil` result inside an otherwise-successful pipeline (for
example, a `GET` on a missing key) is represented as an empty string in
that array, not `null` -- unlike the same command run standalone.

### Script

`with.script` (or `with.script_file`) runs a Lua script via `EVAL`,
with `with.script_keys` as `KEYS` and `with.script_args` as `ARGV`. It
takes priority over a single command, but not over `with.pipeline`.

### Lock

`with.lock` acquires a named distributed lock (an atomic `SETNX` under
a `dagu:lock:` key prefix) before running, and releases it once the
step finishes, successfully or not. `with.lock_retry` (default 10) and
`with.lock_wait` (default 100ms) control how many attempts, and how
long to wait between them, before giving up if the lock is already
held.

## Errors

### Validation

Every one of these is rejected at DAG-build-time validation (`dagu
validate`), not only when the step runs:

- `with.command` disagreeing with the `redis.<command>` action name:
  an error containing `command must be "<COMMAND>" for this action`.
- `with.mode` set to a value other than `standalone`, `sentinel`, or
  `cluster`: a schema `enum` error.
- `with.output_format` set to a value other than `json`, `jsonl`,
  `raw`, or `csv`: a schema `enum` error.
- `with.port` outside `1`-`65535`, or `with.db` outside `0`-`15`: a
  schema `minimum`/`maximum` error.

All other configuration errors below pass `dagu validate` and surface
only when the step actually runs:

- `with.mode: sentinel` without `with.sentinel_master` or
  `with.sentinel_addrs`: an error containing `"sentinel_master is
  required for sentinel mode"` or `"sentinel_addrs is required for
  sentinel mode"`.
- `with.mode: cluster` without `with.cluster_addrs`: an error
  containing `"cluster_addrs is required for cluster mode"`.
- `with.tls_cert` set without `with.tls_key` (or vice versa): an error
  containing `"both tls_cert and tls_key must be provided together"`.

### Runtime

- A distributed lock already held (by another process, or a
  previously abandoned attempt) when `with.lock_retry` attempts are
  exhausted: an error containing `"lock is held by another process"`.
- A pipeline with a failing queued command: an error containing
  `"pipeline execution failed"`.

## Related Specs

- Run context: [Spec 017: Built-In Run Context](017-built-in-run-context.md)

## Examples

Cache a computed value with an expiration:

```yaml
redis:
  url: redis://cache.internal:6379
steps:
  - action: redis.set
    with:
      key: "report:latest"
      value: "$REPORT_ID"
      ttl: 3600
```

Serialize a critical section across concurrent steps with a lock, and
batch several reads in one round trip:

```yaml
steps:
  - action: redis.incr
    with:
      host: cache.internal
      key: "counter:visits"
      lock: "counter:visits"
  - action: redis.get
    with:
      host: cache.internal
      key: unused
      pipeline:
        - command: GET
          key: "user:1:name"
        - command: GET
          key: "user:2:name"
```
