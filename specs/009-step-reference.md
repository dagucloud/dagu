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
- If a dependency fails, the DAG run fails.

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

- A step output reference can resolve only when `step_id` is an existing step `id`.
- A step output reference can resolve only after the referenced step completes.
- Output declaration and publication behavior belongs to the output-owning field spec.
- Missing or unavailable step output references follow the passive diagnostic preservation rules in Spec 007.

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
