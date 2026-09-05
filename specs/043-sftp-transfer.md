# Spec: SFTP Transfer

## Status

Implemented.

This spec defines conformance behavior for the built-in `sftp.upload` and
`sftp.download` actions.

## Scope

This spec defines `sftp.upload` and `sftp.download`, which transfer a file
or directory between the local filesystem and a remote host over SFTP.

This spec covers:

- `with.source` and `with.destination`, and their direction-dependent
  meaning
- that `with.direction` is fixed by the action name (`sftp.upload` implies
  `direction: upload`, `sftp.download` implies `direction: download`), and
  what happens when a step also sets `with.direction` explicitly
- single-file transfer, and recursive directory transfer
- that an upload replaces an existing destination file atomically, and
  that a download does not
- file permission preservation
- that `with.source` and `with.destination` are validated only at runtime,
  not at DAG-build time
- validation and runtime errors specific to this action

This spec does not define:

- the connection fields (`with.host`, `with.port`, `with.user`,
  `with.password`, `with.key`, `with.strict_host_key`,
  `with.known_host_file`, `with.timeout`, `with.bastion`) or
  connection-level errors (refused connections, rejected authentication,
  host-key verification) -- `sftp.upload`/`sftp.download` share this
  action's connection configuration and client, so see
  [Spec 042: SSH Run Action](042-ssh-run.md) for that behavior
- direct `type: sftp` authoring, which exists (defaulting `direction` to
  `upload` when unset) but is a separate, unverified-by-this-spec path
  from the `action:` shorthand this spec documents
- symbolic link handling
- registry/host-key trust management

## Goal

Workflow authors move files to or from a remote host without shelling out
to a local `scp`/`sftp` client through a command executor.

## Behavior

### Direction, source, and destination

`sftp.upload` transfers `with.source` (a local path) to `with.destination`
(a remote path). `sftp.download` transfers `with.source` (a remote path) to
`with.destination` (a local path). Both fields are required.

The action name fixes `with.direction`: `sftp.upload` sets it to `upload`,
`sftp.download` sets it to `download`. A step may also set `with.direction`
explicitly; setting it to the value the action name already implies has no
effect, but setting it to the other value is rejected (see "Errors").

### Single-file transfer

When `with.source` names a regular file:

- Upload creates any missing remote parent directories, then writes the
  file's content to a temporary path alongside the destination and renames
  it into place, replacing any existing file at `with.destination`. An
  upload therefore never leaves a partially-written file visible at
  `with.destination`.
- Download creates any missing local parent directories, then writes the
  file's content directly to `with.destination`.

Both directions set the destination file's permission bits to match the
source file's.

### Directory transfer

When `with.source` names a directory, the whole tree is transferred
recursively, preserving its structure under `with.destination`: each file
is transferred the same way as a single-file transfer (an upload still
uses the atomic write-then-rename per file), and each subdirectory is
created at the destination before its contents are transferred.

## Errors

### Validation

- `with.source` or `with.destination` missing: a runtime error, surfaced
  only when the step actually runs (`"source path is required for sftp
  step"` / `"destination path is required for sftp step"`) -- unlike
  `ssh.run`'s `with.command`, this is not checked at DAG-build time, so
  `dagu validate` accepts a step missing either field.
- `with.direction` set to a value other than the one the action name
  implies: rejected at DAG-build-time validation, with an error containing
  `"direction must be"`.

### Runtime

- The local source does not exist (upload): the error contains `"failed
  to stat source"`.
- The remote source does not exist (download): the error contains
  `"failed to stat remote"`.
- Connection failures (refused connection, rejected authentication, and so
  on): see [Spec 042: SSH Run Action](042-ssh-run.md)'s Errors section --
  the same underlying client produces the same errors here.

## Related Specs

- Connection configuration and client: [Spec 042: SSH Run
  Action](042-ssh-run.md)
- Run context: [Spec 017: Built-In Run Context](017-built-in-run-context.md)

## Examples

Upload a single file:

```yaml
steps:
  - action: sftp.upload
    with:
      host: example.internal
      user: deploy
      key: ~/.ssh/id_ed25519
      source: ./dist/app.tar.gz
      destination: /srv/releases/app.tar.gz
```

Download a directory recursively:

```yaml
steps:
  - action: sftp.download
    with:
      host: example.internal
      user: deploy
      key: ~/.ssh/id_ed25519
      source: /var/log/myapp
      destination: ./logs/myapp
```
