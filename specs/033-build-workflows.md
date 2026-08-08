# Spec: Build Workflows

## Status

Implemented for local execution. Distributed materialization requires the
coordinator fencing protocol and is not part of this spec revision.

## Scope

An opt-in `type: build` DAG may declare named regular-file inputs and a
path-backed output on a step. Dagu infers dependencies from matching canonical
paths and evaluates each ready node against its last committed materialization.

## Declarations

```yaml
type: build
working_dir: /workspace

steps:
  - id: compile
    name: compile
    inputs:
      - name: source
        path: source.txt
    outputs:
      - name: artifact
        path: artifact.bin
    run: compiler "${inputs.source}" -o "${outputs.artifact}"
```

Input and output names use `^[A-Za-z][A-Za-z0-9_]*$` and are unique within the
step. A path output and a value-output `type` are mutually exclusive. A step may
declare at most one path output.

Relative paths resolve against an authored/default working directory, or the
source DAG directory for a file-backed definition. Inline YAML without one of
those stable bases is invalid. Paths never resolve against per-run scratch
space.

Only host command and shell steps may declare build paths. DAG- or
step-level containers and other executor types are invalid for those steps.

## Planning and references

Paths are canonicalized before execution. If an input path matches another
step's output path, the plan gains a producer-to-consumer dependency. Explicit
and inferred edges are combined and the resulting graph must be acyclic. Each
canonical output path must have exactly one producer, so two steps may not use
equivalent spellings such as `artifact.bin` and `./artifact.bin`.

`${inputs.<name>}` resolves to the final absolute input path inside its owning
step. `${outputs.<name>}` resolves to a fresh, absent sibling staging path for
each executor attempt. It is valid only in command text, scripts, arguments,
and executor environment. After commit or reuse,
`${steps.<id>.outputs.<name>}` publishes the final absolute output path.

## Decisions

Every build-DAG node records a decision independently of its lifecycle
status:

- `reuse`: the recipe, input contents, manifest, and current output match;
- `execute`: execution is required by a miss, changed content, or `no_reuse`;
- `always`: the step is not reuse-eligible and executes normally;
- `deferred`: dry-run cannot decide until an upstream producer executes;
- `none`: evaluation did not reach an execute-or-reuse decision.

A reused node is `succeeded`, never `skipped`. A reusable step has exactly one
path output and no dynamic, scalar, secret-derived, repeating, human, parallel,
foreach, sub-DAG, or container output surface. Other valid steps run with the
`always` decision.

## Materialization safety

The recipe digest includes the resolved command/script configuration,
parameters, non-secret environment, declarations, platform, tools, and
effective working directory. A working directory under per-run scratch is
recorded relative to a stable scratch marker, so a new run ID alone does not
invalidate an otherwise identical recipe. Regular input and output files are
hashed with SHA-256.

Evaluation holds shared input locks and an exclusive output lock through
preconditions, execution, verification, and commit. Each retry uses a new
same-directory staging path. A successful attempt is published with a
recoverable output-and-manifest journal. Failure leaves the previous final
output and manifest intact. Recovery refuses to overwrite a final output that
matches neither the previous nor the proposed materialization.

Standard-stream destinations, including artifact destinations, must not resolve
to any declared build input or output. Static aliases are rejected during
planning, and late-resolved aliases are rejected before opening the destination.

`--no-reuse` on `start`, `dry`, or `enqueue` bypasses manifest hits without
bypassing staging or commit safety. Dry-run only previews decisions: it creates
no locks, staging files, manifests, or run-history records.

## Surfaces

Persisted status and the REST API expose the decision, phase, reason, detail,
fingerprint, materialization key, and producer run when available. CLI status
and the Web UI display reuse separately from the existing node status.
