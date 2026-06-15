# Spec: Step Reference

## Implementation Status

Not implemented. This spec describes target conformance behavior and must not be
treated as current product behavior.

## Scope

This spec defines step identity, dependencies, and step references used by value resolution.

It does not define how a step runs, how outputs are produced, or how output files are parsed.

## Goal

Make the currently supported step identity rules explicit.

`name` is the runtime step identity and must be unique. `id` is an optional
stable alias used by strict `${steps.<id>.outputs.<name>}` value references.

## Input

Input is a workflow YAML file accepted by the YAML schema spec.

Step reference validation extends:

```sh
dagu validate <path/to/dag_file>
```

Rules:

- Validation checks step ids, dependencies, and statically checkable step references.
- Validation must not execute steps.

## Step identity

| Field | Meaning | Rules |
| --- | --- | --- |
| `name` | Runtime step identity and display text. | Required after loading. Must be unique inside one DAG document. |
| `id` | Optional stable value-reference alias. | Must be unique inside one DAG document when present. Must match `^[a-zA-Z][a-zA-Z0-9_]*$` when present. |

Rules:

- A step referenced by strict `${steps.<id>.outputs.<name>}` must have `id`.
- A step without `id` may still be referenced by dependency fields through `name`.
- Step ids are scoped to one DAG document.
- Inline sub-DAG documents have their own independent step-id scopes.

## Dependencies

`depends` names runtime step names. For transition support, a dependency may
also name a step `id`; validation rewrites that dependency to the corresponding
step `name` before runtime planning.

Rules:

- `depends` may be a string or a non-empty sequence of strings.
- Every dependency must resolve to an existing step `name` or `id`.
- A step must not depend on itself.
- Dependencies must not contain cycles.
- DAG execution order must respect `depends`.
- A step must not start until every dependency completed successfully.
- If a dependency fails, dependent steps must not start.

Example:

```yaml
steps:
  - id: build
    name: build-image
    run: ./build.sh

  - id: deploy
    depends: build
    run: ./deploy.sh
```

This is also valid because `depends` uses the runtime step name:

```yaml
steps:
  - id: build
    name: Build image
    run: ./build.sh

  - id: deploy
    depends: Build image
    run: ./deploy.sh
```

## Value references

Step references under the `steps` namespace use step ids:

```text
${steps.step_id.outputs.name}
```

Rules:

- `step_id` must be an existing step `id`.
- The referenced step must complete before the owning step starts.
- Output declaration and publication behavior belongs to the output-owning field spec.

Example:

```yaml
steps:
  - id: build
    name: build-image
    run: ./build.sh
    output:
      image:
        from: stdout

  - id: deploy
    depends: build
    run: ./deploy.sh ${steps.build.outputs.image}
```

## Outputs

Step reference validation does not write workflow events, run logs, artifacts, or result files.

Runtime step status and runtime outputs must identify a step by `id` when `id` is defined.

## Errors

Validation or execution must fail when:

- Two steps in one DAG document use the same `id`.
- Two steps in one DAG document use the same `name`.
- A step `id` has invalid syntax.
- `depends` names an unknown step name or id.
- `depends` names the owning step.
- Dependencies contain a cycle.
- A value reference uses an unknown `steps.<step_id>`.
- A field attempts to reference a step that has no `id`.

## Examples

Valid unreferenced step without `id`:

```yaml
steps:
  - name: Say hello
    run: echo hello
```

Invalid duplicate ids:

```yaml
steps:
  - id: build
    run: ./build.sh
  - id: build
    run: ./build-again.sh
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

## Acceptance criteria

- A black-box fixture verifies `dagu validate` accepts a referenced step with `id`.
- A black-box fixture verifies `dagu validate` accepts an unreferenced step without `id`.
- A black-box fixture verifies `dagu validate` does not execute steps.
- A black-box fixture verifies `dagu validate` rejects duplicate step ids.
- A black-box fixture verifies `dagu validate` rejects invalid step id syntax.
- A black-box fixture verifies `dagu validate` accepts `depends` that references a step `name`.
- A black-box fixture verifies `dagu validate` accepts `depends` that references a step `id` and executes by the resolved step `name`.
- A black-box fixture verifies `dagu validate` rejects an unknown `depends` reference.
- A black-box fixture verifies `dagu validate` rejects self-dependency.
- A black-box fixture verifies `dagu validate` rejects dependency cycles.
- A black-box fixture verifies `dagu run` does not start `step_b` before `step_a` completes successfully when `step_b` declares `depends: step_a`.
- A black-box fixture verifies `dagu run` exits with code `0` when every step in a dependency chain completes successfully.
- A black-box fixture verifies `dagu run` does not start a step whose dependency failed, and exits non-zero.
- A black-box fixture verifies `dagu run` resolves `${steps.step_id.outputs.name}` by step `id`.
