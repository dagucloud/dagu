# Spec 064: Dry-Run Step Checks

## Status

Implemented.

## Scope

This specification covers the executable-access checks `dagu dry` performs for
local command steps. It does not define checks for executors that run off the
host, such as containers, SSH, or remote jobs. It does not define name
resolution inside shells that are not Unix-like, checks for names containing
shell syntax or run-time references, or shell syntax validation.

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

## Diagnostics

Each warning is written to stderr, contains
`Dry run: step may fail on this host`, and names the unresolved command, shell,
or script.

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
permission checks are skipped on Windows.
