# Spec: Step Reference

Scope: step identity, dependency references, and references from value
resolution.

## Objective

Define how one step is identified and referenced by another step.

## Inputs

Input is a workflow YAML file accepted by the YAML schema spec.

Step reference validation uses:

```sh
dagu workflow validate <workflow_file>
```

The command validates step ids and step references.

The command must not execute steps.

## Behavior

Each step may define `id`.

`id` is the canonical step reference.

`id` must be unique inside one DAG document.

`id` must match `[a-z][a-z0-9_]*`.

Each step may define `name`.

`name` is display text.

`name` is not a step reference.

`name` does not need to use `snake_case`.

`name` does not need to be unique.

When a step is referenced, the referenced step must have `id`.

Steps without `id` are valid only when no field references them.

`depends` references step ids.

`depends` may be a string or a non-empty sequence of strings.

A step must not depend on itself.

Dependencies must not contain cycles.

DAG execution order must respect `depends`.

Value references under the `steps` namespace use step ids:

```text
${{ steps.step_id.outputs.name }}
```

A `steps.<step_id>.outputs.<name>` reference requires the referenced step to
complete before the owning step starts.

Output declaration and publication behavior is defined by the step outputs
spec.

Step ids are scoped to one DAG document.

Inline sub-DAG documents have independent step-id scopes.

## Outputs

Step reference validation does not write workflow result events, run logs, or
artifacts.

Runtime step status and outputs must identify steps by `id` when `id` is
defined.

## Errors

Duplicate step ids in one DAG document must fail before execution.

Invalid step id syntax must fail before execution.

An unknown `depends` reference must fail before execution.

A self-dependency must fail before execution.

A dependency cycle must fail before execution.

An unknown `steps.<step_id>` value reference must fail before the owning step
starts.

An attempt to reference a step without `id` must fail before execution.

## Examples

Valid dependency and output reference:

```yaml
steps:
  - id: build
    name: Build image
    run: |
      printf 'image=v1.2.3\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image

  - id: deploy
    name: Deploy service
    depends: build
    run: ./deploy.sh ${{ steps.build.outputs.image }}
```

Valid unreferenced step without `id`:

```yaml
steps:
  - name: Say hello
    run: echo hello
```

Invalid dependency on display name:

```yaml
steps:
  - id: build
    name: Build image
    run: ./build.sh

  - id: deploy
    depends: Build image
    run: ./deploy.sh
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

## Acceptance Criteria

- A black-box fixture verifies `dagu workflow validate` accepts a referenced
  step with `id`.
- A black-box fixture verifies `dagu workflow validate` accepts an unreferenced
  step without `id`.
- A black-box fixture verifies `dagu workflow validate` does not execute steps.
- A black-box fixture verifies `dagu workflow validate` rejects duplicate step
  ids.
- A black-box fixture verifies `dagu workflow validate` rejects invalid step id
  syntax.
- A black-box fixture verifies `dagu workflow validate` rejects `depends` that
  references a step `name`.
- A black-box fixture verifies `dagu workflow validate` rejects an unknown
  `depends` reference.
- A black-box fixture verifies `dagu workflow validate` rejects self-dependency.
- A black-box fixture verifies `dagu workflow validate` rejects dependency
  cycles.
- A black-box fixture verifies `dagu run` resolves
  `${{ steps.step_id.outputs.name }}` by step `id`.
