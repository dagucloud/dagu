# Spec: File Dependencies

## Status

Implemented.

## Scope

This spec defines:

- the step-level `dependencies` field
- dependency path matching and validation
- local and distributed workspace snapshot and materialization behavior
- retry and inline child-DAG behavior

This spec does not define:

- external tool installation
- build-workflow input materialization or reuse
- artifact persistence after a DAG-run finishes
- remote retrieval of files belonging to a separately stored child DAG

## Goal

A DAG can use declared files from its working directory with the same isolated workspace behavior in local and distributed execution.

## Behavior

### Declaration

- A step may declare `dependencies` as one string or an array of strings.
- Each string must be literal and must not contain a Dagu value reference.
- Declarations on regular steps, lifecycle handlers, foreach body steps, and every inline DAG document contribute to one snapshot for the dispatched DAG-run.

### Matching

- Paths resolve relative to the DAG working directory. When `working_dir` is omitted, the authored DAG file's directory is the source root.
- An exact regular file selects that file.
- An exact directory selects the directory and its descendants recursively.
- A glob may use `*`, `?`, character classes, and `**`.
- Overlapping selections include each filesystem entry once.
- Every declaration must match at least one filesystem entry when the run workspace is prepared.
- Absolute paths, parent traversal, `.git` paths, symlinks, special files, and invalid glob patterns are invalid.

### Workspace execution

- A local or distributed start or retry snapshots the current matching entries before execution.
- The snapshot contains the exact DAG definition carried by the dispatched task.
- Local execution materializes the snapshot directly. Distributed execution transports the immutable content-addressed bundle through the coordinator before the worker materializes it.
- The snapshot is materialized as `DAG_RUN_WORK_DIR` and `${context.paths.work_dir}` before execution.
- The materialized directory is the process working directory for the DAG. An explicit step working directory retains its normal precedence.
- The bundle must not exceed 64 MiB compressed, 256 MiB extracted, or 8192 entries.

### Child DAGs and retries

- Inline multi-document child DAGs reuse the root DAG's snapshot.
- A separately fetched named child DAG with file dependencies cannot be dispatched from a remote worker without an available source workspace.
- Each independently executed retry creates a fresh snapshot from the DAG working directory.

## Errors

| Failure class | Required diagnostic |
| --- | --- |
| Value reference in a dependency | validation error naming `dependencies` |
| Invalid or unsafe path | workspace preparation fails with the dependency path and reason |
| Declaration with no match | workspace preparation fails naming the unmatched declaration |
| Unsupported filesystem entry | workspace preparation fails naming the entry type and path |
| Bundle limit exceeded | workspace preparation fails naming the exceeded limit |
| Worker download, verification, or extraction failure | DAG-run fails before step execution |

## Examples

```yaml
steps:
  - id: backup
    run: ./scripts/backup.sh --config config/app.yaml
    dependencies:
      - scripts/**
      - config/app.yaml
```

```yaml
steps:
  - id: import
    run: python scripts/import.py
    dependencies: scripts/import.py
```
