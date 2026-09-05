# Spec: Artifact Actions

## Status

Implemented.

This spec defines conformance behavior for the built-in `artifact.write`,
`artifact.read`, and `artifact.list` actions.

## Scope

This spec defines `artifact.write`, `artifact.read`, and `artifact.list`,
which write, read, and list files in a DAG-run's own artifact directory.

This spec covers:

- that using any `artifact.*` action auto-enables artifacts for the DAG,
  and that explicitly setting `artifacts.enabled: false` while also using
  one is a conflict
- `with.path`, relative to the artifact directory, and that it must stay
  within it (no absolute paths, no `~`, no `..` segments)
- `artifact.write`'s required `with.content`, `with.mode`, and
  `with.overwrite` (default `false`: writing to an existing path fails)
  with `with.atomic` (default `true`; `overwrite: true` requires it)
- `artifact.read`'s required `with.path`, `with.format` (`raw`, the
  default, streaming the file's exact bytes; or `json`, wrapping the
  content with metadata), and `with.max_bytes`
- `artifact.list`'s `with.pattern` (a glob), `with.recursive` (default
  `false`: only direct children), and `with.include_dirs` (default
  `false`: files only)
- that every configuration error this spec documents is rejected at
  DAG-build-time validation, not only at runtime
- validation and runtime errors

This spec does not define:

- `artifacts.dir`, the DAG-level option to relocate the artifact
  directory (this spec only covers its default location)
- symlink-based escape attempts, beyond the same `..`/absolute-path
  protections already covered
- direct `type: artifact` authoring

## Goal

Workflow authors persist and retrieve files scoped to a single DAG-run,
without shelling out to a command executor for file management, and
without accidentally writing outside that run's own storage.

## Behavior

### Enabling artifacts

Using any `artifact.write`/`artifact.read`/`artifact.list` step
auto-enables artifact storage for the DAG; no separate configuration is
needed. Setting `artifacts.enabled: false` while a step also uses an
artifact action is rejected (see "Errors").

### Path safety

`with.path` is always relative to the DAG-run's own artifact directory.
An absolute path, a path starting with `~`, or one containing a `..`
segment is rejected, regardless of which action uses it.

### Write

`artifact.write` requires `with.content` (the file's new content) and
`with.path`. `with.mode` sets the file's permission bits (an octal
string such as `"0640"`); it defaults to `0600`. Without
`with.overwrite`, writing to a path that already exists fails; with
`with.overwrite: true`, the existing file is replaced. `with.atomic`
(default `true`) governs how a replacement is written; `with.overwrite:
true` requires it and cannot be combined with `with.atomic: false`.

### Read

`artifact.read` requires `with.path`. `with.format: raw` (the default)
copies the file's exact bytes to stdout. `with.format: json` instead
writes one JSON object with the file's path, type, size, mode,
modification time, and its content as a string. `with.max_bytes`, when
greater than zero, rejects a file larger than that many bytes rather
than reading it. Reading a path that is a directory fails.

### List

`artifact.list` writes one JSON object naming the count of files found
and an array of entries (each with path, type, size, mode, and
modification time), sorted by path. `with.path` (default: the artifact
directory's root) names the directory to list. `with.recursive: true`
descends into subdirectories; otherwise only direct children are listed.
`with.include_dirs: true` includes directory entries in the result;
otherwise only regular files are listed. `with.pattern`, when set, is a
glob matched against each candidate's slash-separated relative path.

## Errors

### Validation

Every one of these is rejected at DAG-build-time validation (`dagu
validate`), not only when the step runs:

- `artifact.write` without `with.content`.
- `artifact.read` without `with.path`.
- `with.overwrite: true` combined with `with.atomic: false`.
- `artifacts.enabled: false` set while a step uses an artifact action.

### Runtime

- `with.path` is absolute, starts with `~`, or contains a `..` segment:
  an error containing `"artifact path must be relative"` or `"artifact
  path must not contain .."`.
- `artifact.write` to an existing path without `with.overwrite`: an
  error containing `"file exists"`.
- `artifact.read` on a file larger than `with.max_bytes`: an error
  containing `"exceeds max_bytes"`.
- `artifact.read` on a directory: an error containing `"cannot read a
  directory"`.

## Related Specs

- Run context: [Spec 017: Built-In Run Context](017-built-in-run-context.md)

## Examples

Write a file, then read it back as JSON metadata plus content:

```yaml
steps:
  - id: save
    action: artifact.write
    with:
      path: report.json
      content: '{"status":"ok"}'
  - id: inspect
    depends: save
    action: artifact.read
    with:
      path: report.json
      format: json
```

List every `.log` file under a subdirectory:

```yaml
steps:
  - action: artifact.list
    with:
      path: logs
      pattern: "**/*.log"
      recursive: true
```
