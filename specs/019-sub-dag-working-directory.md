# Spec: Sub-DAG Working Directory

## Status

Implemented.

## Scope

This spec defines the default process working directory behavior for a child DAG
run started by `dag.run`.

It covers:

- child DAG runs started by a normal `dag.run` step
- represented child DAG runs started by `parallel` with `action: dag.run`
- the interaction between child run work directories and explicit
  `working_dir` configuration
- observable behavior of relative file writes inside child DAG steps

This spec does not define:

- `parallel` item expansion, duplicate coalescing, or aggregate outputs
- `dag.enqueue` queue processing behavior
- scheduler, coordinator, worker, or storage internals
- physical storage layout for run work directories
- automatic cleanup of work directories
- a fresh work directory for each retry attempt
- new YAML fields for inheriting or sharing parent working directories

## Goal

Child DAG runs can be launched concurrently without accidental relative-path
collisions.

A workflow author can rely on this rule: a child DAG that does not declare a
working directory writes relative files into that child run's own work
directory, not into the parent step's current working directory.

## Related Specs

- Step run: [Spec 013: Step Run](013-step-run.md)
- Built-in run context: [Spec 017: Built-In Run Context](017-built-in-run-context.md)
- Parallel fan-out: [Spec 018: Parallel Fan-Out and Foreach Iteration](018-parallel-and-foreach.md)

## Terms

A parent DAG run is the DAG run that contains the `dag.run` step.

A child DAG run is the DAG run started by a `dag.run` step.

A represented child DAG run is a child run represented by `parallel` after item
expansion and duplicate coalescing under Spec 018.

A child run work directory is the per-run work directory exposed to the child
DAG as `DAG_RUN_WORK_DIR` and `${context.paths.work_dir}`.

An explicit child working directory is a `working_dir` value declared by the
child DAG document, by child DAG base configuration, or by a step inside the
child DAG.

## Behavior

### Default Child Working Directory

- Every child DAG run has its own child run work directory.

- The child run work directory is scoped to the child DAG run, not to the parent
  DAG run.

- A child DAG run that does not have an explicit child working directory must
  use the child run work directory as the default process working directory for
  its steps.

- Starting a child DAG run must not implicitly set the child DAG's explicit
  `working_dir` from the parent step process working directory.

- Starting a child DAG run must not implicitly set the child DAG's explicit
  `working_dir` from the parent DAG run work directory.

- The parent step process working directory and the child run work directory may
  be the same path only when workflow-authored configuration explicitly selects
  the same path.

- A child DAG step process sees `DAG_RUN_WORK_DIR` for the child DAG run, not
  for the parent DAG run.

- `${context.paths.work_dir}` in a child DAG run resolves to the child run work
  directory.

### Explicit Working Directory

- Step-level `working_dir` inside the child DAG has highest precedence for that
  child step.

- An explicit root `working_dir` in the child DAG changes the default process
  working directory for child steps that do not declare step-level
  `working_dir`.

- An explicit `working_dir` inherited through child DAG base configuration is
  explicit child working directory configuration.

- Dagu must not ignore, rewrite, or isolate an explicit child working directory
  solely because the child DAG was started by `dag.run`.

- A workflow that needs parent and child steps to share a directory must set the
  child working directory explicitly.

### Parallel Child Runs

- Each represented child DAG run started by a `parallel` `dag.run` step has its
  own child run work directory.

- Distinct represented child DAG runs in the same `parallel` step must not share
  the same default child run work directory.

- If `parallel` duplicate coalescing represents multiple item slots as one child
  DAG run, that represented child run has one child run work directory.

- A relative file created by one represented child DAG run must not collide with
  the same relative file created by another represented child DAG run unless
  workflow-authored explicit working directory configuration selects a shared
  path.

### Retry Behavior

- This spec does not require a fresh process working directory for each retry
  attempt.

- Retrying a child DAG run may reuse that child run's work directory unless
  another spec defines attempt-scoped working directory behavior.

## Errors

Validation errors for malformed `working_dir` fields are defined by the owning
YAML schema and step run specs.

Runtime must fail when the selected process working directory for a child step
cannot be used as a working directory.

Runtime must not fail solely because the parent step process working directory
is unavailable to the child DAG when the child DAG has no explicit
`working_dir`.

## Examples

### Parallel Children Do Not Collide

Workflow:

```yaml
steps:
  - id: fanout
    action: dag.run
    with:
      dag: child
      params: item=${ITEM}
    parallel:
      - a
      - b

---
name: child
steps:
  - id: write
    run: |
      printf '%s\n' "$DAG_RUN_ID" > result.txt
      pwd > pwd.txt
```

Expected behavior:

- The parent step starts two represented child DAG runs.
- Both child DAG runs succeed.
- Each child run writes its own `result.txt`.
- The two child `pwd.txt` files record different default working directories.

### Parent Working Directory Is Not Inherited

Workflow:

```yaml
working_dir: parent-work

steps:
  - id: child
    action: dag.run
    with:
      dag: child

---
name: child
steps:
  - id: inspect
    run: |
      pwd > pwd.txt
      printf '%s\n' "$DAG_RUN_WORK_DIR" > run-work-dir.txt
```

Expected behavior:

- The child `pwd.txt` value is the child run work directory.
- The child `run-work-dir.txt` value is the same path as `pwd.txt`.
- The child default working directory is not `parent-work`.

### Explicit Child Working Directory Wins

Workflow:

```yaml
steps:
  - id: child
    action: dag.run
    with:
      dag: child

---
name: child
working_dir: shared-child-work

steps:
  - id: inspect
    run: pwd > pwd.txt
```

Expected behavior:

- The child step runs in `shared-child-work`.
- The explicit child `working_dir` takes precedence over the child run work
  directory.
