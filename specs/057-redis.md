# Spec: Redis Actions

## Status

Partially implemented.

Integration tests cover relative `with.script_file` and `with.tls_ca` paths
resolved from the step working directory.

## Scope

This spec covers Redis action configuration acceptance, command agreement,
and representative configuration errors. Conformance requires no Redis
service.

Command execution, response formats, scripts, pipelines, locks, credential
transport, and connection lifecycle belong to executor unit and integration
coverage.

## Goal

Workflow authors can configure Redis actions and identify invalid
configuration before commands reach a Redis service.

## Behavior

`redis.<command>` selects the Redis command. An explicit `with.command`
must agree with that action name. Configured `redis.set` and `redis.get`
actions with DAG-level connection settings pass `dagu validate` without
contacting Redis.

A relative `with.script_file`, `with.tls_ca`, `with.tls_cert`, or
`with.tls_key` names a file under the step working directory.

## Errors

`dagu validate` rejects a conflicting `with.command`, an unrecognized
`with.mode`, and a `with` field the action does not define with a nonzero
exit code. An unknown field's error names the field and, when a defined
field is a likely misspelling of it, suggests that field.

When the step starts, cluster mode without `with.cluster_addrs` fails with
`cluster_addrs is required for cluster mode`. A client certificate without
its matching key fails with `both tls_cert and tls_key must be provided
together`. These failures occur before connecting to Redis.

Timeout, abort, lock cleanup, and service failures are outside this
configuration conformance scope.

## Example

```yaml
redis:
  host: redis.example.com
  port: 6379
steps:
  - action: redis.set
    with:
      key: greeting
      value: hello
  - action: redis.get
    with:
      key: greeting
```
