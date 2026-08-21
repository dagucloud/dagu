# Spec: File Dependencies

## Status

Implemented.

## Scope

This spec defines:

- the step-level `dependencies` field
- dependency path matching and validation
- distributed workspace snapshot and materialization behavior
- retry and inline child-DAG behavior

This spec does not define:

- external tool installation
- build-workflow input materialization or reuse
- artifact persistence after a DAG-run finishes
- remote retrieval of files belonging to a separately stored child DAG

## Goal

A DAG dispatched to a worker can use scripts, configuration, and other files stored beside its authored YAML without requiring a shared filesystem.

## Behavior

### Declaration

- A step may declare `dependencies` as one string or an array of strings.
- Each string must be literal and must not contain a Dagu value reference.
- Declarations on regular steps, lifecycle handlers, foreach body steps, and every inline DAG document contribute to one snapshot for the dispatched DAG-run.

### Matching

- Paths resolve relative to the authored DAG file.
- An exact regular file selects that file.
- An exact directory selects the directory and its descendants recursively.
- A glob may use `*`, `?`, character classes, and `**`.
- Overlapping selections include each filesystem entry once.
- Every declaration must match at least one filesystem entry at dispatch time.
- Absolute paths, parent traversal, `.git` paths, symlinks, special files, and invalid glob patterns are invalid.

### Distributed execution

- A distributed start or retry snapshots the current matching entries before task dispatch.
- The snapshot contains the exact DAG definition carried by the dispatched task.
- The coordinator transports an immutable content-addressed bundle to the worker.
- The worker materializes the bundle as `DAG_RUN_WORK_DIR` and `${context.paths.work_dir}` before execution.
- The materialized directory is the implicit process working directory. An explicit DAG working directory remains the process working directory without changing `DAG_RUN_WORK_DIR`.
- The bundle must not exceed 64 MiB compressed, 256 MiB extracted, or 8192 entries.

### Child DAGs and retries

- Inline multi-document child DAGs reuse the root DAG's snapshot.
- A separately fetched named child DAG with file dependencies cannot be dispatched from a remote worker without an available source workspace.
- Each independently dispatched retry creates a fresh snapshot from the authored DAG directory.
- Local execution does not stage declared files.

## Errors

| Failure class | Required diagnostic |
| --- | --- |
| Value reference in a dependency | validation error naming `dependencies` |
| Invalid or unsafe path | dispatch rejected with the dependency path and reason |
| Declaration with no match | dispatch rejected naming the unmatched declaration |
| Unsupported filesystem entry | dispatch rejected naming the entry type and path |
| Bundle limit exceeded | dispatch rejected naming the exceeded limit |
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
