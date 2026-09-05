# Spec: DuckDB Action

## Status

Implemented.

This spec defines conformance behavior for the official `duckdb@v1` Dagu
Action, and, since its behavior is entirely governed by the underlying
mechanism, the built-in `action` executor's resolution and validation of
a versioned action reference.

## Scope

This spec defines `action: duckdb@v1`, the official Dagu Action that runs
a SQL statement with the DuckDB CLI, and the reference-resolution and
input/output-validation behavior every action reference (official,
GitHub, or `source:`) goes through to run it.

This spec covers:

- `action: duckdb@v1`'s own `with.query` field, and its JSON output
  shape: `{"result": "<raw duckdb -json output>"}`
- that resolving any action reference (official `name@version`, GitHub
  `owner/repo@version`, or `source:target@version`) is a runtime step,
  not a DAG-build-time one: `dagu validate` only checks the reference's
  syntax
- an action's `with.input` being validated against its manifest's
  `inputs` JSON Schema before it runs, and its result being validated
  against the manifest's `outputs` JSON Schema before it is reported
- that an action DAG must not set `working_dir` itself
- the reference syntaxes this spec's fixtures exercise directly through
  `source:`, standing in for the same resolution official and GitHub
  references share: an official action resolves to
  `github.com/dagucloud/<name>.git`; a `source:` reference resolves to a
  local directory when its target is one, and otherwise to a git clone
- validation and runtime errors

This spec does not define:

- `dagu-action.yaml` manifest fields beyond `dag`, `inputs`, and
  `outputs`
- the `tools:` mechanism an action DAG (such as `duckdb@v1`'s own
  workflow) uses to provision a CLI it depends on
- DuckDB's own SQL dialect or CLI behavior beyond what `duckdb@v1`
  passes through
- `pkg:` action references, which are rejected outright and not
  implemented
- workspace bundling/packing details of how an action's files reach its
  sub-DAG run

## Goal

Workflow authors run a versioned, shareable unit of behavior -- such as a
DuckDB query -- as a single step, the same way regardless of whether it
is authored by the Dagu team, a GitHub user, or a local, explicit source.

## Behavior

### Reference resolution

`action: <ref>` accepts three forms: an official action, `name@version`
(no `/`), which resolves to `https://github.com/dagucloud/<name>.git` at
that version; a GitHub action, `owner/repo@version`; and an explicit
source action, `source:target@version`, whose `target` is a local
directory (an absolute path, a path relative to the step's
`working_dir`, or a `file://` URL) when one exists at that location, and
otherwise a git remote to clone.

Resolving a reference -- checking out the right git ref, or reading a
local directory -- happens when the step runs, not when the DAG is
built: `dagu validate` only checks that the reference's own syntax is
well-formed (see "Errors"), so a reference to a repository or path that
does not exist still passes validation and only fails when the step
actually runs.

### Input, execution, and output

The resolved bundle's `dagu-action.yaml` manifest names a DAG file (run
as a sub-DAG, with `with.input` passed to it as its params) and,
optionally, JSON Schemas for `inputs` and `outputs`. `with.input` is
validated against the `inputs` schema before the sub-DAG runs; the
sub-DAG's own output values are validated against the `outputs` schema
before being written to stdout as one JSON object.

An action's DAG must not set `working_dir` itself, since it always runs
in its own sub-DAG attempt work directory.

`duckdb@v1` itself takes `with.query` (a SQL statement passed to
`duckdb -c`) and reports `{"result": "<raw duckdb -json output>"}` --
the DuckDB CLI's own `-json`-format stdout, as a string.

## Errors

### Validation

- A versioned reference that is not a well-formed official action,
  GitHub action, or `source:` reference (a missing `@version`, or an
  invalid name/owner/repo): rejected at DAG-build-time validation, with
  an error containing `"versioned action references must use official
  action@version or GitHub owner/repo@version"` (or, for a malformed
  `source:` reference specifically, `"source action references must use
  source:target@version"`).
- A bare action name that is neither a registered built-in action nor a
  versioned reference: rejected at DAG-build-time validation, with an
  error containing `"unknown action"`.

### Runtime

- `with.input` does not match the manifest's `inputs` schema (a missing
  required field, or an unexpected one): a runtime error containing
  `"action input does not match inputs schema"`.
- The action DAG's output does not match the manifest's `outputs`
  schema: a runtime error containing `"action output does not match
  outputs schema"`.
- The action DAG sets `working_dir` itself: a runtime error containing
  `"must not set working_dir"`.
- The bundle has no `dagu-action.yaml`: a runtime error containing
  `"read action manifest"`.
- A `source:` target that is neither an existing local directory nor a
  resolvable git remote: a runtime error containing `"clone action
  source"`.

## Related Specs

- Run context: [Spec 017: Built-In Run Context](017-built-in-run-context.md)

## Examples

Run a DuckDB query as a step:

```yaml
steps:
  - action: duckdb@v1
    with:
      query: "SELECT 42 AS answer, 'duckdb' AS engine;"
```

Reference an explicit local or git source instead of an official or
GitHub-hosted action:

```yaml
steps:
  - action: "source:./actions/my-action@v1"
    with:
      message: hello
```
