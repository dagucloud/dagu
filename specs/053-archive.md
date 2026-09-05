# Spec: Archive Actions

## Status

Implemented.

This spec defines conformance behavior for the built-in `archive.create`,
`archive.extract`, and `archive.list` actions.

## Scope

This spec covers:

- `archive.create`'s required `with.source` and `with.destination`
  (except under `with.dry_run`), and format selection: an explicit
  `with.format` always wins; otherwise the format is inferred from
  `with.destination`'s file extension
- `archive.extract`'s required `with.source`, and format detection by
  content sniffing (with a filename hint) rather than by extension alone
- `archive.list`'s required `with.source`, reporting archive contents
  without extracting anything
- `with.strip_components` (extract) and `with.preserve_paths` (extract,
  default `true`)
- `with.include` and `with.exclude` (extract and list): glob filters over
  archive entry paths
- `with.overwrite` (extract, default `false`): whether extracting into an
  existing destination is allowed
- `with.password`: meaningful only for 7z and rar archives, and only for
  `archive.extract`/`archive.list`
- `with.dry_run` (create and extract): reports counts without performing
  file I/O, but still requires a determinable format
- that path-escaping entries inside an archive (zip-slip) are rejected
  during extraction
- the split between build-time and runtime validation for this executor,
  which differs from other built-in actions
- validation and runtime errors

This spec does not define:

- the specific set of archive formats and compression algorithms
  supported (this is an implementation detail of the underlying archive
  library), beyond the formats exercised by this spec's examples
- `with.compression_level`, `with.verbose`, `with.follow_symlinks`,
  `with.verify_integrity`, or `with.continue_on_error`, beyond noting
  that they exist
- behavior for 7z or rar archives specifically (this spec does not
  construct or exercise password-protected 7z/rar fixtures)

## Goal

Workflow authors create, extract, and inspect common archive formats
(zip, tar, tar.gz, and others) as DAG steps, without shelling out to
`tar`, `zip`, or `unzip`.

## Behavior

### Format selection

For `archive.create`, an explicit `with.format` always wins, even if it
conflicts with `with.destination`'s extension (for example,
`destination: out.dat` with `format: zip` produces a genuine zip file).
Without `with.format`, the format is inferred from `with.destination`'s
extension (`.zip`, `.tar.gz`, `.tgz`, `.tar.bz2`, `.tar.xz`,
`.tar.zst`, `.gz`, `.bz2`, `.xz`, `.zst`, `.lz4`, and others).

For `archive.extract` and `archive.list`, the format is detected by
sniffing the source file's content (using a filename hint), not solely
by its extension. A file bearing a `.zip` extension that is not
actually a valid zip file fails with a parse error from the underlying
format library (for example, `"zip: not a valid zip file"`), not a
generic "format detection failed" message -- the extension is trusted
enough to attempt parsing, and the failure surfaces at the parse stage.

### Create

`archive.create` requires `with.source`. `with.destination` is required
unless `with.dry_run: true` -- but even under `with.dry_run`, a format
must still be determinable from either an explicit `with.format` or a
non-empty `with.destination`'s extension; if neither is available,
`archive.create` fails even in dry-run mode.

### Extract

`archive.extract` requires `with.source`. Without `with.overwrite`,
extracting into a destination that already contains the extracted path
fails; with `with.overwrite: true`, existing files are replaced.

`with.strip_components` (an integer, minimum `0`) strips that many
leading path components from each extracted entry's name.
`with.preserve_paths: false` flattens every extracted entry to just its
basename inside the destination root, discarding any directory
structure the archive recorded.

`with.include` and `with.exclude` (arrays of doublestar glob patterns)
filter which archive entries are written; entries that don't match
`with.include` (when set) or that match `with.exclude` are skipped and
counted in the result's `filesSkipped`, not extracted.

### List

`archive.list` reports an archive's contents (path, size, mode,
modification time, and whether each entry is a directory) without
extracting anything. `with.include` and `with.exclude` filter which
entries are reported, the same way they filter extraction.

### Password

`with.password` only affects 7z and rar archives; it is silently
ignored for zip and tar-based formats. It is only a valid configuration
for `archive.extract` and `archive.list` -- setting it for
`archive.create` is a runtime error.

### Dry run

`with.dry_run: true`, for both `archive.create` and `archive.extract`,
performs no actual file I/O: no archive file is created, and no
destination directory or file is created for extraction. The result
still reports accurate would-be counts (`filesAdded`/`bytesArchived` for
create, `filesExtracted`/`bytesExtracted` for extract).

### Path safety

Extracting an archive containing an entry whose name would resolve
outside `with.destination` (a "zip-slip" entry, such as one named
`../escape.txt`) is rejected before any file is written, regardless of
what the archive format itself permits.

## Errors

### Validation

The registered step validator for `archive.*` checks only that the
operation name (`command`) is non-empty; it does not rerun the full
custom configuration validation. As a result, only the following are
rejected at DAG-build-time validation (`dagu validate`), enforced by
JSON Schema:

- Any `archive.*` action without `with.source`: an error mentioning
  `missing properties: ["source"]`.
- `with.strip_components` set to a negative integer: an error
  mentioning the schema's `minimum` constraint.

All other configuration errors below pass `dagu validate` and surface
only when the step actually runs:

- `archive.create` without `with.destination` and without
  `with.dry_run: true`: an error containing `"destination is required
  for create"`.
- `with.password` set on `archive.create`: an error containing
  `"password is only supported for extract/list operations"`.

### Runtime

- `archive.extract`/`archive.list` on a source that does not exist: an
  error containing `"source not found"`.
- `archive.extract`/`archive.list` on a source whose content does not
  match its apparent format: a parse error from the underlying archive
  library (for example, `"zip: not a valid zip file"`).
- `archive.create` with neither `with.format` nor a destination
  extension to infer from (including under `with.dry_run`): an error
  containing `"could not infer format"`.
- `archive.extract` into an existing destination without
  `with.overwrite`: an error containing `"exists (overwrite disabled)"`.
- `archive.extract` of an archive containing a path-escaping entry: an
  error containing `"escapes destination"`; no file is written outside
  `with.destination`.

## Related Specs

- File actions: [Spec 052: File Actions](052-file.md)
- Run context: [Spec 017: Built-In Run Context](017-built-in-run-context.md)

## Examples

Create a zip archive from a directory, then extract it elsewhere with
its leading path component stripped:

```yaml
steps:
  - id: pack
    action: archive.create
    with:
      source: ./build/output
      destination: ./dist/release.zip

  - id: unpack
    depends: pack
    action: archive.extract
    with:
      source: ./dist/release.zip
      destination: ./staging
      strip_components: 1
```

List an archive's contents, restricted to a subset of files, without
extracting anything:

```yaml
steps:
  - action: archive.list
    with:
      source: ./dist/release.zip
      include:
        - "**/*.json"
```
