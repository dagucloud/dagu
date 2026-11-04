# Spec: Archive Actions

## Status

Partially implemented.

Conformance covers one small ZIP create/list/extract workflow, create dry-run
output suppression, relative paths resolved from the step working directory
(including extraction with no destination), and representative invalid
configurations. Executor tests
own format matrices, filtering, permissions, malformed archives, and path
safety edge cases.

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
- `with.dry_run` (create and extract): reports counts without writing archive
  or extracted payload files, but still reads sources and requires a determinable
  format; extraction directory behavior is outside conformance scope
- that path-escaping entries inside an archive (zip-slip) are rejected
  during extraction
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

A relative `with.source` or `with.destination` names a path under the step
working directory. `archive.extract` without `with.destination` extracts into
the step working directory.

### Format selection

For `archive.create`, an explicit `with.format` always wins, even if it
conflicts with `with.destination`'s extension (for example,
`destination: out.dat` with `format: zip` produces a genuine zip file).
Without `with.format`, the format is inferred from `with.destination`'s
extension (`.zip`, `.tar.gz`, `.tgz`, `.tar.bz2`, `.tar.xz`,
`.tar.zst`, `.gz`, `.bz2`, `.xz`, `.zst`, `.lz4`, and others).

For `archive.extract` and `archive.list`, format detection uses source
content and the filename. Invalid or unsupported archives fail. Exact
third-party parser diagnostics are outside this contract.

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

`with.dry_run: true` suppresses archive creation and extracted payload
writes. Sources are still read to determine formats and counts. The result
reports would-be counts (`filesAdded`/`bytesArchived` for create,
`filesExtracted`/`bytesExtracted` for extract).

Conformance checks that a configured create dry run leaves its archive
destination absent. Extraction directory behavior and dry-run count
permutations are outside this partial conformance scope.

### Path safety

Extracting an archive containing an entry whose name would resolve
outside `with.destination` (a "zip-slip" entry, such as one named
`../escape.txt`) is rejected without writing that entry outside the
destination. This contract does not require transactional extraction or
rollback of entries already extracted.

## Errors

### Validation

Invalid configurations fail with a nonzero exit status:

- Any `archive.*` action without `with.source` fails `dagu validate`.
- Negative `with.strip_components` fails `dagu validate`.
- `archive.create` requires `with.destination` unless `with.dry_run: true`.
- `with.password` is valid only for extract/list operations.

This spec does not require invalid configurations to pass validation before
failing at runtime.

### Runtime

- `archive.extract`/`archive.list` on a source that does not exist: an
  error containing `"source not found"`.
- `archive.extract`/`archive.list` on invalid archive content fails with a
  nonzero exit status.
- `archive.create` with neither `with.format` nor a destination
  extension to infer from (including under `with.dry_run`): an error
  containing `"could not infer format"`.
- `archive.extract` into an existing destination without
  `with.overwrite`: an error containing `"exists (overwrite disabled)"`.
- `archive.extract` of an archive containing a path-escaping entry: an
  error containing `"escapes destination"`; no file is written outside
  `with.destination`.

## Related Specs

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
