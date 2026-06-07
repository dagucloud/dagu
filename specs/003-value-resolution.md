# Spec: Value Resolution

Scope: Dagu reference syntax, root `consts`, and runtime value lookup.

## Objective

Define how workflow fields reference values without conflicting with shell
environment variable syntax.

## Inputs

Input is a workflow YAML file accepted by the YAML schema spec.

The workflow may define immutable values with `consts`:

```yaml
consts:
  service: api
  deploy_script: ./scripts/deploy.sh
  enabled: true
```

Step `run` fields may contain Dagu references:

```yaml
steps:
  - name: deploy
    run: ${{ consts.deploy_script }} ${{ params.environment }}
```

## Behavior

Dagu references use this syntax:

```text
${{ path }}
```

Whitespace after `${{` and before `}}` is ignored.

The path must be namespaced.

Supported namespaces:

| Namespace | Meaning |
| --- | --- |
| `consts` | immutable values from root `consts` |
| `params` | runtime parameters |
| `steps` | outputs from completed steps, addressed by step `id` |

Supported reference forms:

```text
${{ consts.name }}
${{ params.name }}
${{ steps.step_id.outputs.name }}
```

Reference path segments must use `snake_case`.

Unqualified references such as `${{ name }}` are invalid.

Function call expressions are defined by the expression functions spec.

Operators, filters, and inline default values are not part of this spec.

This spec requires value resolution for step `run` fields. Other fields support
value resolution only when their own specs say so.

Root `consts` must be a mapping.

`consts` keys must use `snake_case`.

`consts` values must be literal strings, numbers, or booleans.

`consts` values must not contain Dagu references.

`consts` values are immutable for one DAG run.

Runtime parameters are referenced only through `params`.

Step outputs are referenced only through `steps.<step_id>.outputs.<name>`.

Step `id` behavior is defined by the step reference spec.

When a referenced value is inserted into a string field, strings are inserted as
written, booleans are inserted as `true` or `false`, and numbers are inserted in
base-10 form.

Dagu must not resolve shell-style `$NAME` or `${NAME}` syntax.

Shell-style variables remain part of the field value after Dagu value
resolution. A shell or target runtime may expand them later.

## Outputs

Value resolution does not write workflow result events, run logs, or artifacts.

When value resolution succeeds for a step field, the step receives the resolved
field value.

## Errors

Malformed Dagu reference syntax must fail before the owning step starts.

An unknown namespace must fail before the owning step starts.

An unknown `consts` reference must fail before the owning step starts.

An unknown `params` reference must fail before the owning step starts.

An unknown `steps` output reference must fail before the owning step starts.

An unavailable step output must fail before the owning step starts.

Invalid `consts` shape must fail during workflow validation.

Invalid `consts` key names must fail during workflow validation.

Invalid `consts` value types must fail during workflow validation.

## Examples

Valid consts and parameters:

```yaml
consts:
  deploy_script: ./scripts/deploy.sh
  service: api
params:
  - name: environment
    type: string
    required: true
steps:
  - name: deploy
    run: ${{ consts.deploy_script }} ${{ params.environment }} ${{ consts.service }}
```

Shell variables are not resolved by Dagu:

```yaml
steps:
  - name: shell_env
    run: echo "$HOME ${HOME}"
```

Invalid unqualified reference:

```yaml
steps:
  - name: bad
    run: echo ${{ environment }}
```

Invalid consts reference:

```yaml
steps:
  - name: bad
    run: echo ${{ consts.missing }}
```

## Acceptance Criteria

- A black-box fixture verifies `dagu workflow validate` accepts literal
  `consts` values.
- A black-box fixture verifies `dagu workflow validate` rejects invalid
  `consts` value types.
- A black-box fixture verifies `dagu workflow validate` rejects an unqualified
  Dagu reference.
- A black-box fixture verifies `dagu run` resolves `${{ consts.name }}`.
- A black-box fixture verifies `dagu run` resolves `${{ params.name }}`.
- A black-box fixture verifies `dagu run` resolves
  `${{ steps.step_id.outputs.name }}` after the referenced step completes.
- A black-box fixture verifies missing Dagu references fail before the owning
  step starts.
- A black-box fixture verifies `$NAME` and `${NAME}` are not resolved by Dagu.
