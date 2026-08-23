# File Dependencies

Use step-level `dependencies` when a DAG run needs files from its working directory.

```yaml
steps:
  - id: process
    run: ./scripts/process.sh --config config/app.yaml
    dependencies:
      - scripts/**
      - config/app.yaml
```

The field accepts one string or an array. Each item is a literal path relative to the DAG working directory and may be an exact file, a recursively included directory, or a glob using `*`, `?`, character classes, or `**`. When `working_dir` is omitted, the authored DAG file's directory is the source root.

Every item must match when the run workspace is prepared. Do not use `${...}` references, absolute paths, parent traversal, `.git` paths, symlinks, or special files. All dependencies in the root DAG, lifecycle handlers, foreach bodies, and inline DAG documents share one bundle.

Local execution materializes the bundle directly; distributed execution transfers it through the coordinator before worker materialization. The result is exposed as `DAG_RUN_WORK_DIR` and `${context.paths.work_dir}` and becomes the DAG process working directory. An explicit step working directory retains its normal precedence.

Retries create a new snapshot. Separately fetched named child DAGs cannot add dependencies from a remote worker; use an inline multi-document child DAG or keep that child local.
