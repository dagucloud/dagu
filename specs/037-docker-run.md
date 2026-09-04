# Spec: Docker Run Action

## Status

Implemented.

This spec defines conformance behavior for the built-in `docker.run` action.

## Scope

This spec defines the `docker.run` action, which runs a command against a
Docker-compatible daemon in one of two modes:

- create mode: create a new container from an image, run a command in it,
  and optionally remove the container afterward.
- exec mode: run a command inside an already-running container, without
  creating or removing anything.

This spec covers:

- mode selection from `with.image` and `with.container_name`
- accepted `with` fields for both modes
- the command and output contract shared with other command-shaped actions
- image pull policy
- the `working_dir` and `volumes` shortcuts
- container naming and `auto_remove`
- validation and runtime errors

This spec does not define:

- the DAG-level `container:` field, which keeps one container alive across
  every step in a DAG (a different mechanism from the per-action containers
  this spec describes)
- the `container.run` action, an alias of this same action under a different
  name, kept for backward compatibility
- the full Docker Engine API surface reachable through the legacy nested
  `container`, `host`, `network`, and `exec` configuration blocks (image
  build, networks, health checks, restart policies, resource limits)
- registry authentication
- signal delivery, timeout, and abort behavior beyond what already applies to
  any step (see [Spec 017: Built-In Run Context](017-built-in-run-context.md))

## Goal

Workflow authors run a command against a container image without writing a
Dockerfile-shaped configuration block, and can target either a fresh
container or one already running, using the same action.

## Behavior

### Mode selection

`docker.run` selects its mode from two `with` fields:

- `with.image`: the image to create a new container from.
- `with.container_name`: the name or ID of an existing container.

If `with.image` is set and `with.container_name` is not, the action creates
a new container from that image (create mode).

If `with.container_name` is set and `with.image` is not, the action runs the
command inside that already-running container (exec mode). The container
must already exist and be running; this action never creates, starts, or
stops it.

If both are set, the action creates a new container from `with.image` and
assigns it the name `with.container_name` (create mode with an explicit
container name).

At least one of `with.image` or `with.container_name` must be set.

### Command and output

The action's command comes from `with.command`, using the same string and
argument-array shapes as any other command-shaped action (see
[Spec 014: Step Run Command](014-step-run-command.md)). Values in `with.command`
are resolved on the host, using the step's normal value resolution scope
(see [Spec 003: Value Resolution and Field Evaluation](003-value-resolution.md)),
before the resulting command is sent to the container. The container process
does not need matching environment variables of its own for `${...}`-style
substitution to work, because substitution already happened before dispatch.

If `with.command` is omitted in create mode, the action starts the container
using the image's own default command.

The command's combined stdout is captured the same way as any other
command-shaped action: an `output` field on the step stores it as a step
output. A non-zero exit status from the command fails the step, and the
process exit code is preserved as the step's exit code.

`with.command` may be a list of two or more command entries. Each entry runs
in turn, in the same container, in order. If any entry exits non-zero, the
action stops and fails without running the remaining entries.

### Pull policy (create mode only)

`with.pull` selects when the image is pulled before the container is
created:

- `missing` (default): pull only if the image is not already present
  locally.
- `always`: always pull before creating the container.
- `never`: never pull; fail if the image is not already present locally.

### Working directory and volumes shortcuts

`with.working_dir` sets the working directory the command runs in inside
the container.

`with.volumes` is a list of `source:target` or `source:target:mode` entries,
`mode` being `ro` or `rw` (default `rw`). A `source` that looks like a path
(absolute, or starting with `.` or `~`) is treated as a bind mount and
resolved relative to the step's working directory when relative; any other
`source` is treated as the name of a Docker volume.

### Container naming and cleanup (create mode only)

`with.auto_remove` (default `false`) removes the container once the command
finishes, whether it succeeded or failed. In exec mode, `with.auto_remove`
has no effect, because the action does not own the existing container's
lifecycle.

### Exec options (exec mode only)

`with.exec` accepts `user`, `working_dir`, and `env`, applied to the exec
call made against the existing container. Setting `with.exec` without
`with.container_name` is an error, since these options only apply to exec
mode.

`with.image` present always selects create mode, even when
`with.container_name` is also set to name the created container (see "Mode
selection" above). In that combination `with.exec` is accepted rather than
rejected -- the validation error above only fires when
`with.container_name` is absent -- but it is silently ignored: create mode
never applies it, so a value like `with.exec.working_dir` has no effect on
the created container.

## Errors

- Neither `with.image` nor `with.container_name` is set, and `with` has a
  field other than `command`: DAG build fails config-schema validation
  before the DAG starts running, with an error naming the missing
  `image`/`container_name` requirement.
- Neither `with.image` nor `with.container_name` is set, and `with` has no
  field other than `command`: `command` is extracted into the step's own
  command field, `with` becomes empty, and the config-schema check above
  does not apply. The step instead fails at run time with a generic
  "configuration is required" error, before contacting the daemon.
- `with.exec` is set together with `with.image` but without
  `with.container_name`: the step fails before contacting the daemon, with
  an error stating that exec options require `container_name`.
- `with.pull: never` and the image is not present locally (create mode): the
  step fails with a daemon "not found" error.
- `with.container_name` does not name an existing container (exec mode): the
  step fails with a daemon "not found" error.
- `with.container_name` names a container that is not running (exec mode):
  the step fails with a daemon error stating the container is not running.
- The command exits non-zero: the step fails, and the step's exit code
  matches the command's exit code.

## Related Specs

- Step outputs: [Spec 012: Step Outputs](012-step-outputs.md)
- Command shape: [Spec 014: Step Run Command](014-step-run-command.md)
- Value resolution: [Spec 003: Value Resolution and Field Evaluation](003-value-resolution.md)
- Run context: [Spec 017: Built-In Run Context](017-built-in-run-context.md)

## Examples

Create a new container and capture its output:

```yaml
steps:
  - action: docker.run
    with:
      image: alpine:3
      auto_remove: true
      command: echo hello
    output: OUT
```

Run a command inside an already-running container:

```yaml
steps:
  - action: docker.run
    with:
      container_name: my-existing-container
      command: echo hello
    output: OUT
```

Pin the working directory and mount a host directory:

```yaml
steps:
  - action: docker.run
    with:
      image: alpine:3
      auto_remove: true
      working_dir: /data
      volumes:
        - ./data:/data
      command: cat file.txt
    output: OUT
```
