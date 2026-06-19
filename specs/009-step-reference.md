# Spec: Step Reference

## Status

Implemented.

## Scope

This spec defines executable step identity and dependency references inside one
DAG document.

It also defines which step identity is used by strict step-output references in
value resolution.

This spec does not define how a step runs, how a step publishes outputs, how
step output values are parsed, or how lifecycle handler steps run.

## Goal

A workflow author can safely rename display-oriented step names without breaking
strict `${steps.<id>.outputs.<name>}` references.

`name` is the runtime step identity. `id` is an optional stable alias for strict
step-output references.

## Motivation

Workflow authors often need a human-readable step name for status views and a
stable machine-readable identifier for references.

This spec separates those identities so display text can change without
breaking value references. It also defines how dependencies match steps, so
validation and execution use the same step graph.

## Input

Input is a workflow YAML file accepted by the YAML schema spec.

Step reference validation extends:

```sh
dagu validate <path/to/dag_file>
```

Rules:

- Validation checks step ids, step names, dependency references, and statically
  inspectable step-output references.
- Validation must not execute steps.
- Passive value-reference notices are inspection results. They are not
  validation failures.

## Behavior

### Step Identity

Identity rules apply to executable steps that result from workflow loading.

| Field | Meaning | Rules |
| --- | --- | --- |
| `name` | Runtime step identity and display text. | Required after loading. Must be unique inside one DAG document. Must be at most 255 UTF-8 bytes. |
| `id` | Optional stable value-reference alias. | Must be unique inside one DAG document when present. Must match `^[A-Za-z][A-Za-z0-9_]*$`. Must be at most 40 UTF-8 bytes. |

Rules:

- `name` and `id` are trimmed before identity validation.
- Empty or whitespace-only `name` is treated as missing after trimming.
- If `name` is missing and `id` is present, Dagu uses `id` as the runtime step
  name.
- If both `name` and `id` are missing, Dagu assigns a Dagu-owned runtime name.
  Workflow authors should not depend on that generated name. Add an explicit
  `name` or `id` before referencing the step.
- A step `id` must not use a reserved word. Reserved-word checks are
  case-insensitive.
- Reserved step ids are `env`, `params`, `args`, `stdout`, `stderr`, `output`,
  and `outputs`.
- A step `id` must not equal another step's `name`.
- A step `name` must not equal another step's `id`.
- One step may use the same value for both `name` and `id`.
- Step `name` matching is exact and case-sensitive after trimming.
- Step `id` matching is exact and case-sensitive after trimming.
- Step identity is scoped to one DAG document.
- Inline sub-DAG documents have independent step identity scopes.
- A dependency or strict step-output reference cannot cross a DAG document
  boundary.

### Dependencies

`depends` declares ordering between executable steps in the same DAG document.

Rules:

- `depends` may be omitted or null.
- Null `depends` is treated as omitted.
- In graph DAGs, `depends` may be a string or a sequence.
- In graph DAGs, an empty `depends` sequence means the step explicitly has no
  dependencies.
- Sequence entries are converted to text before dependency lookup. Workflow
  authors should use strings to avoid surprising YAML type conversion.
- In chain DAGs, explicit `depends` is invalid, including an empty sequence.
- Every dependency entry must identify an existing step by `name` or `id`.
- When a dependency identifies a step by `id`, the dependency is treated as a
  dependency on that step's runtime `name`.
- After loading, dependency lists identify runtime step names.
- Dependency entries are not trimmed. Matching is exact and case-sensitive.
- Dependencies must stay inside one DAG document.
- A dependency graph with a cycle cannot execute. A direct self-dependency is a
  cycle.

Execution-status behavior for failed, skipped, rejected, waiting, retrying, or
continued dependencies belongs to the step execution specs and lifecycle specs.
This spec only defines how dependency references identify steps.

Example using a stable id:

```yaml
steps:
  - id: build
    name: Build image
    run: ./build.sh

  - id: deploy
    name: Deploy image
    depends: build
    run: ./deploy.sh
```

Example using a runtime step name:

```yaml
steps:
  - id: build
    name: Build image
    run: ./build.sh

  - id: deploy
    name: Deploy image
    depends: Build image
    run: ./deploy.sh
```

### Value References

Strict step-output references under the `steps` namespace use step ids:

```text
${steps.step_id.outputs.output_name}
```

Rules:

- `step_id` must identify an existing step `id`.
- A step without `id` cannot be referenced by a strict step-output reference.
- `output_name` identifies a top-level output declared and published by the
  referenced step.
- Step-output reference syntax, supported fields, escaping, dependency
  requirements, passive notices, and runtime lookup behavior are defined by
  [Spec 007: Value Resolution Steps](007-value-resolution-steps.md).
- Step output declaration and publication are defined by
  [Spec 012: Step Outputs](012-step-outputs.md).

Unknown, unavailable, unsupported, or out-of-scope step-output references follow
the passive notice and preservation rules in Spec 007. They are not validation
failures by themselves.

Example:

```yaml
steps:
  - id: build
    name: Build image
    run: |
      printf 'image=v1.2.3\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image

  - id: deploy
    name: Deploy image
    depends: build
    env:
      IMAGE: ${steps.build.outputs.image}
    run: ./deploy.sh "$IMAGE"
```

### Handler Steps

Lifecycle handler fields are outside this spec's executable-step identity and
dependency scope.

Rules:

- Handler steps do not participate in the `steps` dependency graph.
- Handler fields do not have step-output lookup scope under Spec 007.
- A strict step-output reference in a handler field is preserved and reported as
  a passive notice with reason `namespace_unavailable`.

### Runtime Surfaces

Rules:

- Runtime status surfaces that expose a step object include the runtime `name`.
- Runtime status surfaces include `id` when the step defines `id`.
- Surfaces that address a step for logs, approvals, retries, status nodes, or
  DAG-run control use runtime step names unless the owning spec explicitly says
  otherwise.
- Strict step-output value resolution uses step ids.

### Validation Outputs

Step reference validation does not write workflow events, run logs, artifacts,
or result files.

## Errors

Validation must fail when:

- Two executable steps in one DAG document use the same `name`.
- Two executable steps in one DAG document use the same `id`.
- A step `id` has invalid syntax.
- A step `id` is longer than 40 UTF-8 bytes.
- A step `id` uses a reserved word.
- A step `id` conflicts with another step's `name`.
- A step `name` conflicts with another step's `id`.
- A step `name` is longer than 255 UTF-8 bytes.
- `depends` has an invalid shape.
- `depends` is present in a chain DAG.
- `depends` names an unknown step name or id.

Execution must fail before running steps when the dependency graph contains a
cycle.

## Examples

Valid unreferenced step without `id`:

```yaml
steps:
  - name: Say hello
    run: echo hello
```

Valid step whose `name` is promoted from `id`:

```yaml
steps:
  - id: say_hello
    run: echo hello
```

Invalid duplicate ids:

```yaml
steps:
  - id: build
    name: Build image
    run: ./build.sh
  - id: build
    name: Build image again
    run: ./build-again.sh
```

Invalid id/name conflict:

```yaml
steps:
  - id: build
    name: Build image
    run: ./build.sh
  - name: build
    run: ./other.sh
```

Invalid cycle:

```yaml
steps:
  - id: first
    depends: second
    run: echo first
  - id: second
    depends: first
    run: echo second
```
