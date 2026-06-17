# Spec: Value Resolution Params

## Implementation Status

Implemented.

## Scope

This spec defines `${params.name}` references.

Common reference syntax is defined by [Spec 003: Value Resolution](003-value-resolution.md).
Spec 003 also defines unbraced text preservation, supported fields, string insertion, and resolution timing.

This spec does not define parameter declaration schema beyond the rules needed for value resolution.

This spec does not define positional parameter behavior.
It only states that positional parameters are not addressable through the `params` namespace.

## Goal

Supported workflow fields can reference named runtime parameters.

## Motivation

Runtime parameters let a workflow caller provide values without editing the workflow file.
Value resolution needs a named lookup rule.
Supported fields can then use those values predictably.

This spec separates declaration validation from runtime value availability.
`dagu validate` can reject invalid parameter declarations.
Unknown or missing parameter values preserve the original reference text.
Explicit inspection surfaces report passive notices for preserved parameter references.

## Behavior

### Declarations

- Parameter names used by `${params.name}` must be declared by name.

- Parameter names must match `^[A-Za-z][A-Za-z0-9_]*$`.

- Positional parameters are not addressable through the `params` namespace.

- `params[].default` and declaration metadata do not support Dagu references.

- `params[].eval` is dynamic-evaluated by Spec 010 and Spec 011.
- `params[].eval` is not a normal value-resolution field.
- Dagu references inside `params[].eval` use this spec's reference form, declaration rules, and runtime lookup rules.

### Reference Form

- `${params.name}` reads the runtime value for the declared parameter `name`.

- A braced expression is a `params` reference only when it matches `${params.name}`.
- The `name` part must match the parameter name rule.

- Braced text that does not match `${params.name}` is not interpreted by the `params` namespace.
- Dagu preserves unsupported braced text as ordinary string content.

- `$params.name` is not Dagu-owned params reference syntax.
- Dagu preserves `$params.name` as ordinary string content.

- Other expression syntax is outside this spec.

### Field Availability

- `${params.name}` is available in Spec 003 value-resolution fields that resolve after runtime params are available.

- `${params.name}` is available inside `params[].eval`.
- Spec 010 and Spec 011 define the dynamic evaluation rules for `params[].eval`.

- `${params.name}` is not available in root `consts` list-form values.
- `consts` resolution can see only earlier `consts` entries.

- `${params.name}` is not available inside `params[].default` or declaration metadata.
- `params[].default` and declaration metadata are literal.

- The validator and runtime must use the same field availability rules.

### Single-Quoted Environment References

- Single-quoted `$NAME` and `${NAME}` are preserved during Dagu environment expansion.

### Runtime Values

- Runtime `params` are available after Dagu builds the run input.

- A resolved parameter value is inserted into string fields. Spec 003 defines the insertion rules.

- A declared `params` reference resolves when the runtime value exists.

- If the runtime value is missing, Dagu preserves the original reference text.
- Explicit inspection surfaces report a passive notice for that preserved reference.

## Errors

- An invalid parameter declaration name must fail during workflow validation.

- An undeclared `params` reference in a value-resolution field must preserve the original reference text.
- Explicit inspection surfaces must report a passive notice for that preserved reference.

- In workflows with only positional parameters, a `${params.name}` reference has no matching named declaration and must preserve the original reference text.
- Explicit inspection surfaces must report a passive notice for that preserved reference.

- A `${params.name}` reference in a field where params are unavailable must preserve the original reference text.
- Explicit inspection surfaces must report a passive notice for that preserved reference.

- A declared `params` reference with no runtime value must preserve the original reference text.
- Explicit inspection surfaces must report a passive notice for that preserved reference.

- Notices must identify the owning field and the original reference text.

## Field Matrix

This matrix defines the required `${params.name}` behavior for value-resolution fields.

| Spec 003 field surface | Params behavior |
| --- | --- |
| `consts` list form | `${params.name}` is unavailable because only earlier `${consts.*}` entries are visible. Dagu preserves the original reference text. Explicit inspection surfaces report a passive notice. |
| `params[].eval` | `${params.name}` references in `eval` resolve before command substitution. Missing runtime values preserve before the evaluated parameter is consumed. Explicit inspection surfaces report a passive notice. |
| `env` | Root environment values in map form, array-of-map form, and `KEY=value` list form resolve declared params. |
| `dotenv[]` | Each dotenv path string resolves declared params. |
| `shell`, `shell_args[]`, `working_dir` | Root shell command, shell args, and working directory resolve declared params. |
| `preconditions[].condition` | Root precondition condition strings resolve declared params. |
| root `container` | Container string form resolves declared params. In object form, `exec`, `image`, `name`, `user`, `working_dir`, `network`, `volumes[]`, `ports[]`, `env` values, `command[]`, and `shell[]` resolve declared params. |
| `steps[].run` | String-form `run` and each array-form `run` entry resolve declared params. |
| `steps[].with` | Every nested string value under the step `with` object resolves declared params. |
| `steps[].working_dir` | Step working directory resolves declared params. |
| `steps[].env` | Step environment values in map form, array-of-map form, and `KEY=value` list form resolve declared params. |
| `steps[].preconditions[].condition` | Step precondition condition strings resolve declared params. |
| `steps[].repeat_policy.condition` | Repeat condition strings resolve declared params. |
| `steps[].parallel` | `variable`, `items[]`, `items[].value`, and `items[].params.*` string values resolve declared params. |
| `steps[].stdout`, `steps[].stdout.artifact` | Stdout file path strings and artifact path strings resolve declared params. |
| `steps[].stderr`, `steps[].stderr.artifact` | Stderr file path strings and artifact path strings resolve declared params. |
| `steps[].stdout.outputs.fields.*` | Literal string values under field entries resolve declared params. |
| `steps[].output.*` | Literal string values and `path` strings under structured output entries resolve declared params. |
| `steps[].container` | Step container string form resolves declared params. In object form, `exec`, `image`, `name`, `user`, `working_dir`, `network`, `volumes[]`, `ports[]`, `env` values, `command[]`, and `shell[]` resolve declared params. |
| handler steps | The same step-owned cases apply under `handler_on.init`, `handler_on.success`, `handler_on.failure`, `handler_on.abort`, `handler_on.exit`, and `handler_on.wait`. |

## Examples

Valid `params` reference:

```yaml
params:
  - name: environment
    type: string
    required: true
steps:
  - name: deploy
    run: ./deploy.sh ${params.environment}
```

Undeclared `params` reference:

```yaml
params:
  - name: environment
    type: string
    required: true
steps:
  - name: preserved
    run: echo ${params.region}
```

Unbraced params-looking text is ordinary content:

```yaml
params:
  - name: environment
    type: string
    required: true
steps:
  - name: script
    run: |
      php -r '$params.environment = "literal"; echo $params.environment;'
```

Params reference where params are unavailable:

```yaml
params:
  - name: environment
    type: string
    required: true
consts:
  - target: ${params.environment}
steps:
  - name: deploy
    run: echo ${consts.target}
```
