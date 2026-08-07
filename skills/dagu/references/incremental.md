# Incremental Workflows

Use `type: incremental` for local workflows that transform stable regular files and should reuse unchanged work across DAG runs. Incremental execution is materialization reuse, not a cache that restores a missing output: a missing or modified final output causes recomputation.

## Minimal Pipeline

```yaml
type: incremental
working_dir: /srv/build

steps:
  - id: compile
    inputs:
      - name: source
        path: source.c
    outputs:
      - name: binary
        path: app.bin
    run: compiler "${inputs.source}" -o "${outputs.binary}"

  - id: package
    inputs:
      - name: binary
        path: app.bin
    outputs:
      - name: archive
        path: app.tar
    run: packager "${inputs.binary}" "${outputs.archive}"
```

The `package` step does not need `depends: compile`. Its canonical input path matches the `compile` output path, so Dagu infers the dependency. Keep explicit dependencies for ordering that is not represented by a file.

## Declarations and References

- Set a stable `working_dir`, or load the DAG from a file whose directory can anchor relative paths.
- Each input requires `name` and `path`.
- A path-backed output requires `name` and `path`. It cannot also declare a value `type`.
- A step may declare at most one path-backed output.
- Names must start with a letter and contain only letters, digits, or `_`.
- Path declarations are supported only on host command and shell steps without DAG-level or step-level containers.
- Output parent directories must already exist.

| Reference | Meaning |
| --- | --- |
| `${inputs.<name>}` | Absolute final path of the owning step's declared input |
| `${outputs.<name>}` | Fresh, initially absent staging path for the current executor attempt |
| `${steps.<step_id>.outputs.<name>}` | Absolute final output path after commit or reuse |

`${inputs.*}` and `${outputs.*}` are step-scoped. Use `${outputs.<name>}` only in command text, scripts, arguments, and executor environment where an attempt staging path exists. Path declarations may use stable parameters and environment values, but must resolve before execution; do not derive them from step outputs or command substitution.

## Dependency and Path Rules

- Matching canonical producer outputs and consumer inputs create inferred dependencies.
- Each canonical output path must have exactly one producer.
- A step cannot declare the same canonical path as both input and output.
- Explicit and inferred dependencies must remain acyclic.
- Inputs and outputs must be regular, non-symlink files. Inputs must exist when the step is evaluated.

## Reuse and Publication Safety

A reusable step has exactly one path-backed output and a stable host command or shell recipe. Steps with dynamic or scalar output surfaces, secrets, repeat, human tasks, approvals, parallel or foreach bodies, child DAGs, or containers execute normally instead of being reused.

Dagu hashes the resolved recipe, declared input contents, and current output. A matching materialization produces a `reuse` decision and a succeeded node. Changed inputs, recipe, environment, tools, working directory, missing output, or modified output produce an `execute` decision.

The default per-run scratch directory is normalized in the recipe digest, so a different run ID does not prevent reuse by itself. An authored working directory remains part of the recipe and changing it requires execution.

Always write the result to `${outputs.<name>}`. Dagu verifies the staged file and input snapshots before atomically replacing the final output. Writing directly to the final output bypasses that contract. `stdout`, `stderr`, `stdout.artifact`, and `stderr.artifact` destinations cannot target any declared incremental input or output.

Each retry receives a new staging path. A failed, timed-out, or aborted attempt removes its staging file and leaves the previous final output and manifest unchanged.

## Execution Controls

Incremental workflows are local-only. Distributed execution is rejected because workers do not share the required materialization fencing.

Preview decisions without executing:

```sh
dagu dry workflow.yaml
```

Disable reuse for one run without disabling staging or commit safety:

```sh
dagu start --no-reuse workflow.yaml
dagu enqueue --no-reuse workflow.yaml
dagu dry --no-reuse workflow.yaml
```

In the Web UI, enable **Disable reuse for this run** in the Start or Enqueue dialog.
