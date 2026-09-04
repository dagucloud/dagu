# Spec: Kubernetes Run Action

## Status

Implemented.

This spec defines conformance behavior for the built-in `kubernetes.run`
action (alias: `k8s.run`).

## Scope

This spec defines the `kubernetes.run` action, which runs a command as a
Kubernetes `Job` with exactly one Pod attempt by default, waits for it to
finish, and captures its output and exit status.

This spec covers:

- the required `with.image` field and its two validation paths
- the command and output contract shared with other command-shaped actions
- container environment variables via `with.env`
- the `with.working_dir` field
- Job naming, namespace, and the default single-attempt (no-retry) policy
- `with.cleanup_policy` and Job lifecycle after completion
- validation and runtime errors

This spec does not define:

- an "exec into an existing Job or Pod" mode; unlike
  [Spec 037: Docker Run Action](037-docker-run.md), `kubernetes.run` always
  creates a new Job and never targets an existing one
- resource requests/limits, node selectors, tolerations, affinity, security
  contexts, pod failure policies, service accounts, priority classes,
  volumes, or image pull secrets, all of which this action accepts and
  passes through to the Kubernetes API essentially unchanged
- kubeconfig and cluster authentication and discovery
- registry authentication for private images
- signal delivery, timeout, and abort behavior beyond what already applies
  to any step (see [Spec 017: Built-In Run Context](017-built-in-run-context.md))

## Goal

Workflow authors run a command as a Kubernetes workload the same way they
would run a local command or a Docker container, without writing raw Job
manifests.

## Behavior

### Required image

`with.image` is required. There is no alternate target to run against, in
contrast with `docker.run`'s exec mode.

### Command and output

The action's command comes from `with.command`, using the same string and
argument-array shapes as any other command-shaped action (see
[Spec 014: Step Run Command](014-step-run-command.md)). The action does not
wrap the command in a shell of its own; a command that needs shell
operators or variable expansion inside the container must invoke a shell
itself (for example `sh -c '...'`).

The Pod's stdout and stderr are captured as one combined stream. An
`output` field on the step stores that stream as a step output, the same
as any other command-shaped action.

### Container environment

`with.env` is a list of `{name, value}` (or `{name, value_from: ...}`)
entries that become the container's own environment variables, visible to
a shell running inside the Pod through normal `$VAR` expansion. This is a
different mechanism from a plain command executor's environment: it is not
resolved on the host before dispatch, but delivered to the Kubernetes API as
part of the Pod spec and read by the container process itself.

### Working directory

`with.working_dir`, when set, becomes the container's working directory.
The directory must already exist in the image; the action does not create
it.

### Namespace and naming

`with.namespace` defaults to `default`. The Job's name is generated from the
step name (lowercased, sanitized to Kubernetes naming rules) plus a random
suffix; a workflow author does not choose the exact Job name.

### Retry policy

The Job's `with.backoff_limit` defaults to `0`: a failing command's Pod is
not retried, and the Job is marked failed after that single attempt.

### Cleanup

`with.cleanup_policy` controls whether the Job (and its Pod) is deleted once
it reaches a terminal state:

- `delete` (default): the Job is deleted after the step finishes,
  regardless of whether it succeeded or failed.
- `keep`: the Job is left in place after the step finishes.

## Errors

- `with` has no field other than `command`: `command` is extracted into the
  step's own command field, `with` becomes empty, and the step fails
  before contacting the cluster with an error naming `with.image` as
  required.
- `with` has a field other than `command`, and `with.image` is not set: DAG
  build fails config-schema validation before the DAG starts running, with
  an error naming the missing `image` property.
- `with.cleanup_policy` is set to a value other than `delete` or `keep`: the
  step fails before contacting the cluster, with an error stating the
  allowed values.
- `with.active_deadline` (and the other non-negative numeric fields this
  action accepts) is negative: DAG build fails config-schema validation
  before the DAG starts running.
- The command exits non-zero: with the default `backoff_limit: 0`, the Job
  is marked failed after that single Pod attempt, and the step fails with
  an error describing the Job's own failure condition (for example,
  reaching its backoff limit), not the bare numeric exit code.

## Related Specs

- Docker Run Action (closest sibling action): [Spec 037: Docker Run Action](037-docker-run.md)
- Step outputs: [Spec 012: Step Outputs](012-step-outputs.md)
- Command shape: [Spec 014: Step Run Command](014-step-run-command.md)
- Run context: [Spec 017: Built-In Run Context](017-built-in-run-context.md)

## Examples

Run a command as a Job and capture its output:

```yaml
steps:
  - action: kubernetes.run
    with:
      image: alpine:3
      command: echo hello
    output: OUT
```

Set the container's working directory and environment, and keep the Job
around after it finishes:

```yaml
steps:
  - action: kubernetes.run
    with:
      image: alpine:3
      working_dir: /tmp
      cleanup_policy: keep
      env:
        - name: MY_VAR
          value: hello
      command: sh -c 'pwd && echo $MY_VAR'
    output: OUT
```
