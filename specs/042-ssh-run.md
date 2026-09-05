# Spec: SSH Run Action

## Status

Implemented.

This spec defines conformance behavior for the built-in `ssh.run` action.

## Scope

This spec defines `ssh.run`, which runs a command on a remote host over
SSH.

This spec covers:

- the connection fields `with.host`, `with.port`, `with.user`,
  `with.password`, `with.key`
- the required `with.command` field, in the same string and argument-array
  shapes as any other command-shaped action
- that a command list runs as a single remote script, not as separate
  per-command connections, and what that implies for shared shell state and
  stop-on-failure behavior
- the `working_dir` step field's SSH-specific default
- that connection fields are validated only at runtime, not at DAG-build
  time
- validation and runtime errors
- what a local step timeout or abort does and does not guarantee about the
  remote command's own fate

This spec does not define:

- `with.strict_host_key` or `with.known_host_file`
- `with.bastion` (jump host) configuration
- the `sftp.upload`/`sftp.download` actions, which share this action's
  connection configuration but are a separate action
- registry/host-key trust management beyond the above

## Goal

Workflow authors run a command on a remote host without shelling out to a
local `ssh` client through a command executor.

## Behavior

### Connection fields

`with.host` and `with.user` identify the target; `with.port` defaults to
`22`. Authentication uses `with.key` (a path to a private key file) if set,
otherwise `with.password`. If neither is set, the action falls back to
trying the caller's default SSH keys
(`~/.ssh/{id_rsa,id_ecdsa,id_ed25519,id_dsa}`).

None of the connection fields are validated when the DAG is built (see
"Errors" below); a step with a completely empty `with.host`, for example,
does not fail `dagu validate`.

### Command

`with.command` (required) uses the same string and argument-array shapes as
any other command-shaped action (see
[Spec 014: Step Run Command](014-step-run-command.md)).

An array of commands is not run as separate remote invocations. All
entries are concatenated into one script, wrapped in a shell function, and
run as a single remote shell invocation with `set -e`, so:

- Shell state (exported variables, `cd`, and similar) set by one entry is
  visible to later entries in the same list, because they share one shell
  process.
- A failing entry stops the remaining entries from running, and the step
  fails with the remote process's exit status.

### Working directory

The step's `working_dir` field, when set, changes to that directory on the
remote host before running the command. When unset, the command runs in
the SSH user's home directory on the remote host -- the DAG's own local
`working_dir` (meant for local execution) is not used as a fallback for SSH
steps.

## Errors

### Timeout, abort, and cleanup

A step `timeout_sec` elapsing, or the DAG-run being stopped (`dagu stop` or
an equivalent cancellation), ends the step the same way it ends any other
step (see [Spec 017: Built-In Run Context](017-built-in-run-context.md)):
the step is reported as timed out or aborted promptly, without waiting for
the remote command to finish.

Reaching that result closes dagu's local SSH session and connection to the
remote host. It does not send any signal to the remote command. Whether the
remote process actually stops when the connection closes is up to the
remote host's own SSH server and shell, not to `ssh.run` -- a command still
running when the local step ends may keep running to completion on the
remote host, detached from the DAG-run that started it.

### Other errors

- `with.command` is missing: rejected at DAG-build-time validation, before
  the DAG starts running, with an error containing `"with.command is
  required"`.
- Every other connection failure is a runtime error, surfaced only when the
  step actually runs, wrapping the underlying SSH library's error:
  - the target port refuses the connection (nothing listening): the error
    contains `"connection refused"`.
  - authentication is rejected (wrong password, no working key): the error
    contains `"unable to authenticate"`.
- The remote command exits non-zero: the step fails, and the error contains
  `"Process exited with status <code>"`.

## Related Specs

- Command shape: [Spec 014: Step Run Command](014-step-run-command.md)
- Step outputs: [Spec 012: Step Outputs](012-step-outputs.md)
- Run context: [Spec 017: Built-In Run Context](017-built-in-run-context.md)

## Examples

Run a command on a remote host:

```yaml
steps:
  - action: ssh.run
    with:
      host: example.internal
      user: deploy
      key: ~/.ssh/id_ed25519
      command: systemctl restart myapp
```

Run several commands that share shell state, in a specific remote
directory:

```yaml
steps:
  - action: ssh.run
    working_dir: /srv/myapp
    with:
      host: example.internal
      user: deploy
      key: ~/.ssh/id_ed25519
      command:
        - export RELEASE=$(git rev-parse HEAD)
        - echo "deploying $RELEASE"
```
