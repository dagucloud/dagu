# Spec: Value Resolution Params

## Implementation Status

Not implemented. This spec describes target conformance behavior and must not be
treated as current product behavior.

## Scope

This spec defines `${params.name}` references.

Common reference syntax, invalid shorthand rules, supported fields, string
insertion, and resolution timing are defined by [Spec 003: Value Resolution](003-value-resolution.md).

This spec does not define parameter declaration schema beyond the rules needed
for value resolution.

This spec does not define positional parameter behavior except to say that
positional parameters are not addressable through the `params` namespace.

## Goal

Supported workflow fields can reference named runtime parameters.

## Motivation

Runtime parameters let a workflow caller provide values without editing the
workflow file. Value resolution needs a named lookup rule so supported fields can
use those values predictably.

This spec separates declaration validation from runtime value availability.
`dagu validate` can reject unknown parameter names, while missing runtime values
fail only when the run input does not provide them.

## Behavior

### Declarations

- Parameter names used by `${params.name}` must be declared by name.

- Parameter names must match `^[A-Za-z][A-Za-z0-9_]*$`.

- Positional parameters are not addressable through the `params` namespace.

- `params` declarations do not support Dagu references.

### Reference Form

- `${params.name}` reads the runtime value for the declared parameter `name`.

- A braced expression is a `params` reference only when it matches
  `${params.name}` and `name` matches the parameter name rule.

- Braced text that does not match `${params.name}` is not interpreted by the
  `params` namespace and is preserved as ordinary string content.

- `$params.name` is invalid Dagu-looking shorthand and must fail validation in a
  value-resolution field.

- Other expression syntax is outside this spec.

### Field Availability

- `${params.name}` is available in every Spec 003 value-resolution field whose
  resolution timing occurs after runtime params are available.

- `${params.name}` is not available in root `consts` list-form values because
  `consts` resolution can see only earlier `consts` entries.

- `${params.name}` is not available inside `params` declarations because
  `params` declarations do not support Dagu references.

- The validator and runtime must use the same field availability rules.

### Single-Quoted Environment References

- Single-quoted `$NAME` and `${NAME}` are preserved during Dagu environment
  expansion.

### Runtime Values

- Runtime `params` are available after Dagu builds the run input.

- A resolved parameter value is inserted into string fields according to Spec 003
  string insertion rules.

- A missing runtime value for a declared `params` reference must fail before the
  owning field is used.

## Errors

- An invalid parameter declaration name must fail during workflow validation.

- `$params.name` in a value-resolution field must fail during workflow
  validation.

- An undeclared `params` reference in a value-resolution field must fail during
  workflow validation.

- In a workflow that declares only positional parameters, a `${params.name}`
  reference must fail during workflow validation because there is no matching
  named declaration.

- A `${params.name}` reference in a field where params are unavailable must fail
  during workflow validation when statically knowable.

- A missing runtime value for a declared `params` reference must fail before the
  owning field is used.

- Validation and runtime failures must include enough field context for black-box
  tests to identify the invalid field.

## Black-Box Conformance Viewpoint

Black-box coverage for this spec is exhaustive only when every Spec 003
value-resolution field has an explicit params case.

For each field where params are available, the conformance suite must include:

- a valid declared-name case that proves `${params.name}` resolves;
- an undeclared-name validation failure for `${params.missing}`;
- an invalid shorthand validation failure for `$params.name`;
- a missing runtime value case that proves the owning field is not used after
  the missing value is detected.

Missing-value cases should be grouped by resolution timing class for review, but
each listed field where params are available needs coverage unless an exception
is explicitly accepted.

The exhaustive field viewpoint is:

| Spec 003 field surface | Params conformance case |
| --- | --- |
| `consts` list form | Negative case: `${params.name}` is unavailable because only earlier `${consts.*}` entries are visible. |
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
| `steps[].stdout.outputs.fields.*` | Literal string values and `path` strings under field entries resolve declared params. |
| `steps[].output.*` | Literal string values and `path` strings under structured output entries resolve declared params. |
| `steps[].container` | Step container string form resolves declared params. In object form, `exec`, `image`, `name`, `user`, `working_dir`, `network`, `volumes[]`, `ports[]`, `env` values, `command[]`, and `shell[]` resolve declared params. |
| handler steps | The same step-owned cases apply under `handler_on.init`, `handler_on.success`, `handler_on.failure`, `handler_on.abort`, `handler_on.exit`, and `handler_on.wait`. |

Any hard-to-test exception must be named, justified, and kept small. A spec
cannot be marked implemented while an unaccepted exception leaves a listed field
without params coverage.

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

Invalid undeclared `params` reference:

```yaml
params:
  - name: environment
    type: string
    required: true
steps:
  - name: bad
    run: echo ${params.region}
```

Invalid params shorthand:

```yaml
params:
  - name: environment
    type: string
    required: true
steps:
  - name: bad
    run: echo $params.environment
```

Invalid params reference from `consts`:

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
