# Spec 064: Dry-Run Step Checks

## Status

Not implemented. This proposed contract requires executor-aware dry-run
validation before its conformance tests can pass.

## Scope

This proposed contract covers direct command steps. Other executors and shell
language constructs are outside this suite.

## Goal

Find inaccessible commands and shells without executing a workflow.

## Behavior

For a command step, `dagu dry` should:

- Accept a resolvable command and shell without executing the step or creating
  its output files.
- Reject an unresolved command or shell and identify it in the diagnostic.
- On systems with executable permission bits, reject a non-executable script
  invoked directly as a command.

## Errors

Access failures must identify the command or shell and return a nonzero exit
status. Timeout, abort, and cleanup during execution are outside dry validation.

## Examples

```yaml
steps:
  - run: missing-command
```

## Conformance

`conformance/spec064_dry_run_step_checks/` contains the proposed acceptance
checks. The negative checks remain feature-dependent. This suite does not
establish validation rules for other executors or shell language constructs.
