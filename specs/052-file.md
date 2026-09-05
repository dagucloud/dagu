# Spec: File Actions

## Status

Implemented.

This spec defines conformance behavior for the built-in `file.stat`,
`file.read`, `file.write`, `file.copy`, `file.move`, `file.delete`,
`file.mkdir`, and `file.list` actions.

## Scope

This spec defines the `file.*` actions, which operate on ordinary local
filesystem paths -- unlike [Spec 051: Artifact Actions](051-artifact.md),
which confines every path to an isolated per-DAG-run directory.

This spec covers:

- `with.path`'s resolution: relative to the step's working directory
  when relative, used as-is (after `~` expansion) when absolute -- there
  is no artifact-style restriction to a sandboxed directory
- `file.write`'s required `with.content`, `with.mode`, `with.overwrite`
  (default `false`: writing to an existing path fails), `with.atomic`,
  `with.create_dirs`, and `with.dry_run`
- `file.read`'s required `with.path`, `with.format` (`raw`, the default,
  streaming the file's exact bytes; or `json`, wrapping the content with
  metadata), and `with.max_bytes`
- `file.stat`'s `with.missing_ok` (succeed, reporting `exists: false`,
  instead of failing when the path is absent)
- `file.copy` and `file.move`'s required `with.source` and
  `with.destination`, `with.recursive` (required to copy or move a
  directory), and that a destination inside the source is rejected
- `file.move`'s default requirement that the destination not already
  exist, and that the source no longer exists afterward
- `file.delete`'s `with.recursive` (required to delete a directory) and
  `with.missing_ok`
- `file.list`'s `with.pattern` (a glob), `with.recursive` (default
  `false`: only direct children), and `with.include_dirs` (default
  `false`: files only)
- that every configuration error this spec documents is rejected at
  DAG-build-time validation, not only at runtime
- validation and runtime errors

This spec does not define:

- `with.follow_symlinks`, or symlink handling generally, beyond what a
  reader needs to know that it exists
- cross-filesystem move behavior
- refusing to delete the filesystem root, beyond noting that the
  behavior exists (this spec does not exercise it directly, since doing
  so live would require actually attempting that deletion)
- direct `type: file` authoring

## Goal

Workflow authors manage ordinary files on the local filesystem as DAG
steps -- reading, writing, copying, moving, deleting, creating
directories, and listing -- without shelling out to a command executor
for what is otherwise a small, declarative file operation.

## Behavior

### Path resolution

Every path field (`with.path`, `with.source`, `with.destination`) is
resolved the same way: relative to the step's working directory when it
is relative, or used directly (after expanding a leading `~`) when it is
absolute. Unlike `artifact.*`, there is no restriction keeping a path
inside any particular directory.

### Write

`file.write` requires `with.content` and `with.path`. `with.mode` sets
the new file's permission bits (an octal string such as `"0640"`);
it defaults to `0600`. Without `with.overwrite`, writing to a path that
already exists fails; with `with.overwrite: true`, the existing file is
replaced (`with.atomic`, default `true`, governs how). `with.create_dirs:
true` creates missing parent directories first. `with.dry_run: true`
reports what would happen (`created: false`) without writing anything.

### Read and stat

`file.read` requires `with.path`. `with.format: raw` (the default)
copies the file's exact bytes to stdout; `with.format: json` instead
writes one JSON object with the file's path, type, size, mode,
modification time, and content. `with.max_bytes`, when greater than
zero, rejects a file larger than that many bytes. Reading a directory
fails.

`file.stat` reports the same metadata (without content) for any path.
`with.missing_ok: true` makes a missing path succeed, with `exists:
false`, instead of failing.

### Copy and move

`file.copy` and `file.move` require `with.source` and
`with.destination`; the two must differ. Copying or moving a directory
requires `with.recursive: true`; a destination inside the source is
rejected. `file.move`, without `with.overwrite`, fails if the destination
already exists; on success, the source no longer exists.
`with.create_dirs: true` creates missing destination parent directories
first. `with.dry_run: true` reports the intended operation without
performing it.

### Delete and mkdir

`file.delete` requires `with.path`. Deleting a directory requires
`with.recursive: true`. `with.missing_ok: true` makes a missing path
succeed, with `deleted: false`, instead of failing. `file.mkdir` creates
`with.path` (and any missing parents), applying `with.mode` when set.

### List

`file.list` requires `with.path` (the directory to list) and writes one
JSON object naming the count of files found and an array of entries
(each with path, type, size, mode, and modification time), sorted by
path. `with.recursive: true` descends into subdirectories; otherwise
only direct children are listed. `with.include_dirs: true` includes
directory entries; otherwise only regular files are listed.
`with.pattern`, when set, is a glob matched against each candidate's
slash-separated path relative to `with.path`.

## Errors

### Validation

Every one of these is rejected at DAG-build-time validation (`dagu
validate`), not only when the step runs:

- `file.write` without `with.content`.
- `file.stat`/`file.read`/`file.delete`/`file.mkdir`/`file.list` without
  `with.path`.
- `file.copy`/`file.move` without `with.source` or `with.destination`.

### Runtime

- `file.write` to an existing path without `with.overwrite`: an error
  containing `"file exists"`.
- `file.read` on a file larger than `with.max_bytes`: an error
  containing `"exceeds max_bytes"`.
- `file.read` on a directory: an error containing `"cannot read a
  directory"`.
- `file.copy`/`file.move` on a directory without `with.recursive`: an
  error containing `"recursive is required to copy a directory"` (or
  `"...move..."`).
- `file.copy` with a destination inside the source: an error containing
  `"destination must not be inside source"`.
- `file.move` to an existing destination without `with.overwrite`: an
  error containing `"destination exists"`.
- `file.delete` on a directory without `with.recursive`: an error
  containing `"recursive is required to delete a directory"`.

## Related Specs

- Artifact-scoped file actions: [Spec 051: Artifact
  Actions](051-artifact.md)
- Run context: [Spec 017: Built-In Run Context](017-built-in-run-context.md)

## Examples

Copy a directory tree, creating the destination's parents if needed:

```yaml
steps:
  - action: file.copy
    with:
      source: ./build/output
      destination: /srv/releases/latest
      recursive: true
      create_dirs: true
      overwrite: true
```

Check whether a file exists before deciding what to do next:

```yaml
steps:
  - id: check
    action: file.stat
    with:
      path: ./cache/result.json
      missing_ok: true
```
