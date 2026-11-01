# Spec 064: Dry-Run Step Checks

## Status

Implemented.

## Scope

This specification covers the executable-access checks `dagu dry` performs for
local command steps, and the way an executor registers a check of its own,
such as the xlsx actions' check that a workbook, sheet, or column exists
(Spec 077). It does not define checks for executors that run off the host,
such as containers, SSH, or remote jobs. It does not define name resolution
inside shells that are not Unix-like, checks for names containing shell
syntax or run-time references, or shell syntax validation.

## Goal

Surface inaccessible commands and shells without executing a workflow, without
blocking a run whose executables exist only where it actually runs.

## Behavior

For a local command step, `dagu dry` emits a warning when:

- The step's shell does not resolve on the host.
- A command name run directly or through a Unix-like shell does not resolve on
  `PATH` and is not a shell builtin.
- A path-form command does not exist or, on systems with executable permission
  bits, is not executable.

A warning never fails the dry run. `dagu dry` exits with status 0, reports the
step as succeeded, starts no step process, and creates no step output files.
The real run may execute on another host, and an upstream step may create or
install the executable first.

A step that passes these checks produces no warning.

An executor may register a dry-run check of its own. It receives the step
with its `with` fields resolved as far as a dry run can: params and
environment resolve, while a reference to a step output, which no step has
produced in a dry run, is left as written and the check skips that field.
The check reports what the real run would fail on that the host can show
now, such as a file the step reads that does not exist, and never what it
cannot judge, such as a file another program holds. Every problem the check
finds is listed in the one warning, and the command checks above still run
for the same step.

## Diagnostics

Each warning is written to stderr, contains
`Dry run: step may fail on this host`, and names the unresolved command, shell,
or script, or the `with` field an executor's check found wanting, as
`field 'with.path': orders.xlsx: workbook not found`.

`--format text` is the default. It leaves those warnings on stderr and prints
the run summary on stdout.

`--format json` prints one JSON object on stdout and no run summary. The
warnings stay on stderr. `checks` lists every problem, in step order, and is
empty when every step passes. Several problems of one step are several
objects, in the order the check reported them. `step` is the step name,
`field` is the name inside the warning (`with.sheet`, `run`, `shell`), and
`message` is the text after that field label. The exit status is unchanged.

```json
{"checks": [{"step": "read", "field": "with.sheet", "message": "orders.xlsx: sheet \"Order\" not found; sheets present: Orders, Summary"}]}
```

## Examples

```yaml
steps:
  - run: missing-command --flag
```

`dagu dry` exits with status 0. Stderr contains
`Dry run: step may fail on this host` and `missing-command`.

## Conformance

`conformance/spec064_dry_run_step_checks/` covers a missing command, a missing
step-level shell, and a non-executable script invoked by path, each producing a
warning with exit status 0 and no step output. It also covers a resolvable
command, shell, and executable script producing no warning. The execute
permission checks are skipped on Windows. `--format json` is covered by a DAG
whose xlsx and file steps each produce one check, and by a dry run that
produces none. The xlsx actions' registered check is covered in
`conformance/spec077_xlsx/`.
