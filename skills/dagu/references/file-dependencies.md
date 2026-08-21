# File Dependencies

Use step-level `dependencies` when a distributed worker needs files stored beside the authored DAG.

```yaml
steps:
  - id: process
    run: ./scripts/process.sh --config config/app.yaml
    dependencies:
      - scripts/**
      - config/app.yaml
```

The field accepts one string or an array. Each item is a literal path relative to the authored DAG file and may be an exact file, a recursively included directory, or a glob using `*`, `?`, character classes, or `**`.

Every item must match during distributed dispatch. Do not use `${...}` references, absolute paths, parent traversal, `.git` paths, symlinks, or special files. All dependencies in the root DAG, lifecycle handlers, foreach bodies, and inline DAG documents share one bundle.

The worker extracts the bundle to `DAG_RUN_WORK_DIR`, also exposed as `${context.paths.work_dir}`. This is the default process working directory unless the DAG has an explicit working directory. Local execution does not stage dependencies.

Retries create a new snapshot. Separately fetched named child DAGs cannot add dependencies from a remote worker; use an inline multi-document child DAG or keep that child local.
