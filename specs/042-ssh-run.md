# Spec: SSH Run Action

## Status

Partially implemented.

Conformance covers action configuration and the run-start resolution of
credentials without an SSH server. Authentication, remote shell behavior, and
cancellation belong in executor unit and integration tests.

## Scope

This spec defines the configuration boundary for `ssh.run`.
It does not define SSH protocol behavior, host-key management, SFTP transfers,
or the lifetime of remote processes after disconnection.

## Goal

Workflow authors can validate an SSH command before connecting to its host.

## Behavior

`ssh.run` accepts connection fields under `with` and requires `with.command`.
The command follows [Spec 014: Step Run Command](014-step-run-command.md).
`dagu validate` accepts a configured SSH action without contacting the host or
requiring its private key to exist locally.
DAG-level `ssh` supplies connection defaults for `ssh.run`. Its string fields
resolve Dagu-owned references as defined by
[Spec 003: Value Resolution](003-value-resolution.md).

### Credentials

These rules apply to `ssh.run`, `sftp.upload`, `sftp.download`, and DAG-level
`ssh`, and to the `bastion` of each.

- `key` is a private key path, `{ file: <path> }`, or `{ secret: <ref> }`.
- `password` and `passphrase` are a string or `{ secret: <ref> }`.
- `<ref>` names a Dagu-managed secret in the registry and follows the ref
  grammar of [Spec 069: Secrets Providers](069-secrets-providers.md). It
  resolves in the DAG's workspace, then in the global workspace. It is
  literal: Dagu never resolves references inside it.
- Each hop authenticates with one method. A set `key` is used with its
  `passphrase`. Otherwise a set `password` is used. Otherwise the first
  existing default key (`~/.ssh/id_rsa`, `id_ecdsa`, `id_ed25519`,
  `id_dsa`) is used with the `passphrase`. A bastion with neither a key nor
  a password tries the default keys.
- A passphrase is used only when the key is encrypted.
- Each distinct ref that the auth methods of a DAG use resolves once, at run
  start, next to `secrets:`. Every connection, step, and `foreach` item reads
  that value. A ref that an auth method does not use is not resolved.
- Resolved values are masked wherever Spec 069 masks `secrets:` values. They
  are not exported to step environments.
- A run on a worker resolves its refs through the coordinator, which answers
  only the refs that the DAG declares in `secrets:` or uses for SSH.
- A credential that DAG-level `ssh` or a base config sets replaces the
  inherited one, whatever the form of either. A `key` also replaces the
  inherited `passphrase`. A `password` replaces only the password.
- Connection fields under a step's `with` replace DAG-level `ssh` as a whole.

## Errors

Omitting `with.command` fails validation with a diagnostic naming that field.

A credential object with an unknown field, with both `secret` and `file`, with
a `secret` that does not match the ref grammar, or with `${...}` in `secret`
fails validation with a diagnostic naming the field, such as
`steps.deploy.with.key.secret`.

A ref that fails to resolve fails the run before any step runs, with an error
naming the field and the ref. A key read from a secret that is not a private
key fails the step with an error that names the ref and does not include the
value.

## Examples

```yaml
steps:
  - action: ssh.run
    with:
      host: example.invalid
      user: deploy
      key: ~/.ssh/id_ed25519
      command: echo hello
```

```yaml
ssh:
  host: example.invalid
  user: deploy
  key: { secret: ssh/deploy-key }
  passphrase: { secret: ssh/deploy-passphrase }
  bastion:
    host: jump.example.invalid
    user: deploy
    password: { secret: ssh/jump-password }
steps:
  - action: ssh.run
    with:
      command: echo hello
```
